// 代码由 tmp_analysis 流水线生成（gen_tables.py），数据=官服抓包 2026-11-10~17 实测值。

package gameserver

// 官服实测 XP 速率表。公式：XP = trunc(c(鱼种,档位) × W_g)（trunc=截断取整，与官服一致）。
// 实测优先原则：凡有实测的种（含单点）全用实测值，缺档用「本种锚 × 档位乘子」；
// 亲缘组（复用拉力 K 表 fishPullSpeciesGroup）仅兜零实测种。
// 推导与样本口径见 specs/changes/fish-xp-rate-table/spec.md。抓包原始数据不入库（CONTRIBUTING 禁令），此处仅数值。

// fishXPRateFallback 全局缺省速率（现行值，实测/组均未命中时兜底，理论上不可达）。
const fishXPRateFallback = 0.4

// fishXPRateC 官服实测 (鱼种,档位)→c。注释 = 样本数/重量范围/来源(310=结算,320=记录卡)/会话。
// [加成嫌疑] = 同组样本 XP/W 不自洽（官服疑有活动加成），c 取低值方向（去加成）。
var fishXPRateC = map[vgKey]float64{
	{"a.sleeper", 2}:         1.125,     // n=1 W=396~396g src=320
	{"a_char", 1}:            0.5,       // n=1 W=415~415g src=310
	{"a_char", 2}:            1.15,      // n=1 W=8916~8916g src=320
	{"a_grayling", 2}:        2.8565,    // n=1 W=1397~1397g src=320
	{"a_omul", 1}:            1.5989,    // n=1 W=283~283g src=320
	{"ambl_r", 2}:            11.1107,   // n=1 W=4567~4567g src=320
	{"asp", 1}:               0.8429,    // n=1 W=1614~1614g src=320
	{"atl_cod", 1}:           0.524495,  // n=2 W=2674~5677g src=310 [加成嫌疑→低值]
	{"atl_cod", 2}:           3.149971,  // n=1 W=30676~30676g src=320
	{"atl_herring", 2}:       32.499107, // n=2 W=560~637g src=310,320 [加成嫌疑→低值]
	{"b_bream", 2}:           10.0013,   // n=1 W=377~377g src=320
	{"b_carp", 2}:            2.08,      // n=1 W=7828~7828g src=310
	{"b_carp", 3}:            12.999953, // n=2 W=31932~31932g src=320
	{"b_gill", 1}:            0.1814,    // n=1 W=113~113g src=310
	{"b_gill", 3}:            1.3633,    // n=2 W=1383~1383g src=320
	{"barsch", 2}:            3.1996,    // n=1 W=714~714g src=320
	{"bh_carp", 3}:           4.3333,    // n=4 W=37894~37894g src=320
	{"bl_buffalo", 2}:        10.3995,   // n=1 W=3235~3235g src=320
	{"black_crappie", 2}:     3.9948,    // n=1 W=668~668g src=320
	{"bream", 3}:             1.4999,    // n=2 W=4414~4414g src=320
	{"buffalo", 2}:           0.936,     // n=1 W=14669~14669g src=320
	{"burbot", 2}:            1.3,       // n=1 W=4571~4571g src=320
	{"c.carp", 2}:            0.1858,    // n=1 W=1574~1574g src=310
	{"c.carp", 3}:            0.5714,    // n=3 W=2171~2439g src=320
	{"c.roach", 2}:           0.3007,    // n=1 W=680~680g src=310
	{"c.roach", 3}:           2.9988,    // n=2 W=1290~1290g src=320
	{"c_barbel", 1}:          0.5335,    // n=1 W=1478~1478g src=320
	{"c_bleak", 2}:           11.245,    // n=1 W=100~100g src=320
	{"c_nase", 2}:            4.6875,    // n=1 W=632~632g src=320
	{"c_squid", 2}:           75.0007,   // n=1 W=672~672g src=320
	{"carp", 1}:              0.1625,    // n=1 W=2434~2434g src=310
	{"carp", 2}:              0.52,      // n=5 W=4137~5710g src=310
	{"carp", 3}:              3.249971,  // n=2 W=25665~25665g src=320
	{"carp_ghost", 2}:        3.574895,  // n=2 W=4513~12632g src=310,320 [加成嫌疑→低值]
	{"ch_catfish", 2}:        0.4229,    // n=1 W=1997~1997g src=320
	{"cha_herring", 2}:       10.9065,   // n=1 W=230~230g src=320
	{"chartus_kuoriius", 2}:  2.25,      // n=1 W=6490~6490g src=320
	{"chir", 2}:              1.2998,    // n=1 W=1256~1256g src=320
	{"chlamys_isl", 2}:       83.3221,   // n=1 W=104~104g src=320
	{"coalfish", 1}:          0.22933,   // n=2 W=702~1692g src=310
	{"coalfish", 2}:          0.468285,  // n=2 W=3027~17823g src=310,320 [加成嫌疑→低值]
	{"common_carp", 3}:       6.239969,  // n=2 W=25197~25197g src=320
	{"conger", 1}:            2.5,       // n=1 W=3681~3681g src=320
	{"crawfish", 2}:          10.0027,   // n=1 W=186~186g src=320
	{"crucian", 2}:           0.137571,  // n=2 W=527~743g src=310 [加成嫌疑→低值]
	{"crucian", 3}:           0.5501,    // n=3 W=2299~2686g src=320
	{"dace", 3}:              13.8887,   // n=2 W=265~265g src=320
	{"din_l_carp", 1}:        0.5834,    // n=1 W=1715~1715g src=310
	{"din_l_carp", 2}:        2.999981,  // n=1 W=26479~26479g src=320
	{"din_m_carp", 2}:        1.3333,    // n=1 W=29005~29005g src=320
	{"din_n_carp", 2}:        6.1667,    // n=1 W=18864~18864g src=320
	{"dreissena", 2}:         5.6026,    // n=1 W=117~117g src=320
	{"e.chub", 2}:            0.6997,    // n=1 W=3482~3482g src=320
	{"e.pikeperch", 2}:       1.625,     // n=1 W=2783~2783g src=320
	{"e_bream", 2}:           1.3001,    // n=1 W=1088~1088g src=320
	{"e_smelt", 2}:           20.0066,   // n=1 W=76~76g src=320
	{"ed_crab", 3}:           99.9988,   // n=2 W=2123~2123g src=320
	{"fhcatfish", 2}:         1.199933,  // n=3 W=22275~22275g src=320
	{"freshwater_mussel", 2}: 2.0037,    // n=1 W=135~135g src=320
	{"fwdrum", 2}:            1.3998,    // n=1 W=3215~3215g src=320
	{"g_carp", 2}:            0.624,     // n=5 W=4880~21742g src=310
	{"g_carp", 3}:            3.639973,  // n=2 W=30513~30513g src=320
	{"g_shad", 2}:            0.7202,    // n=1 W=831~831g src=320
	{"g_tench", 2}:           19.067,    // n=1 W=992~992g src=320
	{"gc_smelt", 1}:          0.1561,    // n=1 W=772~772g src=310
	{"gc_smelt", 2}:          0.333308,  // n=2 W=2225~6605g src=320 [加成嫌疑→低值]
	{"gorbusha", 2}:          13.0911,   // n=1 W=1487~1487g src=320
	{"gudgeon", 3}:           29.988,    // n=3 W=102~140g src=320
	{"halibut_w", 1}:         0.5468,    // n=1 W=4110~4110g src=320
	{"hi_utsri", 2}:          20,        // n=1 W=11017~11017g src=320
	{"hippoglos_p", 2}:       29.0752,   // n=1 W=2639~2639g src=320
	{"hs_bass", 2}:           3.0765,    // n=1 W=1890~1890g src=320
	{"karasekarp", 3}:        11.9996,   // n=2 W=3944~3944g src=320
	{"katran", 2}:            20.2496,   // n=1 W=3973~3973g src=320
	{"kohaku", 2}:            3.75,      // n=1 W=14135~14135g src=320
	{"kw_tench", 3}:          53.3334,   // n=2 W=4392~4392g src=320
	{"l_carp", 2}:            1.8199,    // n=1 W=18300~18300g src=320
	{"l_carp_ghost", 2}:      18.7196,   // n=1 W=7916~7916g src=320
	{"l_minnow", 2}:          2.5,       // n=1 W=43~43g src=320
	{"lamna_n", 2}:           4.1304,    // n=1 W=33167~33167g src=320
	{"lenok_o", 2}:           6.9996,    // n=1 W=1403~1403g src=320
	{"lh_kambala", 2}:        9.9991,    // n=1 W=2904~2904g src=320
	{"lm_b_bass", 2}:         3.1997,    // n=1 W=2456~2456g src=320
	{"m_carp", 1}:            0.1561,    // n=1 W=3494~3494g src=310
	{"m_carp", 2}:            0.5201,    // n=1 W=4756~4756g src=310
	{"m_carp", 3}:            2.399956,  // n=2 W=25189~25189g src=320
	{"m_carp_ghost", 2}:      6.2399,    // n=1 W=13290~13290g src=320
	{"m_goshiki", 2}:         16.0001,   // n=1 W=8694~8694g src=320
	{"m_molva", 2}:           6.4996,    // n=1 W=5003~5003g src=320
	{"m_scorpius", 1}:        0.446341,  // n=4 W=205~585g src=310 [加成嫌疑→低值]
	{"m_scorpius", 2}:        1.1992,    // n=1 W=1142~1142g src=310
	{"m_scorpius", 3}:        7.5,       // n=2 W=3403~3403g src=320
	{"m_zubatka", 2}:         12.8571,   // n=1 W=12416~12416g src=320
	{"makrel", 2}:            3.0003,    // n=1 W=1891~1891g src=310
	{"makrel", 3}:            32.1425,   // n=2 W=2059~2059g src=320
	{"meagre", 2}:            12.272645, // n=3 W=34930~34930g src=320
	{"menek", 2}:             0.599886,  // n=2 W=6117~13652g src=310,320 [加成嫌疑→低值]
	{"merlang", 1}:           6.8988,    // n=1 W=252~252g src=310
	{"merlang", 2}:           23.0002,   // n=1 W=2130~2130g src=320
	{"merluza", 2}:           2.8,       // n=1 W=8367~8367g src=320
	{"molva", 2}:             6.1109,    // n=1 W=3763~3763g src=320
	{"mytilus_ed", 2}:        80.0075,   // n=1 W=67~67g src=320
	{"n.pike", 2}:            0.333217,  // n=2 W=4305~11164g src=310,320 [加成嫌疑→低值]
	{"n_asagi", 2}:           9.5,       // n=1 W=11599~11599g src=320
	{"n_carp", 1}:            0.2501,    // n=1 W=4405~4405g src=310
	{"n_carp", 2}:            1.666643,  // n=1 W=21513~21513g src=320
	{"o_ogon", 2}:            2.75,      // n=1 W=20322~20322g src=320
	{"os_grayling", 2}:       5.0658,    // n=1 W=691~691g src=320
	{"osetr_ship", 1}:        1.625,     // n=1 W=8419~8419g src=320
	{"ostrea_ed", 1}:         179.9118,  // n=1 W=51~51g src=320
	{"p_sturgeon", 2}:        3.119948,  // n=1 W=16553~16553g src=320
	{"p_zubatka", 2}:         6.7499,    // n=1 W=13459~13459g src=320
	{"peled", 2}:             1.9991,    // n=1 W=1637~1637g src=320
	{"perch", 1}:             0.125,     // n=4 W=100~340g src=310 [加成嫌疑→低值]
	{"perch", 3}:             1.7144,    // n=2 W=1665~1665g src=320
	{"piksha", 2}:            0.8382,    // n=1 W=2975~2975g src=310
	{"piksha", 3}:            10.0588,   // n=2 W=11714~11714g src=320
	{"pinagor", 2}:           13.1575,   // n=1 W=2441~2441g src=320
	{"pollachius", 2}:        2.916422,  // n=2 W=2387~10506g src=310,320 [加成嫌疑→低值]
	{"polyprion", 2}:         3,         // n=1 W=23470~23470g src=320
	{"poutassou", 2}:         10.5,      // n=1 W=591~591g src=320
	{"pumps_sunfish", 2}:     14.994,    // n=1 W=252~252g src=320
	{"r_carp", 2}:            1.456,     // n=1 W=21793~21793g src=320
	{"r_sturgeon", 2}:        1.307663,  // n=1 W=17043~17043g src=320
	{"red_fish", 3}:          17.5,      // n=2 W=3207~3207g src=320
	{"resfish", 2}:           1.8616,    // n=1 W=1348~1348g src=320
	{"ripus", 3}:             6.25,      // n=2 W=1242~1242g src=320
	{"riv_brown_trout", 2}:   3.214,     // n=1 W=1152~1152g src=320
	{"riv_rb_trout", 2}:      2.5,       // n=1 W=6697~6697g src=320
	{"rudd", 2}:              1.248,     // n=1 W=252~252g src=320
	{"ruffe", 3}:             11.996552, // n=4 W=145~168g src=320 [加成嫌疑→低值]
	{"ruffe_n", 2}:           44.4398,   // n=1 W=133~133g src=320
	{"s.bream", 3}:           3.214,     // n=2 W=1077~1077g src=320
	{"s.orfe", 2}:            0.8002,    // n=1 W=1739~1739g src=320
	{"s_carp", 3}:            6.76,      // n=2 W=31632~31632g src=320
	{"s_freaks", 3}:          3.999984,  // n=3 W=31717~31717g src=320
	{"s_saurus", 3}:          170.9928,  // n=2 W=627~627g src=320
	{"sabrefish", 2}:         0.8994,    // n=1 W=721~721g src=320
	{"sardina_p", 2}:         134.9637,  // n=1 W=124~124g src=320
	{"sea_kambala", 3}:       21.4282,   // n=2 W=4548~4548g src=320
	{"sebastes_m", 2}:        8.888,     // n=1 W=1344~1344g src=320
	{"sevan_trout", 2}:       1.9443,    // n=1 W=2342~2342g src=320
	{"sh_barbel", 2}:         6.8095,    // n=1 W=9752~9752g src=320
	{"shemaya_cha", 2}:       10.935897, // n=3 W=117~419g src=310,320 [加成嫌疑→低值]
	{"sig", 1}:               9.3709,    // n=1 W=368~368g src=320
	{"sig_ludoga", 2}:        3.3337,    // n=1 W=1461~1461g src=320
	{"sig_p", 2}:             1.8854,    // n=1 W=1086~1086g src=320
	{"sig_vouksa", 2}:        6.25,      // n=1 W=1194~1194g src=320
	{"sm_bass", 2}:           2.5449,    // n=1 W=2049~2049g src=320
	{"sp_bass", 2}:           3.8997,    // n=1 W=1391~1391g src=320
	{"st_sturgeon", 2}:       1.375,     // n=1 W=11855~11855g src=320
	{"sterlet", 2}:           12.9998,   // n=1 W=8346~8346g src=320
	{"syb_vendace", 2}:       3.0791,    // n=1 W=158~158g src=320
	{"taimen", 1}:            0.9068,    // n=1 W=574~574g src=320
	{"taran", 2}:             2.3748,    // n=1 W=679~679g src=320
	{"tench", 4}:             2.6667,    // n=2 W=6782~6782g src=320
	{"tiulka", 1}:            60.0417,   // n=1 W=12~12g src=320
	{"turbot", 2}:            4.8998,    // n=1 W=11003~11003g src=320
	{"valyok", 1}:            6.9975,    // n=1 W=197~197g src=320
	{"vendace", 2}:           3.9969,    // n=1 W=488~488g src=320
	{"wa_bream", 2}:          3.2702,    // n=1 W=531~531g src=320
	{"walleye", 2}:           4.1738,    // n=1 W=4053~4053g src=320
	{"warm", 2}:              1.0351,    // n=1 W=584~584g src=320
	{"wh_bass", 2}:           0.7258,    // n=1 W=1592~1592g src=320
	{"white_crappie", 2}:     1.5476,    // n=1 W=1239~1239g src=320
	{"yotsushiro", 2}:        12.5001,   // n=1 W=5352~5352g src=320
	{"zoarces_v", 3}:         146.6671,  // n=2 W=1065~1065g src=320
	{"zs_grayling", 2}:       5.0657,    // n=1 W=830~830g src=320
}

// fishXPAnchor 同种锚：该种实测样本最多的档。缺档 c = 锚 c × FACTOR[g] / FACTOR[锚档]。
var fishXPAnchor = map[string]fishXPAnchorEntry{
	"a.sleeper":         {grade: 2, c: 1.125},
	"a_char":            {grade: 1, c: 0.5},
	"a_grayling":        {grade: 2, c: 2.8565},
	"a_omul":            {grade: 1, c: 1.5989},
	"ambl_r":            {grade: 2, c: 11.1107},
	"asp":               {grade: 1, c: 0.8429},
	"atl_cod":           {grade: 1, c: 0.524495},
	"atl_herring":       {grade: 2, c: 32.499107},
	"b_bream":           {grade: 2, c: 10.0013},
	"b_carp":            {grade: 3, c: 12.999953},
	"b_gill":            {grade: 3, c: 1.3633},
	"barsch":            {grade: 2, c: 3.1996},
	"bh_carp":           {grade: 3, c: 4.3333},
	"bl_buffalo":        {grade: 2, c: 10.3995},
	"black_crappie":     {grade: 2, c: 3.9948},
	"bream":             {grade: 3, c: 1.4999},
	"buffalo":           {grade: 2, c: 0.936},
	"burbot":            {grade: 2, c: 1.3},
	"c.carp":            {grade: 3, c: 0.5714},
	"c.roach":           {grade: 3, c: 2.9988},
	"c_barbel":          {grade: 1, c: 0.5335},
	"c_bleak":           {grade: 2, c: 11.245},
	"c_nase":            {grade: 2, c: 4.6875},
	"c_squid":           {grade: 2, c: 75.0007},
	"carp":              {grade: 2, c: 0.52},
	"carp_ghost":        {grade: 2, c: 3.574895},
	"ch_catfish":        {grade: 2, c: 0.4229},
	"cha_herring":       {grade: 2, c: 10.9065},
	"chartus_kuoriius":  {grade: 2, c: 2.25},
	"chir":              {grade: 2, c: 1.2998},
	"chlamys_isl":       {grade: 2, c: 83.3221},
	"coalfish":          {grade: 1, c: 0.22933},
	"common_carp":       {grade: 3, c: 6.239969},
	"conger":            {grade: 1, c: 2.5},
	"crawfish":          {grade: 2, c: 10.0027},
	"crucian":           {grade: 3, c: 0.5501},
	"dace":              {grade: 3, c: 13.8887},
	"din_l_carp":        {grade: 1, c: 0.5834},
	"din_m_carp":        {grade: 2, c: 1.3333},
	"din_n_carp":        {grade: 2, c: 6.1667},
	"dreissena":         {grade: 2, c: 5.6026},
	"e.chub":            {grade: 2, c: 0.6997},
	"e.pikeperch":       {grade: 2, c: 1.625},
	"e_bream":           {grade: 2, c: 1.3001},
	"e_smelt":           {grade: 2, c: 20.0066},
	"ed_crab":           {grade: 3, c: 99.9988},
	"fhcatfish":         {grade: 2, c: 1.199933},
	"freshwater_mussel": {grade: 2, c: 2.0037},
	"fwdrum":            {grade: 2, c: 1.3998},
	"g_carp":            {grade: 2, c: 0.624},
	"g_shad":            {grade: 2, c: 0.7202},
	"g_tench":           {grade: 2, c: 19.067},
	"gc_smelt":          {grade: 2, c: 0.333308},
	"gorbusha":          {grade: 2, c: 13.0911},
	"gudgeon":           {grade: 3, c: 29.988},
	"halibut_w":         {grade: 1, c: 0.5468},
	"hi_utsri":          {grade: 2, c: 20},
	"hippoglos_p":       {grade: 2, c: 29.0752},
	"hs_bass":           {grade: 2, c: 3.0765},
	"karasekarp":        {grade: 3, c: 11.9996},
	"katran":            {grade: 2, c: 20.2496},
	"kohaku":            {grade: 2, c: 3.75},
	"kw_tench":          {grade: 3, c: 53.3334},
	"l_carp":            {grade: 2, c: 1.8199},
	"l_carp_ghost":      {grade: 2, c: 18.7196},
	"l_minnow":          {grade: 2, c: 2.5},
	"lamna_n":           {grade: 2, c: 4.1304},
	"lenok_o":           {grade: 2, c: 6.9996},
	"lh_kambala":        {grade: 2, c: 9.9991},
	"lm_b_bass":         {grade: 2, c: 3.1997},
	"m_carp":            {grade: 3, c: 2.399956},
	"m_carp_ghost":      {grade: 2, c: 6.2399},
	"m_goshiki":         {grade: 2, c: 16.0001},
	"m_molva":           {grade: 2, c: 6.4996},
	"m_scorpius":        {grade: 1, c: 0.446341},
	"m_zubatka":         {grade: 2, c: 12.8571},
	"makrel":            {grade: 3, c: 32.1425},
	"meagre":            {grade: 2, c: 12.272645},
	"menek":             {grade: 2, c: 0.599886},
	"merlang":           {grade: 1, c: 6.8988},
	"merluza":           {grade: 2, c: 2.8},
	"molva":             {grade: 2, c: 6.1109},
	"mytilus_ed":        {grade: 2, c: 80.0075},
	"n.pike":            {grade: 2, c: 0.333217},
	"n_asagi":           {grade: 2, c: 9.5},
	"n_carp":            {grade: 1, c: 0.2501},
	"o_ogon":            {grade: 2, c: 2.75},
	"os_grayling":       {grade: 2, c: 5.0658},
	"osetr_ship":        {grade: 1, c: 1.625},
	"ostrea_ed":         {grade: 1, c: 179.9118},
	"p_sturgeon":        {grade: 2, c: 3.119948},
	"p_zubatka":         {grade: 2, c: 6.7499},
	"peled":             {grade: 2, c: 1.9991},
	"perch":             {grade: 1, c: 0.125},
	"piksha":            {grade: 3, c: 10.0588},
	"pinagor":           {grade: 2, c: 13.1575},
	"pollachius":        {grade: 2, c: 2.916422},
	"polyprion":         {grade: 2, c: 3},
	"poutassou":         {grade: 2, c: 10.5},
	"pumps_sunfish":     {grade: 2, c: 14.994},
	"r_carp":            {grade: 2, c: 1.456},
	"r_sturgeon":        {grade: 2, c: 1.307663},
	"red_fish":          {grade: 3, c: 17.5},
	"resfish":           {grade: 2, c: 1.8616},
	"ripus":             {grade: 3, c: 6.25},
	"riv_brown_trout":   {grade: 2, c: 3.214},
	"riv_rb_trout":      {grade: 2, c: 2.5},
	"rudd":              {grade: 2, c: 1.248},
	"ruffe":             {grade: 3, c: 11.996552},
	"ruffe_n":           {grade: 2, c: 44.4398},
	"s.bream":           {grade: 3, c: 3.214},
	"s.orfe":            {grade: 2, c: 0.8002},
	"s_carp":            {grade: 3, c: 6.76},
	"s_freaks":          {grade: 3, c: 3.999984},
	"s_saurus":          {grade: 3, c: 170.9928},
	"sabrefish":         {grade: 2, c: 0.8994},
	"sardina_p":         {grade: 2, c: 134.9637},
	"sea_kambala":       {grade: 3, c: 21.4282},
	"sebastes_m":        {grade: 2, c: 8.888},
	"sevan_trout":       {grade: 2, c: 1.9443},
	"sh_barbel":         {grade: 2, c: 6.8095},
	"shemaya_cha":       {grade: 2, c: 10.935897},
	"sig":               {grade: 1, c: 9.3709},
	"sig_ludoga":        {grade: 2, c: 3.3337},
	"sig_p":             {grade: 2, c: 1.8854},
	"sig_vouksa":        {grade: 2, c: 6.25},
	"sm_bass":           {grade: 2, c: 2.5449},
	"sp_bass":           {grade: 2, c: 3.8997},
	"st_sturgeon":       {grade: 2, c: 1.375},
	"sterlet":           {grade: 2, c: 12.9998},
	"syb_vendace":       {grade: 2, c: 3.0791},
	"taimen":            {grade: 1, c: 0.9068},
	"taran":             {grade: 2, c: 2.3748},
	"tench":             {grade: 4, c: 2.6667},
	"tiulka":            {grade: 1, c: 60.0417},
	"turbot":            {grade: 2, c: 4.8998},
	"valyok":            {grade: 1, c: 6.9975},
	"vendace":           {grade: 2, c: 3.9969},
	"wa_bream":          {grade: 2, c: 3.2702},
	"walleye":           {grade: 2, c: 4.1738},
	"warm":              {grade: 2, c: 1.0351},
	"wh_bass":           {grade: 2, c: 0.7258},
	"white_crappie":     {grade: 2, c: 1.5476},
	"yotsushiro":        {grade: 2, c: 12.5001},
	"zoarces_v":         {grade: 3, c: 146.6671},
	"zs_grayling":       {grade: 2, c: 5.0657},
}

// fishXPAnchorEntry 同种锚条目。
type fishXPAnchorEntry struct {
	grade int64
	c     float64
}

// fishXPGradeFactor 全表统一档位阶梯（相对未达标 g1）= 1 : 3.265 : 20.40 : 40.81。
// 这是**定稿基准**（2026-11-23 用户确认）：游戏内所有经验结算都以本表为准，
// 不再等待官服 g4 实测、也不再逐档贴合个别样本。
//
// 数值来源：g1 中位 0.3063×g2、g3 中位 6.25×g2（各 10 种成档实测取中位）；g4 取 2×g3（12.5）。
// 官服实测只用于确定各鱼种的「未达标基数」，档位形状一律由本表给出。
// 改动本表等于改游戏经济数值，必须先过 `specs/changes/unified-grade-ladder` 与一致性测试。
var fishXPGradeFactor = [4]float64{
	1, 1 / 0.3063, 6.25 / 0.3063, 12.5 / 0.3063,
}

// fishXPGroupC 亲缘组基数：只存未达标档（g1）的 XP/克，四档由统一阶梯生成。
// 取值=组内实测成员的 g1 中位；原 g2/g3 组中位留在 git 历史里，不再参与计算。
var fishXPGroupC = map[string]float64{
	"apex":           3.734,    // 实测成员 2/7
	"carp_mid":       0.5335,   // 实测成员 12/12
	"catfish":        0.3676,   // 实测成员 3/6
	"flatfish":       0.5468,   // 实测成员 5/6
	"herb_carp":      1.7609,   // 实测成员 6/8
	"marine_gadid":   1.9909,   // 实测成员 5/6
	"marine_pelagic": 41.3417,  // 实测成员 2/9
	"marine_rock":    2.5,      // 实测成员 9/22
	"predator":       0.8429,   // 实测成员 14/20
	"roach_run":      0.2451,   // 实测成员 4/10
	"salmonid":       1.2529,   // 实测成员 23/54
	"small_forage":   3.4471,   // 实测成员 3/13
	"still_carp":     1.0017,   // 实测成员 5/5
	"sturgeon":       1.625,    // 实测成员 5/12
	"sunfish":        0.5221,   // 实测成员 4/5
	"weak_silver":    179.9118, // 实测成员 8/9；组内已无成员使用，g1 异常值待清
}

// fishXPInferred 无任何实测、也没有可落亲缘组的鱼种，按同属/近缘组推断的未达标基数。
// 仅兜底这 4 种，不得当实测值引用；新鱼种缺数值时优先补实测，别往这里塞。
//   - roach：与 c.roach（常见拟鲤）同属同体型段；
//   - s_lamprey / trid_lamprey：与 c_lamprey 等七鳃鳗同科，沿用 marine_rock 组量级；
//   - frog：原落 weak_silver 组（该组 g1 被牡蛎 179.9/g 拉高），改用 small_forage 组量级。
var fishXPInferred = map[string]float64{
	"roach":        0.1470,
	"s_lamprey":    2.5,
	"trid_lamprey": 2.5,
	"frog":         3.4471,
}

// fishXPAnchorOverride g1 基数的人工设定值（2026-11-25 基线重生成）。
//
// 与 fishXPAnchor 的区别：那边靠「锚档实测 c ÷ 阶梯」反推 g1，这里直接钉 g1 本身。
// 用于后台调过 base rate、但没有新实测样本的鱼种——把它们塞进 fishXPAnchor
// 会谎称自己有条实测锚点，塞进 fishXPInferred 又违反那张表「只放无实测推断种」
// 的语义，所以单开一张。
//
// 值为 species_econ 覆盖里的 g1 原值；g2~g4 若偏离阶梯，另见 fishXPExplicit。
var fishXPAnchorOverride = map[string]float64{
	"c_lamprey":  850.0,
	"chimaera":   88.88,
	"fe_lamprey": 2888.88,
	"frog":       0.88,
	"lophius_p":  28.8,
	"u_lamprey":  2888.88,
}

// fishXPRateFor 返回 XP 速率 c（XP = trunc(c×W_g)）。
// 优先级：本种实测精确命中 → g4 由上星推导 → 同种锚×档位乘子 → 皮肤别名→宿主整链 →
// 无实测种推断表 → 亲缘组 → 旧 valueRates → 缺省。
// 注意：皮肤变种（common_carp 等）官服有独立经济数值，实测/锚优先于宿主继承；
// 别名仅兜零实测的皮肤种。
// fishXPExplicit 逐档显式值：后台 species_econ 覆盖合并进基线后的落点
// （2026-11-25 基线重生成）。语义与 fishXPRateC 相同，都是 (鱼种,档位)→c 的
// **绝对值**，但优先级在阶梯之上——命中即直接返回，不再按 g1 基数 × 阶梯推算。
//
// 为什么需要这张表：species_econ 原有 8 种鱼的覆盖，其中 7 种把 g2~g4 人为压平
// （七鳃鳗族 g4 只有阶梯值的 8%，frog 的 g3 是阶梯的 33%），这是刻意的经济调控，
// 不是实测噪声。若只把 g1 基数搬进 fishXPAnchor/fishXPInferred，四档会被
// fishXPGradeFactor 重新拉成统一形状，压平的效果全丢。
//
// 缺省仍然走阶梯：这张表只放**显式偏离阶梯**的档位，没列的档位照旧
// c = fishXPBaseRate(鱼种) × fishXPGradeFactor[档位]。
var fishXPExplicit = map[vgKey]float64{
	// chimaera：g2 抬到 488.88（阶梯值 290.17 的 1.68×），g3/g4 压到阶梯的 38%/30%。
	{"chimaera", 2}: 488.88,
	{"chimaera", 3}: 688.88,
	{"chimaera", 4}: 1088.88,
	// frog：g1 0.88，g2~g4 全部压平（阶梯会给 2.87/17.96/35.91）。
	{"frog", 2}: 2.88,
	{"frog", 3}: 5.88,
	{"frog", 4}: 8.88,
	// lophius_p：g1 28.8，g2~g4 压到阶梯的 73%/19%/14%。
	{"lophius_p", 2}: 68.88,
	{"lophius_p", 3}: 108.88,
	{"lophius_p", 4}: 168.88,
	// 七鳃鳗三兄弟：g1 基数很高（850~2888.88），但 g2~g4 被封在 1 万以内，
	// 避免高价值鱼在高档位刷出天文数字经验。阶梯原值会到 11.8 万。
	{"c_lamprey", 2}:  2168.0,
	{"c_lamprey", 3}:  6888.0,
	{"c_lamprey", 4}:  9888.0,
	{"fe_lamprey", 2}: 6888.88,
	{"fe_lamprey", 3}: 8999.99,
	{"fe_lamprey", 4}: 9999.99,
	{"u_lamprey", 2}:  6888.88,
	{"u_lamprey", 3}:  8888.88,
	{"u_lamprey", 4}:  9999.99,
}

// 刻意不进本表：bh_carp。后台曾把它覆盖成 g2=0.4233/g3=1.6933/g4=4.4333，
// 但 fishXPAnchor 里 bh_carp 有官服实测锚点 {grade:3, c:4.3333}，实测 XP
// 37894g 得 164206（TestOfficialXPAnchors 锁着）。覆盖值把 g3 压到实测的 39%，
// 等于用手填值盖掉实测证据——合并基线时按「实测优先」原则丢弃覆盖，保留锚点。

// fishXPRateFor 返回 XP 速率 c（XP = trunc(c×W_g)）。
// 2026-11-23 起全表统一阶梯：c = 本种未达标基数 × fishXPGradeFactor[档位]。
// 官服实测只用来定「未达标基数」——取该种锚档实测值 ÷ 锚档乘子；
// 同种其他档位的实测样本仍留在 fishXPRateC 里作证据，但不再逐档覆盖阶梯
// （决策：四档形状统一优先于逐档贴合，被放弃的 26 条样本最大偏离 ±46%）。
func fishXPRateFor(species string, grade int64) float64 {
	if grade < 1 {
		grade = 1
	} else if grade > 4 {
		grade = 4
	}
	// 逐档显式值优先：后台刻意压平/抬高的档位不能被阶梯拉回统一形状。
	if v, ok := fishXPExplicit[vgKey{species: species, grade: grade}]; ok {
		return v
	}
	// 锚档直接回实测值，避免 (c/f)×f 的浮点尾差把 trunc 结果挪一格。
	if a, ok := fishXPAnchor[species]; ok && a.grade == grade {
		return a.c
	}
	return fishXPBaseRate(species) * fishXPGradeFactor[grade-1]
}

// fishXPBaseRate 返回未达标档（g1）的 XP/克基数。
// 优先级：人工设定 g1 → 本种锚档实测 → 皮肤别名→宿主 → 无实测种推断表
// → 亲缘组 → 旧 valueRates → 缺省。
// 人工设定排最前：它承载的是后台调过的经济值，比「锚档反推」更直接。
func fishXPBaseRate(species string) float64 {
	if v, ok := fishXPAnchorOverride[species]; ok {
		return v
	}
	if a, ok := fishXPAnchor[species]; ok {
		return a.c / fishXPGradeFactor[a.grade-1]
	}
	if host, ok := fishPullAlias[species]; ok {
		return fishXPBaseRate(host)
	}
	if v, ok := fishXPInferred[species]; ok {
		return v
	}
	if grp, ok := fishPullSpeciesGroup[species]; ok {
		if v, ok2 := fishXPGroupC[grp]; ok2 {
			return v
		}
	}
	if rate, ok := valueRates[vgKey{species: species, grade: 1}]; ok && rate[0] > 0 {
		return rate[0]
	}
	return fishXPRateFallback
}
