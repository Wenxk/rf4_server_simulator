package gameserver

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"rf4server/internal/logging"
	"rf4server/internal/proto"
)

// lineBreakBodyWithLineOut 按 17/9 的真实请求布局造 body：
// 放线米数是以 little-endian float32 存在偏移 0x3f 的。
func lineBreakBodyWithLineOut(meters float32) []byte {
	body := make([]byte, 0x43)
	binary.LittleEndian.PutUint32(body[0x3f:], math.Float32bits(meters))
	return body
}

// mountMonoFifty 造 tele 竿 + mono 主线并挂到位，把主线长度固定成 50 米。
func mountMonoFifty(t *testing.T, srv *Server) (*Session, string, string) {
	t.Helper()
	ctx := context.Background()
	const uid = int64(1000001)

	rod, err := srv.CreateItem(ctx, uid, "tele_10180_4", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	line, err := srv.CreateItem(ctx, uid, "mono_15095", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(srv)
	mountGearForTest(t, srv, sess, rod.GUID, line.GUID)

	lineRow, _ := srv.DB.GetItemByGUID(ctx, line.GUID)
	fields, _ := FieldsFromJSON(lineRow.FieldsJSON)
	if !setItemLengthField(lineRow, fields, 50) {
		t.Fatal("设置主线长度失败")
	}
	fj, _ := FieldsToJSON(fields)
	if err := srv.DB.UpdateItemFields(ctx, line.GUID, fj, lineRow.Qty, lineRow.Durability,
		lineRow.Workmanship, lineRow.RigGUID); err != nil {
		t.Fatal(err)
	}
	return sess, rod.GUID, line.GUID
}

// rodBreakLineOutM 的解析边界：过短、0、负数、超上限都视为未上报。
func TestRodBreakLineOutM(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body []byte
		want float64
	}{
		{"nil body", nil, 0},
		{"body shorter than offset", make([]byte, 0x3f), 0},
		{"body one byte short", make([]byte, 0x42), 0},
		{"zero meters", lineBreakBodyWithLineOut(0), 0},
		{"negative meters", lineBreakBodyWithLineOut(-3), 0},
		{"over cap", lineBreakBodyWithLineOut(6000), 0},
		{"NaN", lineBreakBodyWithLineOut(float32(math.NaN())), 0},
		{"minimal valid", lineBreakBodyWithLineOut(0.5), 0.5},
		{"normal", lineBreakBodyWithLineOut(12.5), 12.5},
		{"at cap", lineBreakBodyWithLineOut(5000), 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rodBreakLineOutM(c.body); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

// rodBreakForces 读 17/9 报文体 +0x2f（鱼拉力）与 +0x33（断件上限）两个 f32。
//
// 这里钉的是**实测帧对齐**出来的偏移，不是 dboabijkbif 的结构体偏移。
// 下面 6 帧是 2026-11-25 玩家连续实测的原始 hex（原样抄自服务器日志），
// 断件 item_id 与其 kngaaefkpka 一并记在注释里，方便日后复核。
// 若有人再动偏移，这个用例必须一起改，并回到真实帧重新对齐。
func TestRodBreakForcesMatchesMeasuredFrames(t *testing.T) {
	t.Parallel()
	// 帧里 +0x33 必须等于断件的 item_def.kngaaefkpka（静态库最大拉力）。
	cases := []struct {
		name   string
		itemID string
		limit  float64
		hex    string
	}{
		{
			name:   "引线 leader_crafted_10（事件1 00:27:50）",
			itemID: "leader_crafted_10",
			limit:  0.70,
			hex:    "030335323403000cdd2a46570039cf63751b0f11891c42950c46bbe260c360ff81c7d875927097d2d90117000000013a67593f3333333f0000000000000000abc5a040000000803f00000000",
		},
		{
			name:   "主线 mono_15390（事件2 00:31:02）",
			itemID: "mono_15390",
			limit:  2.20,
			hex:    "030335323403000ce70db843aa5f4a231c33c83e334349b50c30fec660f97039d86590768ae02f65fb011700000001498b9640cdcc0c400000000000000000fcfa6e40000000803f00000000",
		},
		{
			name:   "鱼竿 tele_10012_tl13（事件3 00:35:50）",
			itemID: "tele_10012_tl13",
			limit:  4.0881,
			hex:    "030335323403000ce70db843aa5f4a231c33c83e334349b50ce70db843aa5f4a231c33c83e334349b501170000000152feeb40fcd182400000000000000000fcfa6e40000000803f00000000",
		},
		{
			name:   "鱼钩 clas_20043（事件4 00:38:43）",
			itemID: "clas_20043",
			limit:  6.30,
			hex:    "030335323403000c91e9ce7b1267ea7abbbc41b936bd6a280c707bdb04a9bff26b2bddf51a370bcb24011700000001fea9d9409999c9400000000000000000455ffa40000000803f00000000",
		},
		{
			name:   "主线 mono_15061（事件5 00:49:35）",
			itemID: "mono_15061",
			limit:  0.70,
			hex:    "030335323403000c91e9ce7b1267ea7abbbc41b936bd6a280ccf7a6784061ee6a16e499b397bfc8942011700000001b9e558403333333f00000000000000007892fd40000000803f00000000",
		},
		{
			name:   "主线 mono_15390（事件6 01:00:58）",
			itemID: "mono_15390",
			limit:  2.20,
			hex:    "030335323403000cdd2a46570039cf63751b0f11891c42950c30fec660f97039d86590768ae02f65fb011700000001529c3e40cdcc0c40000000000000000078929d40000000803f00000000",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, err := hex.DecodeString(c.hex)
			if err != nil {
				t.Fatalf("解码实测帧: %v", err)
			}
			if len(body) != 76 {
				t.Fatalf("实测帧长度应是 76，实得 %d", len(body))
			}
			F, limit := rodBreakForces(body)
			if math.Abs(limit-c.limit) > 1e-3 {
				t.Fatalf("断件 %s 上限应 %.4f，实得 %.4f", c.itemID, c.limit, limit)
			}
			// 客户端只在 F 超过上限时才上报断件（FishingSet.cs:6017）。
			if F <= limit {
				t.Fatalf("F=%.4f 未超过上限 %.4f，与客户端判定矛盾", F, limit)
			}
			// 出线米数同帧必须可读，三者在同一帧里互相印证。
			if got := rodBreakLineOutM(body); got <= 0 {
				t.Fatalf("同帧出线米数应大于 0，实得 %v", got)
			}
		})
	}
}

// rodBreakForces 的边界：过短、负值、超上限、NaN 一律回 0。
func TestRodBreakForcesBounds(t *testing.T) {
	t.Parallel()
	put := func(fishPull, limit float32) []byte {
		body := make([]byte, 0x37)
		binary.LittleEndian.PutUint32(body[0x2f:], math.Float32bits(fishPull))
		binary.LittleEndian.PutUint32(body[0x33:], math.Float32bits(limit))
		return body
	}
	cases := []struct {
		name         string
		body         []byte
		wantF, wantL float64
	}{
		{"nil body", nil, 0, 0},
		{"shorter than 0x2f+4", make([]byte, 0x32), 0, 0},
		{"only fish pull readable", func() []byte {
			b := make([]byte, 0x33)
			binary.LittleEndian.PutUint32(b[0x2f:], math.Float32bits(3.5))
			return b
		}(), 3.5, 0},
		{"negative fish pull", put(-1, 2), 0, 2},
		{"over cap limit", put(3, 6000), 3, 0},
		{"NaN both", put(float32(math.NaN()), float32(math.NaN())), 0, 0},
		{"normal", put(3.389, 0.7), 3.389000057220459, 0.7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			F, limit := rodBreakForces(c.body)
			if math.Abs(F-c.wantF) > 1e-6 || math.Abs(limit-c.wantL) > 1e-6 {
				t.Fatalf("got (F=%v limit=%v) want (F=%v limit=%v)", F, limit, c.wantF, c.wantL)
			}
		})
	}
}

// 客户端上报 5 米：只扣 5 米，主线物品保留。
func TestMainLineBreakLosesReportedLineOutMeters(t *testing.T) {
	t.Parallel()
	srv, _ := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(lineGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断主线未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 45 {
		t.Fatalf("应只扣上报的 5 米（剩 45），实得剩余 %d 米", got)
	}
}

// 上报米数超过剩余线长：封顶扣光，主线按耗尽删除。
func TestMainLineBreakReportedMetersClampToLineLength(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(lineGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(200),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断主线未处理")
	}
	if row, _ := db.GetItemByGUID(context.Background(), lineGUID); row != nil {
		t.Fatalf("上报 200 米 > 线长 50 米，应封顶扣光并删除主线，实得 %+v", row)
	}
}

// 无客户端上报时退回 tunable：line_break_loss_m=20 → 剩 30 米。
func TestMainLineBreakFallsBackToTunableMeters(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	if err := db.TunableSet(t.Context(), "line_break_loss_m", "20", "test"); err != nil {
		t.Fatal(err)
	}
	// tunableF 有 30s 缓存，写入后必须让缓存失效，否则读到的还是旧值。
	srv.tunMu.Lock()
	srv.tunCache = nil
	srv.tunMu.Unlock()
	if got := srv.tunableF("line_break_loss_m", -1); got != 20 {
		t.Fatalf("tunable 未生效：读回 %v（期望 20）", got)
	}
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(lineGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree,
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断主线未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 30 {
		t.Fatalf("tunable 20 米应扣掉 20（剩 30），实得剩余 %d 米", got)
	}
}

// 上报不足 1 米：四舍五入会变成 0，必须钳到至少扣 1 米，
// 否则一个 0.01 的上报就能把"断线"变成"零损耗"。
func TestMainLineBreakSubMeterReportCostsOneMeter(t *testing.T) {
	t.Parallel()
	srv, _ := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(lineGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(0.3),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断主线未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 49 {
		t.Fatalf("上报 0.3 米应钳到扣 1 米（剩 49），实得剩余 %d 米", got)
	}
}

// ---- 断竿的主线扣减 ----
//
// 断竿不再整卷判死：只按 17/9 上报的实际放线米数扣（与断主线同源），
// 扣剩的线与渔轮一样留在断竿上，可 17/3 拆下换竿；未上报一米不扣。

// 断竿上报 12.5 米：扣 13 米（取整），扣剩 37 米的线仍挂在断竿上。
func TestRodBreakDeductsReportedLineOutKeepsLineMounted(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(12.5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 37 {
		t.Fatalf("断竿应只扣上报的 13 米（剩 37），实得剩余 %d 米", got)
	}
	// 线仍在竿上：f3 引用与 catLine 投影行都保留。
	rodRow := mustItem(t, db, rodGUID)
	rodFields, err := FieldsFromJSON(rodRow.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if idx := fieldIdxByCS(rodRow.WireClass, csLine); idx < 0 || idx >= len(rodFields) || rodFields[idx].GuidHex() != lineGUID {
		t.Fatalf("断竿后竿 f3 应保留主线 %s", lineGUID)
	}
	if got := rigSlotGUIDs(t, srv, rodGUID)[catLine]; got != lineGUID {
		t.Fatalf("断竿后 catLine 投影应保留主线，实得 %q", got)
	}
	// 11028：线不进移除列表，更新段带线树。
	dec, _, err := proto.DecodeValue(body, 0, srv.Table())
	if err != nil {
		t.Fatal(err)
	}
	if f4 := dec.Fields[4].Val; f4 != nil {
		for _, it := range f4.Items {
			if it.Hex == lineGUID {
				t.Fatalf("扣剩的主线不应进 11028 移除列表: %+v", f4.Items)
			}
		}
	}
	hasLineTree := false
	if f3 := dec.Fields[3].Val; f3 != nil {
		for _, it := range f3.Items {
			if containsGUID(deepGUIDs(it), lineGUID) {
				hasLineTree = true
			}
		}
	}
	if !hasLineTree {
		t.Fatal("断竿更新段应包含扣剩主线的物品树")
	}
}

// 断竿上报米数超过剩余线长：封顶扣光，主线按耗尽删除、f3 清空。
func TestRodBreakLineExhaustedWhenReportedOverRemaining(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(200),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if row, _ := db.GetItemByGUID(context.Background(), lineGUID); row != nil {
		t.Fatalf("上报 200 米 > 线长 50 米，应封顶扣光并删除主线，实得 %+v", row)
	}
	rodRow := mustItem(t, db, rodGUID)
	rodFields, err := FieldsFromJSON(rodRow.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if idx := fieldIdxByCS(rodRow.WireClass, csLine); idx >= 0 && idx < len(rodFields) && rodFields[idx].GuidHex() != "" {
		t.Fatal("主线耗尽后竿 f3 应清空")
	}
	if got := rigSlotGUIDs(t, srv, rodGUID)[catLine]; got != "" {
		t.Fatalf("主线耗尽后 catLine 投影应清空，实得 %q", got)
	}
}

// 断竿未上报放线米数：主线一米不扣，整卷保留。
func TestRodBreakKeepsLineFullyWithoutLineOutReport(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree,
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 50 {
		t.Fatalf("未上报放线米数时主线应一米不扣（剩 50），实得剩余 %d 米", got)
	}
	logs, err := db.ListItemDamageLogs(context.Background(), 1000001, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		if entry.Event == damageEventLineBreak || entry.Event == damageEventLineExhausted {
			t.Fatalf("未扣线不应记 line_break/line_exhausted: %+v", entry)
		}
	}
}

// 断竿上报不足 1 米（0.5）：与断主线同源，钳到至少扣 1 米。
func TestRodBreakSubMeterLineOutCostsOneMeter(t *testing.T) {
	t.Parallel()
	srv, _ := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(0.5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 49 {
		t.Fatalf("断竿上报 0.5 米应钳到扣 1 米（剩 49），实得剩余 %d 米", got)
	}
}

// makeLineLengthUnreadable 把已挂载主线改成长度不可读：去掉 csQty 长度字段、
// Qty=0，并抹掉 numeric_id/item_id 让"回退定义全长"也拿不到。
func makeLineLengthUnreadable(t *testing.T, srv *Server, lineGUID string) {
	t.Helper()
	ctx := context.Background()
	row, err := srv.DB.GetItemByGUID(ctx, lineGUID)
	if err != nil || row == nil {
		t.Fatalf("读取主线失败: %v", err)
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	idx := fieldIdxByCS(row.WireClass, csQty)
	if idx < 0 || idx >= len(fields) {
		t.Fatalf("主线没有长度字段，用例前置不成立（idx=%d）", idx)
	}
	fields = append(fields[:idx], fields[idx+1:]...)
	fj, err := FieldsToJSON(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.DB.UpdateItemFields(ctx, lineGUID, fj, 0, row.Durability, row.Workmanship, row.RigGUID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.DB.SQL.ExecContext(ctx,
		`UPDATE item SET numeric_id=0, item_id='' WHERE guid=?`, lineGUID); err != nil {
		t.Fatal(err)
	}
}

// 线长读不出来时按未上报处理：整卷保留，不删线。
// 修复前 beforeLength=0 → afterLength=0 → lineKept=false，即使上报 12.5 米
// 也会把主线误删。
func TestRodBreakKeepsLineWhenLengthUnreadable(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)
	makeLineLengthUnreadable(t, srv, lineGUID)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(12.5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if row, _ := db.GetItemByGUID(context.Background(), lineGUID); row == nil {
		t.Fatal("线长不可读时断竿不应删除主线")
	}
	// 线保留：f3 与 catLine 投影行都在。
	rodRow := mustItem(t, db, rodGUID)
	rodFields, err := FieldsFromJSON(rodRow.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if idx := fieldIdxByCS(rodRow.WireClass, csLine); idx < 0 || idx >= len(rodFields) || rodFields[idx].GuidHex() != lineGUID {
		t.Fatalf("线保留时竿 f3 应仍指向 %s", lineGUID)
	}
	if got := rigSlotGUIDs(t, srv, rodGUID)[catLine]; got != lineGUID {
		t.Fatalf("线保留时 catLine 投影应保留，实得 %q", got)
	}
	logs, err := db.ListItemDamageLogs(context.Background(), 1000001, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		if entry.Event == damageEventLineBreak || entry.Event == damageEventLineExhausted {
			t.Fatalf("没扣成米数不应记 line_break/line_exhausted: %+v", entry)
		}
	}
}

// f3 陈旧引用（线已拆回背包但竿上引用没清）：断竿只清引用，不动背包里的线。
func TestRodBreakStaleLineRefSparesBackpackLine(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	ctx := context.Background()
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	// 模拟陈旧引用：线被拆回背包，但竿 f3 与投影行残留。
	if err := srv.DB.SetItemBackpack(ctx, lineGUID); err != nil {
		t.Fatal(err)
	}
	lineRow, _ := db.GetItemByGUID(ctx, lineGUID)
	fields, _ := FieldsFromJSON(lineRow.FieldsJSON)
	_ = setItemLengthField(lineRow, fields, 50)
	fj, _ := FieldsToJSON(fields)
	if err := srv.DB.UpdateItemFields(ctx, lineGUID, fj, lineRow.Qty, lineRow.Durability,
		lineRow.Workmanship, lineRow.RigGUID); err != nil {
		t.Fatal(err)
	}

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(12.5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	// 背包里的线原封不动：50 米一米没扣，也没被删除。
	staleRow := mustItem(t, db, lineGUID)
	if staleRow.Container != "backpack" {
		t.Fatalf("陈旧引用的线应留在背包，实得 container=%s", staleRow.Container)
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 50 {
		t.Fatalf("陈旧引用的线不应被扣长度（应剩 50），实得 %d", got)
	}
	// 竿侧：f3 清空、投影行清掉。
	rodRow := mustItem(t, db, rodGUID)
	rodFields, err := FieldsFromJSON(rodRow.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if idx := fieldIdxByCS(rodRow.WireClass, csLine); idx >= 0 && idx < len(rodFields) && rodFields[idx].GuidHex() != "" {
		t.Fatal("陈旧引用断竿后竿 f3 应清空")
	}
	if got := rigSlotGUIDs(t, srv, rodGUID)[catLine]; got != "" {
		t.Fatalf("陈旧引用断竿后 catLine 投影应清空，实得 %q", got)
	}
}

// 断竿（耐久 0）后主线扣剩仍可 17/3 拆下换竿：拆卸不受断竿状态拦截，
// 线按剩余长度退回背包，f3 与投影清掉。
func TestRodBreakLineDetachableAfterBreak(t *testing.T) {
	t.Parallel()
	srv, db := newRigTestServer(t)
	sess, rodGUID, lineGUID := mountMonoFifty(t, srv)

	tree := rigRequestTree(t, proto.Guid(rodGUID), proto.Guid(rodGUID), proto.U32(1), proto.Null())
	body, ok := hDynamicRodBreak(&Ctx{
		Sess: sess, Main: 17, Sub: 9, Tree: tree, Body: lineBreakBodyWithLineOut(12.5),
		DecodeOK: true, ExpectResponse: true, Log: logging.Discard(),
	})
	if !ok || len(body) == 0 {
		t.Fatal("17/9 断竿未处理")
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 37 {
		t.Fatalf("断竿应扣 13 米剩 37，实得 %d", got)
	}

	// 17/3 拆线（slot 11 = catLine）：断竿状态不拦截。
	runRodRemove(t, srv, sess, rodGUID, 11, lineGUID, 0)
	lineRow := mustItem(t, db, lineGUID)
	if lineRow.Container != "backpack" || lineRow.RigGUID != "" {
		t.Fatalf("拆下后应退回背包并清父引用，实得 container/rig_guid=%s/%s",
			lineRow.Container, lineRow.RigGUID)
	}
	if got := lineLengthOfTestItem(t, srv, lineGUID); got != 37 {
		t.Fatalf("拆下后线长应保持 37 米，实得 %d", got)
	}
	rodRow := mustItem(t, db, rodGUID)
	rodFields, err := FieldsFromJSON(rodRow.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if idx := fieldIdxByCS(rodRow.WireClass, csLine); idx >= 0 && idx < len(rodFields) && rodFields[idx].GuidHex() != "" {
		t.Fatal("拆下后竿 f3 应清空")
	}
	if got := rigSlotGUIDs(t, srv, rodGUID)[catLine]; got != "" {
		t.Fatalf("拆下后 catLine 投影应清空，实得 %q", got)
	}
}
