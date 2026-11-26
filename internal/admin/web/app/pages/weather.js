// 天气预览：每张图的温度区间、逐时天气、当前活跃鱼种。
//
// 数据来源单一：全部走 /api/weather/preview 与 /api/weather/active-fish，
// 后端直接调 gameserver 的天气排程，与客户端下发的预报、判鱼用的天气是同一份，
// 不存在「后台看到的和游戏里不一样」。游戏时钟 1 小时 = 现实 150 秒。
//
// 移植自旧后台 #view-weather 的 21 个函数（loadWeather / renderWeatherSummary /
// renderWeatherDays / renderWeatherChart / renderWeatherBands / renderWeatherSeasons /
// loadWeatherFish / renderWeatherFish / loadWeatherOverview / markWeatherCompare …）。
// 旧版把颜色与网格尺寸写死在深色底上，这里改成 CSS 变量 + currentColor，
// 明暗两套主题都能读——硬编码的 #0e1b29 卡片在浅色主题下就是一块黑砖。
import { h, api, errText, pageHead, work, btn, empty } from '../core.js';

// 六种天气的中文名由后端给（weatherPreviewKindNames），前端不另立一份。
// 这里只做「中文名 → 样式类」，让图标与文字颜色跟着主题走。
const KIND_CLASS = { 晴: 'clear', 少云: 'few', 多云: 'cloudy', 小雨: 'rain', 大雨: 'heavy', 雷雨: 'thunder' };
const kindCls = (name) => KIND_CLASS[name] || 'cloudy';

// 数字格式化：null/非数一律 —，避免页面上出现 NaN 或 undefined。
const wxF = (v, d = 1) => (v == null || v === '' || !isFinite(Number(v)) ? '—' : Number(v).toFixed(d));

// 天气图标：手写 SVG，不引外部字体/图片。全部用 currentColor 上色，
// 颜色由外层 .wx-ic 的样式类给，这样浅色/深色主题各有一套亮度。
function wxIcon(kind, size = 40) {
  const sun = '<circle cx="16" cy="14" r="6.2" fill="currentColor"/>';
  const rays = '<g stroke="currentColor" stroke-width="1.7" stroke-linecap="round" fill="none">'
    + '<path d="M16 2.6v3M16 22.4v3M4.6 14h3M24.4 14h3M8.1 6.1l2.1 2.1M21.8 19.8l2.1 2.1M8.1 21.9l2.1-2.1M21.8 8.2l2.1-2.1"/></g>';
  const smallSun = '<g transform="translate(-3 -4) scale(.72)">' + sun + rays + '</g>';
  const cloud = (dy = 0) => `<path transform="translate(0 ${dy})" fill="currentColor" d="M10.5 26a5.2 5.2 0 0 1 .9-10.3 6.6 6.6 0 0 1 12.5 1.7A4.4 4.4 0 0 1 23 26z"/>`;
  const drops = (dy = 0, w = 2) => `<g stroke="currentColor" stroke-width="${w}" stroke-linecap="round" fill="none">`
    + `<path d="M13 ${29 + dy}v3M19 ${29 + dy}v3M16 ${33 + dy}v3"/></g>`;
  const bolt = (dy = 0) => `<path transform="translate(0 ${dy})" fill="currentColor" d="M17 26l-4.5 6.5h3L14 39l7-8.5h-3.2L19 26z"/>`;
  let body;
  if (kind === '晴') body = sun + rays;
  else if (kind === '少云') body = smallSun + cloud(3);
  else if (kind === '多云') body = cloud(0);
  else if (kind === '小雨') body = cloud(-2) + drops(0);
  else if (kind === '大雨') body = cloud(-3) + drops(-1, 2.4);
  else if (kind === '雷雨') body = cloud(-4) + bolt(-1);
  else body = cloud(0);
  return `<svg viewBox="0 0 32 40" width="${size}" height="${size}" aria-hidden="true">${body}</svg>`;
}

// 日号 →「今天 / 明天 / M月D日」。日号是世界时钟轴（与客户端「游戏中 N 天」同源），
// 所以 day%365 算出来的月日与页面上的季节标签属于同一套历法，不会出现「1月5日 · 秋」。
function wxDayLabel(day, nowDay) {
  const diff = day - nowDay;
  if (diff === 0) return ['今天', ''];
  if (diff === 1) return ['明天', ''];
  if (diff === -1) return ['昨天', ''];
  const idx = ((day % 365) + 365) % 365;
  const months = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  let m = 0, d = idx;
  while (m < months.length && d >= months[m]) { d -= months[m]; m += 1; }
  return [(m + 1) + '月' + (d + 1) + '日', '第' + day + '天'];
}

// 卡片外壳：与其它页一致的 .card + .section-head + .card-pad。
function card(title, desc, ...body) {
  return h('div', { class: 'card' },
    h('div', { class: 'section-head' },
      h('div', {}, h('h2', { text: title }), desc ? h('p', { text: desc }) : null)),
    h('div', { class: 'card-pad' }, ...body));
}

export default async function weather(params, scope) {
  const head = pageHead('天气预览', '每张图的温度区间与逐时天气。与客户端天气预报、判鱼共用同一套数据（游戏 1 小时 = 现实 150 秒）。');

  const state = { maps: [], mapKey: '', days: 7, cur: null, pick: null };

  const status = h('span', { class: 'muted small', id: 'wxStatus', text: '读取中…' });
  const dayLabel = h('span', { class: 'muted small', id: 'wxDayLabel' });

  // 下拉与天数选择只建一次：切图/改天数靠重载数据，重建控件会让正在悬停的选择器失焦。
  const mapSel = h('select', { id: 'wxMap', 'aria-label': '选择水域',
    onchange: () => { state.mapKey = mapSel.value; load(); } });
  const daysSel = h('select', { id: 'wxDays', 'aria-label': '预报天数',
    onchange: () => { state.days = Number(daysSel.value) || 7; load(); } },
  ...[3, 7, 14].map((n) => h('option', { value: String(n), text: '看 ' + n + ' 天', selected: n === 7 })));

  const sumEl = h('div', { class: 'wx-sum', id: 'wxSum' });
  const dayCards = h('div', { class: 'wx-day-row', id: 'wxDayCards' }, empty('读取中…'));
  const chartEl = h('div', { class: 'wx-chart', id: 'wxChart' }, empty('读取中…'));
  const bandBox = h('div', { id: 'wxBandBox' }, empty('读取中…'));
  const seasonBox = h('div', { id: 'wxSeasonBox' }, empty('读取中…'));
  const cmpBox = h('div', { class: 'table-wrap', id: 'wxCmpBox' }, empty('点「全部水域对比」加载'));
  const fishStatus = h('span', { class: 'muted small', id: 'wxFishStatus' });
  const fishSum = h('div', { class: 'wx-sum', id: 'wxFishSum' });
  const fishBox = h('div', { id: 'wxFishBox' }, empty('读取中…'));

  // ---------- 地图下拉 ----------
  function paintMapOptions() {
    mapSel.replaceChildren(...state.maps.map((m) => h('option', { value: m.key, text: m.map_name || m.key })));
    if (state.mapKey) mapSel.value = state.mapKey;
  }

  // ---------- 主流程 ----------
  // lastFishMap 记住活跃鱼种上次是按哪张图取的。
  // 改天数（3/7/14）不影响「此刻哪些鱼活跃」——那张表只跟当前游戏时刻与地图有关，
  // 跟着重取一次纯属浪费（还要后端扫一遍鱼种生态表）。只有换图或点「刷新」才重取。
  let lastFishMap = null;
  // 全区对比表只跟天数有关，跟当前选中哪张图无关：切图时只切高亮行，
  // 不重发请求（18 张图 × 14 天也要扫一遍全部天气模板）。
  let lastCmpDays = 0;

  async function load({ forceFish = false } = {}) {
    status.textContent = '读取中…';
    try {
      if (!state.maps.length) {
        const d = await api('/api/weather/maps');
        state.maps = Array.isArray(d.maps) ? d.maps : [];
        if (!scope.isCurrent()) return;
        paintMapOptions();
        if (!state.mapKey) {
          // 默认选阿赫图巴河（温差最大、最能看效果），没有就第一张。
          const ahtuba = state.maps.find((m) => m.key === 'level_009_ahtuba');
          state.mapKey = (ahtuba || state.maps[0] || {}).key || '';
          if (state.mapKey) mapSel.value = state.mapKey;
        }
      }
      if (!state.mapKey) { status.textContent = '没有可用的水域'; return; }
      const want = state.mapKey, wantDays = state.days;
      const d = await api('/api/weather/preview?map=' + encodeURIComponent(want) + '&days=' + wantDays);
      // 期间用户又切了图或改了天数：这一份已经过期，丢掉，免得盖掉新页面的数字。
      if (!scope.isCurrent() || want !== state.mapKey || wantDays !== state.days) return;
      state.cur = d.preview;
      if (!state.cur) { status.textContent = '没有天气数据'; return; }
      const hasNow = state.cur.days.some((x) => x.day === d.now_day);
      state.pick = hasNow ? d.now_day : (state.cur.days[0] || {}).day;
      status.textContent = `${state.cur.map_name} · ${state.cur.days.length} 天 · ${state.cur.season_name}季`;
      renderSummary();
      renderDays();
      renderChart();
      renderBands();
      renderSeasons();
      // 活跃鱼种按 map 单独取一次（还要读鱼种生态表），失败不该拖垮整页。
      if (forceFish || lastFishMap !== want) {
        lastFishMap = want;
        loadFish().catch(() => {});
      }
      // 对比表：天数变了才重取；只换了地图就仅切高亮行。
      if (lastCmpDays !== wantDays) {
        lastCmpDays = wantDays;
        loadCompare().catch(() => {});
      } else {
        markCompare();
      }
    } catch (e) {
      status.textContent = '读取失败：' + errText(e);
      dayCards.replaceChildren(empty('读取失败', errText(e)));
      chartEl.replaceChildren(empty('读取失败', errText(e)));
    }
  }

  // ---------- 概要 chip ----------
  // 时钟只在最右侧出现一次：左侧再放一个「游戏时钟」chip 就是同一个数写两遍。
  function renderSummary() {
    const p = state.cur;
    const nowH = p.hours.find((x) => x.is_now);
    const chips = [
      ['当前天气', h('b', { text: nowH ? nowH.kind_name : '—' }),
        `${nowH ? nowH.segment_name : ''} · 气温 ${wxF(nowH ? nowH.air_temp_c : null)}℃ · 水温 ${wxF(nowH ? nowH.water_c : null)}℃`],
      ['气温区间', h('b', { text: `${wxF(p.air_min_c)} ~ ${wxF(p.air_max_c)}℃` }), `近 ${p.days.length} 天`],
      ['水温区间', h('b', { text: `${wxF(p.water_min_c)} ~ ${wxF(p.water_max_c)}℃` }), '全水层覆盖'],
      ['季节', h('b', { text: p.season_name }), `风 ${wxF(nowH ? nowH.wind_kmh : null)} km/h · 雾 ${wxF(nowH ? nowH.fog : null, 2)}`],
    ];
    sumEl.replaceChildren(
      ...chips.map(([k, v, s]) => h('span', { class: 'wx-chip' },
        h('span', { class: 'k', text: k }), v, h('span', { class: 'k', text: s }))),
      h('span', { class: 'wx-clock' },
        h('span', { class: 'k', text: `游戏内第 ${p.now_day} 天` }),
        h('b', { text: p.game_clock || '--:--' })));
  }

  // ---------- 7 天卡片 ----------
  // 用 div + role=button 而不是 <button>：theme-aerial.css 有一条全局
  // `button:not(...){background:#101d1a!important}`，把普通按钮强制涂成深色块。
  // 7 张深色砖在浅色主题下和整页打架，而且和旁边的 .card 不在一个视觉层里。
  // 键盘可达性靠 tabindex + Enter/Space 自己补。
  function renderDays() {
    const p = state.cur;
    dayCards.replaceChildren(...p.days.map((d) => {
      const [lab, sub] = wxDayLabel(d.day, p.now_day);
      const isNow = d.day === p.now_day;
      const on = d.day === state.pick;
      const pick = () => { if (state.pick === d.day) return; state.pick = d.day; renderDays(); renderChart(); };
      return h('div', {
        class: 'wx-day' + (on ? ' on' : '') + (isNow ? ' now' : ''),
        role: 'button', tabindex: '0', 'aria-pressed': String(on),
        title: sub || lab,
        onclick: pick,
        onkeydown: (e) => {
          if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); }
        },
      },
      h('div', { class: 'dl' }, h('b', { text: lab })),
      h('div', { class: 'ic wx-k-' + kindCls(d.kind_name), html: wxIcon(d.kind_name, 40) }),
      h('div', { class: 'kn wx-t-' + kindCls(d.kind_name), text: d.kind_name }),
      h('div', { class: 'tt' }, h('span', { class: 'lo', text: wxF(d.low_c, 0) + '°' }), ' / ',
        h('span', { class: 'hi', text: wxF(d.high_c, 0) + '°' })),
      h('div', { class: 'wt', text: `水 ${wxF(d.water_low_c, 0)}~${wxF(d.water_high_c, 0)}℃` }),
      h('div', { class: 'rn', text: d.rain_hours > 0 ? `降雨 ${d.rain_hours} 小时` : '无降雨' }));
    }));
    const d = p.days.find((x) => x.day === state.pick);
    dayLabel.textContent = d ? wxDayLabel(d.day, p.now_day)[0] : '';
  }

  // ---------- 24 小时温度曲线 ----------
  // 手绘 SVG（无第三方图表库）：气温 / 表层 / 中层 / 底层四条线 + 降水雷暴柱 + 昼夜分段底。
  function renderChart() {
    const p = state.cur;
    if (!p) return;
    const day = p.days.find((d) => d.day === state.pick) || p.days[0];
    const hs = p.hours.filter((x) => x.day === day.day);
    if (!hs.length) { chartEl.replaceChildren(empty('该日无逐时数据')); return; }
    const W = 1180, H = 300, padL = 46, padR = 16, padT = 18, padB = 34;
    const iw = W - padL - padR, ih = H - padT - padB;
    // 纵轴范围：四条温度线一起取，上下各留 2℃。
    let lo = Infinity, hi = -Infinity;
    for (const x of hs) for (const v of [x.air_temp_c, x.surface_c, x.water_c, x.deep_c]) { if (v < lo) lo = v; if (v > hi) hi = v; }
    lo = Math.floor(lo - 2); hi = Math.ceil(hi + 2);
    if (hi - lo < 6) hi = lo + 6;
    const xAt = (i) => padL + iw * (i / (hs.length - 1 || 1));
    const yAt = (v) => padT + ih - (ih * (v - lo) / (hi - lo || 1));

    // 横向网格 + 温度刻度
    const grid = [];
    const step = (hi - lo) > 40 ? 10 : ((hi - lo) > 18 ? 5 : 2);
    for (let t = Math.ceil(lo / step) * step; t <= hi; t += step) {
      grid.push(`<line x1="${padL}" y1="${yAt(t).toFixed(1)}" x2="${W - padR}" y2="${yAt(t).toFixed(1)}" class="wx-grid"/>`);
      grid.push(`<text x="${padL - 8}" y="${(yAt(t) + 3.5).toFixed(1)}" class="wx-axis-t" text-anchor="end">${t}</text>`);
    }
    // 降水/雷暴柱：只占下方 22% 高度，避免压住温度线。
    const bars = [];
    const bw = Math.max(3, (iw / hs.length) * 0.62);
    for (let i = 0; i < hs.length; i += 1) {
      const x = hs[i], bx = xAt(i) - bw / 2, base = padT + ih;
      if (x.rain > 0.01) {
        const bh = ih * 0.22 * Math.min(1, x.rain);
        bars.push(`<rect x="${bx.toFixed(1)}" y="${(base - bh).toFixed(1)}" width="${bw.toFixed(1)}" height="${bh.toFixed(1)}" class="wx-rain" rx="1.5"/>`);
      }
      if (x.thunder > 0.01) {
        const bh = ih * 0.22 * Math.min(1, x.thunder);
        bars.push(`<rect x="${bx.toFixed(1)}" y="${(base - bh).toFixed(1)}" width="${bw.toFixed(1)}" height="${bh.toFixed(1)}" class="wx-thunder" rx="1.5"/>`);
      }
    }
    const line = (key, cls) => `<polyline points="${hs.map((x, i) => `${xAt(i).toFixed(1)},${yAt(x[key]).toFixed(1)}`).join(' ')}" class="wx-line ${cls}" fill="none" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`;

    // 昼夜段背景（按小时连续性切段）
    const segs = [];
    const segCls = { 夜: 'night', 晨: 'dawn', 昼: 'day', 昏: 'dusk' };
    let start = 0;
    for (let i = 1; i <= hs.length; i += 1) {
      if (i === hs.length || hs[i].segment !== hs[start].segment) {
        const gap = i > 1 ? (xAt(1) - xAt(0)) / 2 : 0;
        const x0 = xAt(start) - gap;
        const x1 = xAt(i - 1) + (i < hs.length ? (xAt(1) - xAt(0)) / 2 : 0);
        segs.push(`<rect x="${x0.toFixed(1)}" y="${padT}" width="${(x1 - x0).toFixed(1)}" height="${ih}" class="wx-seg ${segCls[hs[start].segment_name] || 'day'}"/>`);
        if (x1 - x0 > 28) segs.push(`<text x="${((x0 + x1) / 2).toFixed(1)}" y="${padT + 11}" class="wx-seg-t" text-anchor="middle">${hs[start].segment_name}</text>`);
        start = i;
      }
    }

    // 当前时刻竖线（该日在窗口内才画）。颜色 #42b8ff 写死：它是「此刻」的唯一标记，
    // 不跟随主题变，验收测试也按这个色值确认「只有今天有竖线」。
    let nowLine = '';
    const nowIdx = hs.findIndex((x) => x.is_now);
    if (nowIdx >= 0) {
      nowLine = `<line x1="${xAt(nowIdx).toFixed(1)}" y1="${padT}" x2="${xAt(nowIdx).toFixed(1)}" y2="${padT + ih}" stroke="#42b8ff" stroke-width="1.5" stroke-dasharray="4 3"/>`
        + `<circle cx="${xAt(nowIdx).toFixed(1)}" cy="${yAt(hs[nowIdx].water_c).toFixed(1)}" r="3.5" fill="#42b8ff"/>`;
    }

    // 横轴刻度：每 3 小时一个，24 小时共 8 个。
    const ticks = [];
    for (let i = 0; i < hs.length; i += 1) {
      if (hs[i].hour % 3 !== 0) continue;
      ticks.push(`<line x1="${xAt(i).toFixed(1)}" y1="${padT + ih}" x2="${xAt(i).toFixed(1)}" y2="${padT + ih + 5}" class="wx-tick"/>`);
      ticks.push(`<text x="${xAt(i).toFixed(1)}" y="${padT + ih + 18}" class="wx-axis-t" text-anchor="middle">${String(hs[i].hour).padStart(2, '0')}:00</text>`);
    }

    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    svg.setAttribute('preserveAspectRatio', 'xMidYMid meet');
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', `${day.kind_name}日的温度曲线`);
    svg.innerHTML = segs.join('') + grid.join('') + bars.join('')
      + line('air_temp_c', 'air') + line('surface_c', 'surface')
      + line('water_c', 'mid') + line('deep_c', 'deep')
      + nowLine + ticks.join('');

    const legend = h('div', { class: 'wx-legend' },
      ...[['air', '气温'], ['surface', '表层水温'], ['mid', '中层水温（默认钓组）'], ['deep', '底层水温（底钓）']]
        .map(([c, t]) => h('span', {}, h('i', { class: 'wx-sw ' + c }), t)),
      h('span', {}, h('i', { class: 'wx-sw rain' }), '降水'),
      h('span', {}, h('i', { class: 'wx-sw thunder' }), '雷暴'));
    chartEl.replaceChildren(h('div', { class: 'wx-chart-in' }, svg), legend);
  }

  // ---------- 温度分档条 ----------
  function renderBands() {
    const p = state.cur;
    if (!p || !p.bands || !p.bands.length) { bandBox.replaceChildren(empty('无分档数据')); return; }
    const bands = p.bands;
    const total = bands.reduce((s, b) => s + b.count, 0) || 1;
    const span = Math.max(1, p.water_max_c - p.water_min_c);
    // 条宽按样本占比，颜色按温度从冷到暖（蓝→青→橙），一眼看出这批样本偏冷还是偏暖。
    const bar = bands.map((b) => {
      const pct = b.count / total * 100;
      if (pct <= 0) return null;
      const t = (b.from_c - p.water_min_c) / span;
      const col = `hsl(${Math.round(205 - 165 * t)},72%,${Math.round(50 + 6 * t)}%)`;
      return h('i', { style: { width: pct.toFixed(2) + '%', background: col },
        title: `${wxF(b.from_c, 0)}~${wxF(b.to_c, 0)}℃：${b.count} 小时（${(b.share * 100).toFixed(1)}%）` });
    }).filter(Boolean);
    const rows = bands.filter((b) => b.count > 0).map((b) => h('div', { class: 'wx-band-row' },
      h('span', { text: `${wxF(b.from_c, 0)} ~ ${wxF(b.to_c, 0)}℃` }),
      h('span', { text: `${b.count} 小时 · ${(b.share * 100).toFixed(1)}%` })));
    bandBox.replaceChildren(
      h('div', { class: 'wx-band' }, ...(bar.length ? bar : [h('i', { style: { width: '100%' } })])),
      h('div', { class: 'wx-band-row' },
        h('span', { text: `冷 ${wxF(p.water_min_c)}℃` }), h('span', { text: `暖 ${wxF(p.water_max_c)}℃` })),
      h('div', { class: 'wx-band-list' }, ...(rows.length ? rows : [empty('窗口内无数据')])),
      h('p', { class: 'muted small wx-note' },
        '按', h('b', { text: '中层水温' }), `每 2℃ 分档统计本窗口 ${p.days.length} 天 × 24 小时 = ${total} 个采样点。`,
        `区间（${wxF(p.water_min_c)}~${wxF(p.water_max_c)}℃）取`, h('b', { text: '全水层' }),
        '，所以比这里的分档更宽——底钓看到的水温比中层低，表层比中层高。'));
  }

  // ---------- 四季对比 ----------
  function renderSeasons() {
    const p = state.cur;
    if (!p || !p.seasons || !p.seasons.length) { seasonBox.replaceChildren(empty('无季节数据')); return; }
    // 四条共用一根轴：以全部季节的最低/最高水温为界，横向位置才有可比性。
    const lo = Math.min(...p.seasons.map((s) => s.water_min_c));
    const hi = Math.max(...p.seasons.map((s) => s.water_max_c));
    const span = Math.max(1, hi - lo);
    const pos = (v) => ((v - lo) / span * 100).toFixed(1);
    const nowS = p.seasons.find((s) => s.is_now);
    seasonBox.replaceChildren(
      h('div', { class: 'wx-season' },
        ...p.seasons.map((s) => h('div', { class: 'sr' + (s.is_now ? ' now' : '') },
          h('span', { class: 'sn', text: s.season_name + (s.is_now ? ' ●' : '') }),
          h('span', { class: 'sb' },
            h('i', { style: { left: pos(s.water_min_c) + '%', width: Math.max(1.5, (s.water_max_c - s.water_min_c) / span * 100).toFixed(1) + '%' } })),
          h('span', { class: 'sv', text: `${wxF(s.water_min_c)}~${wxF(s.water_max_c)}℃` }))),
        h('div', { class: 'wx-axis' },
          h('span'), h('span', { class: 'wx-axis-mid' },
            h('span', { text: wxF(lo, 0) + '℃' }), h('span', { text: wxF((lo + hi) / 2, 0) + '℃' }), h('span', { text: wxF(hi, 0) + '℃' })),
          h('span'))),
      h('p', { class: 'muted small wx-note' },
        `全年水温 ${wxF(lo)}~${wxF(hi)}℃，跨度 `, h('b', { text: wxF(hi - lo) + '℃' }),
        nowS ? `；当前 ${nowS.season_name}季 ${wxF(nowS.water_min_c)}~${wxF(nowS.water_max_c)}℃` : '',
        '。四季扫描用该图 8 天预报模板 × 24 小时 × 5 个水层（0 / .2 / .45 / .7 / .93），与判鱼的水层口径一致。',
        h('b', { text: '水温的季节项是全局硬编码' }), '，地图差异经热响应衰减后传导，所以各图四季跨度相近、绝对温度不同。'));
  }

  // ---------- 全部水域对比 ----------
  async function loadCompare() {
    const wantDays = state.days;
    try {
      const d = await api('/api/weather/preview?days=' + wantDays);
      if (!scope.isCurrent() || wantDays !== state.days) return;
      const list = Array.isArray(d.maps) ? d.maps : [];
      if (!list.length) { cmpBox.replaceChildren(empty('没有数据')); return; }
      const lo = Math.min(...list.map((m) => m.water_min_c));
      const hi = Math.max(...list.map((m) => m.water_max_c));
      const span = Math.max(1, hi - lo);
      const rows = list.map((m) => h('tr', {
        dataset: { map: m.map },
        class: m.map === state.mapKey ? 'on' : '',
        onclick: () => {
          // 点一行等于切到那张图：改完下拉立刻重载，滚动回顶部免得看着像没反应。
          if (state.mapKey === m.map) return;
          state.mapKey = m.map;
          mapSel.value = m.map;
          window.scrollTo({ top: 0, behavior: 'smooth' });
          load();
        },
      },
      h('td', {}, h('b', { text: m.map_name }), h('div', { class: 'mono', text: m.map })),
      h('td', { class: 'mono', text: `${wxF(m.air_min_c)}~${wxF(m.air_max_c)}℃` }),
      h('td', { class: 'mono wx-water', text: `${wxF(m.water_min_c)}~${wxF(m.water_max_c)}℃` }),
      h('td', {}, h('span', { class: 'wx-rng' },
        h('i', { style: { left: ((m.water_min_c - lo) / span * 100).toFixed(1) + '%', width: Math.max(1.5, (m.water_max_c - m.water_min_c) / span * 100).toFixed(1) + '%' } }))),
      h('td', {}, h('span', { class: 'pill', text: m.season_name }))));
      const widest = list.reduce((a, b) => (b.water_max_c - b.water_min_c) > (a.water_max_c - a.water_min_c) ? b : a);
      const narrow = list.reduce((a, b) => (b.water_max_c - b.water_min_c) < (a.water_max_c - a.water_min_c) ? b : a);
      cmpBox.replaceChildren(
        h('table', { class: 'wx-cmp' },
          h('thead', {}, h('tr', {},
            h('th', { text: '水域' }), h('th', { text: '气温区间' }), h('th', { text: '水温区间' }),
            h('th', { style: { width: '34%' }, text: `相对位置（全局 ${wxF(lo)}~${wxF(hi)}℃）` }), h('th', { text: '本季' }))),
          h('tbody', {}, ...rows)),
        h('p', { class: 'muted small wx-note' },
          `窗口 ${wantDays} 天，区间取`, h('b', { text: '全水层' }),
          `。水温跨度最小的是「${narrow.map_name}」，最大的是「${widest.map_name}」，两者相差 ${wxF((widest.water_max_c - widest.water_min_c) - (narrow.water_max_c - narrow.water_min_c))}℃。`));
    } catch (e) { cmpBox.replaceChildren(empty('读取失败', errText(e))); }
  }

  function markCompare() {
    for (const tr of cmpBox.querySelectorAll('tbody tr')) tr.classList.toggle('on', tr.dataset.map === state.mapKey);
  }

  // ---------- 当前活跃鱼种 ----------
  // 后端给的是「昼夜系数 × 水温系数」两个分项再加乘积，这里只负责画。
  // 口径必须写在页面上：不含饵料与水层，所以它是「这个时段这张图哪些鱼有活性」，
  // 不是「你现在挂这套一定能钓到」。
  async function loadFish() {
    const want = state.mapKey;
    if (!want) return;
    fishStatus.textContent = '读取中…';
    fishBox.replaceChildren(empty('读取中…'));
    try {
      const d = await api('/api/weather/active-fish?map=' + encodeURIComponent(want));
      if (!scope.isCurrent() || want !== state.mapKey) return;
      const a = d.active;
      if (!a) { fishStatus.textContent = ''; fishBox.replaceChildren(empty('没有数据')); return; }
      fishStatus.textContent = `${a.game_hour} 点 · ${a.segment} · 水温 ${wxF(a.water_c)}℃`;
      renderFish(a);
    } catch (e) {
      fishStatus.textContent = '读取失败';
      fishSum.replaceChildren();
      fishBox.replaceChildren(empty('读取失败', errText(e)));
    }
  }

  function renderFish(a) {
    const top = a.fish || [];
    const idle = a.idle || [];
    fishSum.replaceChildren(...[
      ['此刻活跃', h('b', { text: String(a.active_num) }), `/ ${a.total} · 共 ${a.total} 种在册`],
      ['时段', h('b', { text: a.segment }), `${a.game_hour} 点 · ${a.season}季`],
      ['中层水温', h('b', { text: wxF(a.water_c) + '℃' }), '与上方温度曲线同源'],
    ].map(([k, v, s]) => h('span', { class: 'wx-chip' },
      h('span', { class: 'k', text: k }), v, h('span', { class: 'k', text: s }))));

    if (!top.length && !idle.length) {
      fishBox.replaceChildren(empty('该图还没有配置鱼种生态档案'));
      return;
    }
    // 条长按当前名单里的最大值归一化：整个时段都低迷时也能看出彼此高下。
    const maxScore = top.length ? Math.max(...top.map((f) => f.score)) : 1;
    const bar = (v, cls) => h('span', { class: 'wx-fbar' },
      h('i', { class: cls, style: { width: Math.max(2, Math.min(100, v / Math.max(maxScore, 0.0001) * 100)).toFixed(1) + '%' } }));
    const tag = (f) => h('span', { class: 'wx-rt r' + (Number(f.rarity) || 0), text: f.rarity_tag || '普通' });

    const head = h('div', { class: 'wx-frow wx-fhead' },
      h('span', { text: '鱼种' }), h('span', { text: '稀有度' }), h('span', { text: '活跃时段' }),
      h('span', { title: '昼夜四段曲线在当前时段取到的系数，峰值 1.0、最低 0.08', text: '昼夜' }),
      h('span', { title: '按适温区间的高斯响应算出的水温系数，最适温度时为 1.0', text: '水温' }),
      h('span', { text: '综合' }), h('span', { text: '相对强度' }));

    const rows = top.map((f) => h('div', { class: 'wx-frow' },
      h('span', { class: 'nm' }, h('b', { text: f.name_zh }), h('i', { text: f.fish_key })),
      h('span', {}, tag(f)),
      h('span', { class: 'muted', text: `${f.diel_label} · ${wxF(f.temp_min_c, 0)}~${wxF(f.temp_max_c, 0)}℃` }),
      h('span', { class: 'num', text: Number(f.diel_factor).toFixed(2) }),
      h('span', { class: 'num', text: Number(f.thermal_fact).toFixed(2) }),
      h('span', { class: 'num sc', text: Number(f.score).toFixed(2) }),
      h('span', {}, bar(f.score, 'ok'))));

    // 不活跃的单独折起来并写明原因——否则管理员会怀疑「我的鱼去哪了」。
    const idleHtml = idle.length ? h('details', { class: 'wx-fidle' },
      h('summary', { text: `此刻不开口的 ${idle.length} 种（点击展开原因）` }),
      ...idle.map((f) => h('div', { class: 'wx-frow idle' },
        h('span', { class: 'nm' }, h('b', { text: f.name_zh }), h('i', { text: f.fish_key })),
        h('span', {}, tag(f)),
        h('span', { class: 'muted', style: { gridColumn: 'span 3' }, text: f.reason || '' }),
        h('span', { class: 'num sc', text: Number(f.score).toFixed(2) }),
        h('span', {}, bar(f.score, 'no'))))) : null;

    fishBox.replaceChildren(
      h('div', { class: 'wx-ftable' }, head, ...rows, idleHtml || null),
      h('p', { class: 'muted small wx-note' },
        '综合 = 昼夜 × 水温。昼夜取四段曲线在当前时段的值（峰值 1.0、最低 0.08），曲线低于 0.5 的鱼整段禁口（',
        h('b', { text: '非活跃时段（昼夜禁口）' }), '）；水温按高斯响应 ',
        h('code', { text: '0.35+0.65·exp(−0.5z²)' }), ' 算，',
        h('code', { text: 'z=(水温−最适)/max(2.2,(上限−下限)/2)' }), '。',
        h('b', { text: '不含饵料、水层、钓组与技能' }),
        '——那些要挂上具体装备才有意义，见「鱼种档案 → 生态基线」。'));
  }

  // ---------- 骨架 ----------
  const headCard = h('div', { class: 'card', style: { marginBottom: '14px' } },
    h('div', { class: 'section-head' },
      h('div', {}, h('h2', { text: '天气预报' }),
        h('p', { text: '与客户端天气预报、判鱼天气同一套数据；游戏时钟 1 小时 = 现实 150 秒' })),
      status),
    h('div', { class: 'card-pad' },
      h('div', { class: 'wx-toolbar' },
        mapSel, daysSel,
        btn('刷新', () => load({ forceFish: true }), 'alt'),
        btn('全部水域对比', () => { loadCompare().catch(() => {}); }, 'alt')),
      sumEl,
      dayCards,
      h('div', { class: 'wx-sub-head' },
        h('div', {}, h('h3', { text: '温度曲线（选中日）' }),
          h('p', { text: '气温 / 表层 / 中层 / 底层四条线；竖线为当前时刻' })),
        dayLabel),
      chartEl));

  const fishCard = h('div', { class: 'card' },
    h('div', { class: 'section-head' },
      h('div', {}, h('h2', { text: '当前活跃鱼种' }),
        h('p', { text: '昼夜曲线 × 水温响应两个分项系数相乘；口径与游戏内判鱼同一套（不含饵料与钓组）' })),
      fishStatus),
    h('div', { class: 'card-pad' }, fishSum, fishBox));

  const duo = h('div', { class: 'wx-duo' },
    card('温度区间', '全水层覆盖（表层 0 → 底钓 0.93），这是「这图能出什么温度」', bandBox),
    card('四季对比', '同一张图在四季的水温跨度，共用一根轴；高亮条＝当前季', seasonBox));

  const cmpCard = card('全部水域温度区间对比', '点一行切换上方明细', cmpBox);
  cmpCard.id = 'wxCmpCard';

  // 先同步出骨架，再异步填数据：网络慢时页面不会只剩一个转圈，
  // 也让「切到这一页」的第一帧就有完整版式。
  load();

  return [head, work([headCard, fishCard, duo, cmpCard])];
}