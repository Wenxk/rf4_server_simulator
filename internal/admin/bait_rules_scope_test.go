package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

// TestBaitCategoriesScopeForSameNameAcrossCategories 验证分类树会为「跨类别同名」
// 的型号名给出带限定的写法。
//
// 背景（用户实测报告）：给白化鲃鱼加了「颗粒的鱼子」以后，想再加「香煮成饵的鱼子」，
// 界面上加不进去——因为 `鱼子` 这一个名字在 3 个类别里都有，前端按名字去重就判重复。
// 即使绕过界面写进去，`鱼子` 一个词也会命中全部同名款（真实库 14 款），
// 无法只开其中一类。解决方式是写「类别@名字」，后端 splitBaitScope 按类别 + 名字双判。
func TestBaitCategoriesScopeForSameNameAcrossCategories(t *testing.T) {
	db := testDB(t)
	// 夹具：让 `鱼子` 同时出现在 香煮成饵(8) 与 颗粒(9)；
	// 再放一个只属于颗粒的型号 `独特颗粒` 作为反向对照。
	for _, q := range []string{
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900901,'cav_boil','27','{"fopbhlmkcpe":8}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('cav_boil','鱼子10')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900902,'cav_plt','27','{"fopbhlmkcpe":9}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('cav_plt','鱼子 14')`,
		`INSERT INTO item_def(numeric_id,item_id,category,attrs_json) VALUES(900903,'uniq_plt','27','{"fopbhlmkcpe":9}')`,
		`INSERT INTO item_catalog(item_id,name_zh) VALUES('uniq_plt','独特颗粒 6mm')`,
	} {
		if _, err := db.SQL.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	srv := NewServer(config.Admin{JWTSecret: "test"}, db, logging.Discard(), WebFS())
	rec := httptest.NewRecorder()
	srv.handleBaitCategories(rec, httptest.NewRequest("GET", "/api/bait/categories", nil))
	if rec.Code != 200 {
		t.Fatalf("状态=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Categories []struct {
			Key      int64 `json:"key"`
			Children []struct {
				Sub    int64  `json:"sub"`
				Label  string `json:"label"`
				Term   string `json:"term"`
				Brands []struct {
					Lines []struct {
						Base       string   `json:"base"`
						Count      int      `json:"count"`
						Term       string   `json:"term"`
						ScopeTerm  string   `json:"scope_term"`
						SharedWith []string `json:"shared_with"`
					} `json:"lines"`
				} `json:"brands"`
			} `json:"children"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	type row struct {
		subLabel, term, scopeTerm string
		sharedWith                []string
	}
	found := map[string]row{}
	for _, cat := range body.Categories {
		for _, ch := range cat.Children {
			for _, bd := range ch.Brands {
				for _, ln := range bd.Lines {
					if ln.Base == "鱼子" || ln.Base == "独特颗粒 6mm" {
						// 用二级类别规则词（ch.Term）当键：分类树的 label 带形态说明
						// （「香煮成饵（下沉 + Pop-up）」），不适合做断言键。
						found[ch.Term+"|"+ln.Base] = row{ch.Label, ln.Term, ln.ScopeTerm, ln.SharedWith}
					}
				}
			}
		}
	}

	// 1) 跨类别的「鱼子」：两个类别各有一条，都必须带限定写法
	for _, label := range []string{"香煮成饵", "颗粒"} {
		r, ok := found[label+"|鱼子"]
		if !ok {
			t.Fatalf("没找到 %s 下的「鱼子」，实际找到：%v", label, keysOf(found))
		}
		if r.term != "鱼子" {
			t.Fatalf("%s 下 term = %q，期望保留原名", label, r.term)
		}
		want := label + "@鱼子"
		if r.scopeTerm != want {
			t.Fatalf("%s 下 scope_term = %q，期望 %q（跨类别同名必须给限定写法）", label, r.scopeTerm, want)
		}
		// shared_with 必须列出对方、且不含自己
		if len(r.sharedWith) != 1 {
			t.Fatalf("%s 下 shared_with = %v，期望恰好 1 个（另一个同名类别）", label, r.sharedWith)
		}
		if r.sharedWith[0] == label {
			t.Fatalf("%s 下 shared_with 含自己：%v", label, r.sharedWith)
		}
	}

	// 2) 反向：只属于一个类别的型号不该带限定，否则规则表全变成「类别@名」长串。
	// 注意 base 用的是 ExportedBaitBaseName（只剥尾部数字），`独特颗粒 6mm` 以字母
	// 结尾不会被剥，所以键是完整名。
	if r, ok := found["颗粒|独特颗粒 6mm"]; ok {
		if r.scopeTerm != "" {
			t.Fatalf("「独特颗粒 6mm」只在颗粒一个类别里，不该有 scope_term，实际 %q", r.scopeTerm)
		}
		if len(r.sharedWith) != 0 {
			t.Fatalf("「独特颗粒 6mm」不该有 shared_with，实际 %v", r.sharedWith)
		}
	} else {
		t.Fatalf("没找到「独特颗粒 6mm」这条反向对照：%v", keysOf(found))
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
