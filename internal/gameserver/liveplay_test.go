package gameserver

// 线上协议级作钓探测（live playtest）：连正在运行的本服，发真实客户端报文。
//
// 与 TestPlaytestFishingFlow 的区别：这里不碰服务端内存，只走网络协议，
// 所以能验证「玩家视角真实看到的东西」——包括 14/14 推送的字段、档位、
// 重量、结算响应，以及延迟的实际体感。
//
// 运行：
//   RF4_LIVEPLAY=1 go test ./internal/gameserver -run TestLivePlay -v -timeout 30m

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"rf4server/internal/proto"
	"rf4server/internal/wiregen"
)

const (
	liveLoginURL  = "http://127.0.0.1:7515/login.php"
	liveRTAddr    = "127.0.0.1:9341"
	liveOutputDir = "../../data/playtest"
)

// liveClient 是最小可用的真实客户端：登录 + 握手 + RC4 帧泵。
type liveClient struct {
	t       *testing.T
	conn    net.Conn
	rc4In   *proto.RC4Stream
	rc4Out  *proto.RC4Stream
	wire    uint64
	mu      sync.Mutex
	buf     []byte
	respCh  chan *proto.Envelope
	pushCh  chan *proto.Envelope
	callSeq uint32
}

func liveplayEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("RF4_LIVEPLAY") != "1" {
		t.Skip("未设置 RF4_LIVEPLAY=1")
	}
}

// liveLogin 取 token（明文兼容口，与客户端一致）。
//
// 服务端身份优先级（gateway/login.go:resolveLoginUserid）：
// 登录名 login= → token= 自带 uid → 来源 IP。
// 这里只能传 login=：传 userid=/uid= 服务端不认，会静默回落到 IP 账号，
// 表现为「明明指定了 uid，登录结果却是 IP 默认号」。
func liveLogin(t *testing.T, nameHint string) (string, int64) {
	t.Helper()
	q := ""
	if nameHint != "" {
		q = "?login=" + url.QueryEscape(nameHint)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	c := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	resp, err := c.Get(liveLoginURL + q)
	if err != nil {
		t.Fatalf("登录请求失败（服务是否在跑？）: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, `success="true"`) {
		t.Fatalf("登录响应异常: %s", text)
	}
	tok := regexp.MustCompile(`<token>([^<]+)</token>`).FindStringSubmatch(text)
	uid := regexp.MustCompile(`<userid>(\d+)</userid>`).FindStringSubmatch(text)
	if tok == nil || uid == nil {
		t.Fatalf("登录缺 token/userid: %s", text)
	}
	var id int64
	fmt.Sscanf(uid[1], "%d", &id)
	return tok[1], id
}

func newLiveClient(t *testing.T, token string) *liveClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", liveRTAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("连接实时服: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &liveClient{t: t, conn: conn,
		respCh: make(chan *proto.Envelope, 256),
		pushCh: make(chan *proto.Envelope, 1024)}

	// 认证包：magic + flag + 长度 + token
	auth := []byte{proto.AuthMagic, 0x00}
	n := len(token)
	auth = append(auth, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
	auth = append(auth, token...)
	if _, err := conn.Write(auth); err != nil {
		t.Fatalf("发送认证: %v", err)
	}
	hdr := make([]byte, 36)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("读问候: %v", err)
	}
	if string(hdr) != proto.GreetingUUID {
		t.Fatalf("问候不符: %q", hdr)
	}
	hermes := []byte("<hermes><token>x</token></hermes>")
	if _, err := conn.Write(proto.BuildFrame(0, 1, hermes)); err != nil {
		t.Fatalf("发 hermes: %v", err)
	}
	c.rc4In, _ = proto.NewRC4([]byte(token))
	c.rc4Out, _ = proto.NewRC4([]byte(token))
	go c.pump()
	return c
}

func (c *liveClient) pump() {
	for {
		chunk := make([]byte, 65536)
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		n, err := c.conn.Read(chunk)
		if n > 0 {
			c.mu.Lock()
			c.buf = append(c.buf, chunk[:n]...)
			frames, consumed, ferr := proto.TakeCompleteFrames(c.buf)
			c.buf = c.buf[consumed:]
			c.mu.Unlock()
			if ferr == nil {
				for _, f := range frames {
					if f.FrameType != 0 || f.BodyLen < 9 {
						continue
					}
					plain := c.rc4Out.Crypt(f.Payload)
					env, ok := proto.ParseEnvelope(plain)
					if !ok {
						continue
					}
					switch env.Marker {
					case -2:
						select {
						case c.respCh <- env:
						default:
						}
					case -1:
						select {
						case c.pushCh <- env:
						default:
						}
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *liveClient) send(main, sub uint8, body []byte) uint32 {
	c.mu.Lock()
	c.callSeq++
	call := c.callSeq
	c.wire++
	wire := c.wire
	c.mu.Unlock()
	if len(body) == 0 {
		body = []byte{0x03, 0x03}
	}
	payload := proto.BuildRequestEnvelope(call, main, sub, append(body, 0x00))
	enc := c.rc4In.Crypt(payload)
	if _, err := c.conn.Write(proto.BuildFrame(0, wire, enc)); err != nil {
		c.t.Logf("发送 %d/%d 失败: %v", main, sub, err)
	}
	return call
}

func (c *liveClient) waitResp(d time.Duration) *proto.Envelope {
	select {
	case e := <-c.respCh:
		return e
	case <-time.After(d):
		return nil
	}
}

// drainPushes 取走一段时间内的所有推送。
func (c *liveClient) drainPushes(d time.Duration) []*proto.Envelope {
	deadline := time.After(d)
	var out []*proto.Envelope
	for {
		select {
		case e := <-c.pushCh:
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

// waitPush 等待指定 main/sub 的推送。
func (c *liveClient) waitPush(main, sub uint8, d time.Duration) *proto.Envelope {
	deadline := time.After(d)
	for {
		select {
		case e := <-c.pushCh:
			if e.Main == main && e.Sub == sub {
				return e
			}
		case <-deadline:
			return nil
		}
	}
}

// ---- 报文构造 ----

func liveArgs(guids ...string) *proto.Value {
	return proto.Args524(proto.GuidArr(guids))
}

// liveCastBody 构造 14/2 抛投报文。
//
// olieodeoaof 的真实字段布局（12 个，wiregen/tables.go:720）：
//
//	f0 FLOAT  requested 请求距离（f32 位型以 U32 承载）
//	f3 STR    场景名（hCast 用 findObjStr 取第一个非空 str）
//	f6 FLOAT  落点 X（castLandingPosition 读 bopigefkkom）
//	f7 FLOAT  落点 Z（castLandingPosition 读 ceicchfhpld）
//	f8 BYTE   抛投动作（0 短促 / 1 常规全挥杆 / 2 …），castActionFactor 据此给系数
//
// 其余 7 个字段（f1 f2 f4 f5 f9 f10 f11）交给 wiregen 补零：f5 是 OPT、
// f10 是位置关联对象，本地服务端不消费它们。字段必须凑满 12 个才能编码，
// 手写 proto.Obj + 2 个 Field 会在编码期报 "ops/fields length mismatch 12 vs 2"。
func liveCastBody(rod string, loc string, requestedM float32, action byte) *proto.Value {
	obj := wiregen.MustMakeObj("olieodeoaof", map[int]*proto.Value{
		0: proto.F32(requestedM),
		3: proto.StrVal(loc),
		6: proto.F32(0), // 落点 X：让 castLandingPosition 走「有落点」分支
		7: proto.F32(20),
		8: proto.U32(uint32(action)),
	})
	return proto.Args524(proto.GuidArr([]string{rod}), obj)
}

// liveWaterBody 构造 14/7 入水/钓组动画报文。
//
// lobemkloehp 15 个字段（wiregen/tables.go:665），服务端只用两个：
//
//	f3 STR  场景名（handlers_game.go:1145 用第一个非空 str 更新当前水域）
//	f5 U32  钓组动画类型位掩码（0x200 静止 / 0x800 入水 / 0x600 …）
//
// 其余字段补零。和 14/2 一样，字段数必须凑满 15 个才能编码。
func liveWaterBody(anim uint32, loc string) *proto.Value {
	vals := map[int]*proto.Value{5: proto.U32(anim)}
	if loc != "" {
		vals[3] = proto.StrVal(loc)
	}
	return proto.Args524(wiregen.MustMakeObj("lobemkloehp", vals))
}

func u64p(v uint64) *uint64 { return &v }

func encodeLive(t *testing.T, v *proto.Value) []byte {
	t.Helper()
	b, err := proto.EncodeValue(v, wiregen.Table())
	if err != nil {
		t.Fatalf("编码: %v", err)
	}
	return b
}

// draw14 从 14/14 推送里提取鱼信息。
//
// 报文形状：args("524")[guid(钓组), obj27 fgogccpglhb]。
// 27 的字段对位（fishing.go:2736 buildFish27）：
//
//	f0 GUID  鱼实例 GUID（后续 14/11 中钩、14/5 入护都用它）
//	f1 STR   鱼种 key（如 "c.roach"）
//	f2 U32   档位 grade（1 未达标 / 2 达标 / 3 上星 / 4 蓝冠）
//	f3 FLOAT 体长 m
//	f4 U32   重量 g
//	f5 FLOAT 体力池  f6 FLOAT 初始体力  f8 FLOAT 拉力
type liveFish struct {
	GUID    string  `json:"guid"`
	Species string  `json:"species"`
	WeightG int64   `json:"weight_g"`
	LengthM float64 `json:"length_m"`
	Grade   int64   `json:"grade"`
	Quality string  `json:"bite_quality"`
	HoldP   float64 `json:"hook_hold_p"`
	Stamina float64 `json:"stamina"`
	Pull    float64 `json:"pull_n"`
	Raw     string  `json:"raw_fields"`
}

// fieldF32 取 FLOAT 字段（wire 上以 f32 位型装在 Num 里）。
func fieldF32(f proto.Field) float64 {
	if f.Op != "FLOAT" {
		return 0
	}
	return float64(f.Float)
}

func parseFish14(t *testing.T, env *proto.Envelope) *liveFish {
	t.Helper()
	dec, _, err := proto.DecodeValue(env.Body, 0, wiregen.Table())
	if err != nil {
		t.Logf("14/14 解码失败: %v", err)
		return nil
	}
	if dec.ArgCount() < 2 {
		t.Logf("14/14 args=%d", dec.ArgCount())
		return nil
	}
	obj := dec.ArgItem(1)
	if obj == nil {
		return nil
	}
	f := &liveFish{}
	if obj.FieldCount() > 0 {
		f.GUID = obj.Field(0).Hex
	}
	if obj.FieldCount() > 1 {
		if s := obj.Field(1); s.Op == "STR" && s.Str != nil {
			f.Species = *s.Str
		}
	}
	if obj.FieldCount() > 2 {
		if n := obj.Field(2).Num; n != nil {
			f.Grade = int64(*n)
		}
	}
	if obj.FieldCount() > 3 {
		f.LengthM = fieldF32(obj.Field(3))
	}
	if obj.FieldCount() > 4 {
		if n := obj.Field(4).Num; n != nil {
			f.WeightG = int64(*n)
		}
	}
	if obj.FieldCount() > 5 {
		f.Stamina = fieldF32(obj.Field(5))
	}
	if obj.FieldCount() > 8 {
		f.Pull = fieldF32(obj.Field(8))
	}
	for i := 0; i < obj.FieldCount() && i < 10; i++ {
		fd := obj.Field(i)
		f.Raw += fmt.Sprintf("f%d=%s/%s;", i, fd.Op, shortLive(fd))
	}
	return f
}

func shortLive(f proto.Field) string {
	switch f.Op {
	case "STR":
		if f.Str != nil {
			return *f.Str
		}
	case "U32", "U64":
		if f.Num != nil {
			return fmt.Sprint(*f.Num)
		}
	case "FLOAT", "DOUBLE":
		return fmt.Sprintf("%.4f", f.Float)
	}
	if f.Hex != "" {
		return f.Hex
	}
	return f.Op
}

// ---------------------------------------------------------------------------
// 主测试：一次完整作钓，记录全过程
// ---------------------------------------------------------------------------

type liveRun struct {
	Rod        string    `json:"rod"`
	RodItem    string    `json:"rod_item"`
	RigType    int64     `json:"rig_type"`
	Loc        string    `json:"loc"`
	Bait       string    `json:"bait"`
	CastOK     bool      `json:"cast_ok"`
	WaterOK    bool      `json:"water_ok"`
	BiteWaitS  float64   `json:"bite_wait_s"`
	GotBite    bool      `json:"got_bite"`
	Fish       *liveFish `json:"fish"`
	Hooked     bool      `json:"hooked"`
	SettleMs   float64   `json:"settle_ms"`
	SettleBody string    `json:"settle_body_head"`
	Kept       bool      `json:"kept"`
	Notes      []string  `json:"notes"`
}

func TestLivePlaySingleCast(t *testing.T) {
	liveplayEnabled(t)
	rod := os.Getenv("RF4_ROD")
	loc := os.Getenv("RF4_LOC")
	name := os.Getenv("RF4_LOGIN")
	if rod == "" || loc == "" {
		t.Skip("需要 RF4_ROD 与 RF4_LOC")
	}
	token, uid := liveLogin(t, name)
	t.Logf("登录 uid=%d", uid)
	c := newLiveClient(t, token)
	boot := c.waitPush(0, 1, 5*time.Second)
	if boot == nil {
		t.Log("未收到 boot 0/1（继续）")
	}
	c.drainPushes(500 * time.Millisecond)

	run := liveRun{Rod: rod, Loc: loc, Notes: []string{}}

	// 14/1
	c.send(14, 1, encodeLive(t, liveArgs(rod)))
	if r := c.waitResp(6 * time.Second); r == nil {
		run.Notes = append(run.Notes, "14/1 无响应")
	} else {
		run.Notes = append(run.Notes, fmt.Sprintf("14/1 响应 %d 字节", len(r.Body)))
	}
	// 14/2
	c.send(14, 2, encodeLive(t, liveCastBody(rod, loc, 20, 1)))
	if r := c.waitResp(6 * time.Second); r == nil {
		run.Notes = append(run.Notes, "14/2 无响应")
	} else {
		run.CastOK = len(r.Body) > 0 && r.Body[0] == 0x01
		run.Notes = append(run.Notes, fmt.Sprintf("14/2 响应 %d 字节 ok=%v", len(r.Body), run.CastOK))
	}
	// 14/7 入水
	c.send(14, 7, encodeLive(t, liveWaterBody(0x800, loc)))
	c.drainPushes(300 * time.Millisecond)

	// 等咬钩
	start := time.Now()
	env := c.waitPush(14, 14, 70*time.Second)
	run.BiteWaitS = time.Since(start).Seconds()
	if env == nil {
		run.Notes = append(run.Notes, "70s 内未收到 14/14")
		liveWriteJSON(t, "live_single.json", run)
		t.Logf("未咬钩，等待 %.1fs", run.BiteWaitS)
		return
	}
	run.GotBite = true
	run.Fish = parseFish14(t, env)
	if run.Fish != nil {
		t.Logf("14/14 鱼=%+v", run.Fish)
	}

	// 14/11 中钩
	if run.Fish != nil {
		c.send(14, 11, encodeLive(t, liveArgs(rod, run.Fish.GUID)))
		c.drainPushes(500 * time.Millisecond)
		run.Hooked = true
	}

	// 14/4 收竿
	t0 := time.Now()
	c.send(14, 4, encodeLive(t, liveArgs(rod)))
	if r := c.waitResp(8 * time.Second); r != nil {
		run.SettleMs = float64(time.Since(t0).Microseconds()) / 1000
		head := r.Body
		if len(head) > 64 {
			head = head[:64]
		}
		run.SettleBody = fmt.Sprintf("%x", head)
	} else {
		run.Notes = append(run.Notes, "14/4 无响应")
	}
	c.drainPushes(600 * time.Millisecond)

	// 14/5 入护
	if run.Fish != nil {
		c.send(14, 5, encodeLive(t, liveArgs(rod, run.Fish.GUID)))
		if r := c.waitResp(8 * time.Second); r != nil {
			run.Kept = len(r.Body) > 0
		}
	}
	liveWriteJSON(t, "live_single.json", run)
}

func liveWriteJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(liveOutputDir, 0o755)
	p := filepath.Join(liveOutputDir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("写出 %s", p)
}