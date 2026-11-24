package gateway

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"

	"rf4server/internal/config"
	"rf4server/internal/metrics"
	"rf4server/internal/portal"
	"rf4server/internal/proto"
)

// 指标（NewLoginServer 里注册；nil 时为 no-op 计数器）。
var (
	loginRequests prometheus.Counter
	loginIssued   prometheus.Counter
	loginErrors   prometheus.Counter
)

// GreetingUUID 客户端校验的服务端问候（协议常量，实现在 proto.GreetingUUID）。
const GreetingUUID = proto.GreetingUUID

const loginXMLTpl = `<logon success="true"><host>%s</host><port>%d</port>` +
	`<region>%s</region><server>%s</server><userid>%d</userid>` +
	`<token>%s</token><logintoken>%s</logintoken>` +
	`<logintokenexpires>%d</logintokenexpires></logon>`

// legacyLoginXMLTpl 是旧版 CN 客户端在 7515 明文兼容口上要求的响应格式。
// 客户端按 <response> 根节点解析；返回 <logon> 会报 Invalid URI。
const legacyLoginXMLTpl = `<?xml version="1.0" encoding="UTF-8"?>
<response success="true"><token>%s</token><host>%s</host><port>%d</port>` +
	`<region>%s</region><userid>%d</userid><logintoken>%s</logintoken>` +
	`<logintokenexpires>%d</logintokenexpires></response>`

// LoginServer HTTPS 登录服务：任何路径都返回 <logon> XML（客户端打 /login.php 或 /steam.php）。
type LoginServer struct {
	cfg       config.Gateway
	store     Store
	log       *slog.Logger
	handler   http.Handler
	srv       *http.Server
	ln        net.Listener
	compatSrv *http.Server
	compatLn  net.Listener
	userid    int64 // 本进程内递增序号，共享序号不可用时使用
}

// NewLoginServer 构建登录服务（含观测指标注册）。
func NewLoginServer(cfg config.Gateway, store Store, log *slog.Logger, obs *metrics.Server) (*LoginServer, error) {
	s := &LoginServer{cfg: cfg, store: store, log: log}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("加载 TLS 证书: %w", err)
	}
	// RF4 CN 客户端会把上游 URL 原样编码进路径，例如：
	// /lv54/https://api.rf4game.ru/login.php。http.ServeMux 会把其中的
	// 双斜杠清理为单斜杠并返回 307，客户端随后会因 URI 被破坏而登录失败。
	// 登录服务本来就接受任意路径，因此直接使用 HandlerFunc，保留 RequestURI。
	s.handler = http.HandlerFunc(s.handleLogin)
	if storage, ok := store.(*SQLiteStore); ok {
		accountPortal := portal.New(storage.DB, s.portalUserid)
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/portal/") {
				accountPortal.ServeHTTP(w, r)
				return
			}
			s.handleLogin(w, r)
		})
	}
	s.srv = &http.Server{
		Addr:              cfg.LoginAddr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		},
	}
	if cfg.LoginCompatAddr != "" {
		s.compatSrv = &http.Server{
			Addr:              cfg.LoginCompatAddr,
			Handler:           s.handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
	}
	// 指标（无观测端口时用本地一次性注册表，避免 nil 指标 panic）
	if obs != nil {
		reg := obs.Registry()
		loginRequests = metrics.NewCounter(reg, "gateway_login_requests_total", "登录请求数")
		loginIssued = metrics.NewCounter(reg, "gateway_tokens_issued_total", "签发 token 数")
		loginErrors = metrics.NewCounter(reg, "gateway_login_errors_total", "登录处理错误数")
	} else {
		reg := prometheus.NewRegistry()
		loginRequests = metrics.NewCounter(reg, "gateway_login_requests_total", "登录请求数")
		loginIssued = metrics.NewCounter(reg, "gateway_tokens_issued_total", "签发 token 数")
		loginErrors = metrics.NewCounter(reg, "gateway_login_errors_total", "登录处理错误数")
	}
	return s, nil
}

// Start 创建 TLS listener 并开始服务；返回错误 channel 与绑定错误。
func (s *LoginServer) Start() (<-chan error, error) {
	ln, err := net.Listen("tcp", s.cfg.LoginAddr)
	if err != nil {
		return nil, fmt.Errorf("登录服务监听 %s: %w", s.cfg.LoginAddr, err)
	}
	s.ln = tls.NewListener(ln, s.srv.TLSConfig)
	errCh := make(chan error, 2)
	go func() {
		if err := s.srv.Serve(s.ln); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("登录服务 %s: %w", s.cfg.LoginAddr, err)
		}
	}()
	if s.compatSrv != nil {
		compatLn, err := net.Listen("tcp", s.cfg.LoginCompatAddr)
		if err != nil {
			_ = s.ln.Close()
			return nil, fmt.Errorf("登录兼容口监听 %s: %w", s.cfg.LoginCompatAddr, err)
		}
		s.compatLn = compatLn
		go func() {
			if err := s.compatSrv.Serve(s.compatLn); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("登录兼容口 %s: %w", s.cfg.LoginCompatAddr, err)
			}
		}()
	}
	return errCh, nil
}

// Addr 实际监听地址（Start 之后有效）。
func (s *LoginServer) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// CompatAddr 明文 HTTP 兼容口的实际监听地址；未启用时为空。
func (s *LoginServer) CompatAddr() string {
	if s.compatLn == nil {
		return ""
	}
	return s.compatLn.Addr().String()
}

// Shutdown 优雅停机。
func (s *LoginServer) Shutdown(ctx context.Context) error {
	var first error
	if s.compatSrv != nil {
		first = s.compatSrv.Shutdown(ctx)
	}
	if err := s.srv.Shutdown(ctx); first == nil {
		first = err
	}
	return first
}

func (s *LoginServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	loginRequests.Inc()
	_ = r.Body.Close()
	// 身份优先级：登录名（后台已建档的同名玩家）→ token 自带 uid → 来源 IP。
	// 登录名存在时不再看 IP，这样同一台机器可以靠输入不同名字切换账号；
	// 名字没建档则回落到 IP 账号，保持旧客户端与「只认本机固定号」的行为。
	userid, accountSource := s.resolveLoginUserid(r.Context(), r)
	if userid <= 0 {
		userid, _ = s.nextUserid(r.Context())
		accountSource = "seq"
	}
	info := TokenInfo{Userid: userid, IssuedAt: time.Now().Unix(), Region: s.cfg.Region}
	ttl := time.Duration(s.cfg.TokenTTL) * 24 * time.Hour
	expires := time.Now().Add(ttl).Unix()
	// 给个人中心留一条「该来源 IP 最近登录的账号」：同一台机器用多个登录名时，
	// /portal/ 跟随最近玩的号，不再固定落到该 IP 的默认账号。
	s.rememberLoginUID(r.Context(), r.RemoteAddr, userid)
	var token, logintoken, xml string
	if r.TLS == nil {
		// 旧版 CN 客户端会把 token 当 URI 片段使用，不能包含 '|' 或 '/'。
		// 这里沿用旧后端的 32hex token / 48hex logintoken，并登记到同一 SQLite。
		token = NewLegacyToken()
		logintoken = NewLegacyLoginToken()
		if err := s.store.SaveToken(r.Context(), token, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存兼容 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := s.store.SaveToken(r.Context(), logintoken, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存兼容登录 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		xml = fmt.Sprintf(legacyLoginXMLTpl,
			token, s.cfg.AdvertiseHost, s.cfg.AdvertisePort,
			s.cfg.Region, userid, logintoken, expires)
	} else {
		token = NewToken(userid)
		if err := s.store.SaveToken(r.Context(), token, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// HTTPS 作者通道继续使用 4 段 logintoken。
		logintoken = NewLogintoken(userid)
		xml = fmt.Sprintf(loginXMLTpl,
			s.cfg.AdvertiseHost, s.cfg.AdvertisePort,
			s.cfg.Region, s.cfg.ServerName, userid,
			token, logintoken, expires)
	}
	loginIssued.Inc()
	if r.TLS == nil {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml))
	s.log.Info("登录",
		"method", r.Method, "request_uri", r.RequestURI, "path", r.URL.Path,
		"raw_path", r.URL.RawPath, "remote", r.RemoteAddr,
		"userid", userid, "account", accountSource, "region", s.cfg.Region, "realtime", s.cfg.AdvertiseHost)
}

// loginNameMaxRunes 与后台「新建玩家」的昵称上限一致：登录名即档案昵称。
const loginNameMaxRunes = 32

// normalizeLoginName 清洗客户端登录名：去首尾空白；超长或含控制字符按未填处理
// （返回空串，调用方回落到下一级身份来源），避免异常名字在库里留下脏键。
func normalizeLoginName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > loginNameMaxRunes {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return name
}

// resolveLoginUserid 决定本次登录的 uid，并把取值来源一并返回供日志排查：
//
//	login=  → 后台已建档的同名玩家（或已登记的映射），ASCII 大小写不敏感；
//	token=  → token 自带的 uid（先查登记表，未登记按 local|…|uid|… 解析，与实时通道一致）；
//	其余    → 沿用来源 IP 分配（本机固定 userid_start，局域网按来源 IP 递增）。
//
// 登录名没建档时既不新建也不报错：新号必须先在后台登记，输错名字只会回到 IP 账号，
// 不会凭空多出一个空档案。
func (s *LoginServer) resolveLoginUserid(ctx context.Context, r *http.Request) (int64, string) {
	q := r.URL.Query()
	if name := normalizeLoginName(q.Get("login")); name != "" {
		if storage, ok := s.store.(*SQLiteStore); ok {
			uid, found, err := storage.ResolveLoginUID(ctx, name)
			switch {
			case err != nil:
				s.log.Warn("登录名解析失败，回落到来源 IP", "login", name, "err", err)
			case found:
				return uid, "login"
			default:
				s.log.Warn("登录名未建档，回落到来源 IP", "login", name)
			}
		}
	}
	if token := strings.TrimSpace(q.Get("token")); token != "" {
		if info, ok, err := s.store.LookupToken(ctx, token); err == nil && ok && info.Userid > 0 {
			return info.Userid, "token"
		}
		if uid := useridFromTokenStr(token); uid > 0 {
			return uid, "token-uid"
		}
	}
	return s.useridForRemote(r.RemoteAddr), "remote-ip"
}

// hostOfRemote 取出来源主机（去掉端口）。
func hostOfRemote(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}

// rememberLoginUID 记录该来源 IP 最近一次成功登录的 uid，供 /portal/ 跟随。
func (s *LoginServer) rememberLoginUID(ctx context.Context, remote string, uid int64) {
	storage, ok := s.store.(*SQLiteStore)
	if !ok || uid <= 0 {
		return
	}
	_ = storage.DB.KVSet(ctx, "last_login_uid:"+hostOfRemote(remote), strconv.FormatInt(uid, 10))
}

// portalUserid 给个人中心解析角色：优先取该来源 IP 最近一次登录的 uid，
// 没有记录时沿用来源 IP 分配（与旧行为一致）。同一台机器换登录名后，
// 个人中心不会继续显示上一个号的资料与余额。
func (s *LoginServer) portalUserid(remote string) int64 {
	if storage, ok := s.store.(*SQLiteStore); ok {
		raw, found, err := storage.DB.KVGet(context.Background(), "last_login_uid:"+hostOfRemote(remote))
		if err == nil && found {
			if uid, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); perr == nil && uid > 0 {
				return uid
			}
		}
	}
	return s.useridForRemote(remote)
}

func (s *LoginServer) nextUserid(ctx context.Context) (int64, error) {
	// 从共享存储获取下一个 userid；共享存储不可用时退化为进程内序号。
	if v, err := s.store.NextUserid(ctx); err == nil {
		return v, nil
	} else {
		s.log.Warn("共享 userid 序号不可用，退化进程内序号", "err", err)
	}
	return atomic.AddInt64(&s.userid, 1), nil
}

// NewToken 生成 4 段 token：hex(32B)|unix|userid|base64(168B)。
// 整串是 RC4 密钥，客户端会在 realtime 握手时回传。
func NewToken(userid int64) string {
	var h [32]byte
	_, _ = rand.Read(h[:])
	var b [168]byte
	_, _ = rand.Read(b[:])
	return strings.Join([]string{
		hex.EncodeToString(h[:]),
		strconv.FormatInt(time.Now().Unix(), 10),
		strconv.FormatInt(userid, 10),
		base64.StdEncoding.EncodeToString(b[:]),
	}, "|")
}

// NewLegacyToken 生成旧版 CN 客户端使用的 32 位十六进制 token。
func NewLegacyToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewLegacyLoginToken 生成旧版 CN 客户端使用的 48 位十六进制 logintoken。
func NewLegacyLoginToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewLogintoken 生成登录 XML 使用的短 token，供辅助 realtime 通道认证。
// 该 token 同时作为 RC4 密钥；第三段是玩家 uid。
func NewLogintoken(userid int64) string {
	var s [6]byte
	_, _ = rand.Read(s[:])
	var t [8]byte
	_, _ = rand.Read(t[:])
	return strings.Join([]string{
		"local",
		hex.EncodeToString(s[:]),
		strconv.FormatInt(userid, 10),
		hex.EncodeToString(t[:]),
	}, "|")
}

// ---- 局域网/Radmin 多账号：按来源 IP 分配 userid ----

var (
	ipUIDOnce sync.Once
	ipUIDMu   sync.Mutex
	ipUIDMap  = map[string]int64{}
)

func (s *LoginServer) useridForRemote(remote string) int64 {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	base := s.cfg.UseridStart
	if base <= 0 {
		base = 1000001
	}
	// 本机保持原固定 UID，避免现有存档换号。
	if host == "" || host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return base
	}
	ipUIDOnce.Do(func() {
		if b, err := os.ReadFile("gateway_ip_uids.json"); err == nil {
			_ = json.Unmarshal(b, &ipUIDMap)
		}
		if ipUIDMap == nil {
			ipUIDMap = map[string]int64{}
		}
	})
	ipUIDMu.Lock()
	defer ipUIDMu.Unlock()
	if uid, ok := ipUIDMap[host]; ok && uid > 0 {
		return uid
	}
	max := base
	for _, v := range ipUIDMap {
		if v > max {
			max = v
		}
	}
	uid := max + 1
	ipUIDMap[host] = uid
	if b, err := json.MarshalIndent(ipUIDMap, "", "  "); err == nil {
		_ = os.WriteFile("gateway_ip_uids.json", b, 0o644)
	}
	s.log.Info("LAN 账号分配", "ip", host, "userid", uid)
	return uid
}
