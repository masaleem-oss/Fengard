import { get, post, patch } from '../api.js';
import { $, $$, esc, icon, fmt, compact, pct, ago, until, duration, attempt, isSet } from '../ui.js';
import { stackedBars, bucketize, barList } from '../charts.js';
import { CATEGORY_ICONS } from '../meta.js';
import { platformTiles, wirePlatformTiles, trustStatus, trustBadge } from '../cert.js';
import { state, isAdmin, refreshShell, alertIcon } from '../main.js';
import { startUpdate, updateBanner } from '../update.js';
import { speedCard, testedAt, homeCard, scanCard } from '../home.js';

const RANGES = [[1, '24h'], [7, '7 days'], [30, '30 days']];

export async function render(el, ctx) {
  let range = Number(sessionStorage.getItem('fg-range') || 1);
  el.innerHTML = `
    <div id="d-banners" class="stack" hidden></div>
    <section class="panel stats" id="d-stats">${'<div class="stat"><div class="skeleton" style="height:48px"></div></div>'.repeat(4)}</section>
    <section class="grid g-3">
      <div class="panel"><div class="panel-head"><h3>Speed test</h3><span class="sub" id="d-net-when"></span>
        ${isAdmin() ? `<div class="actions"><button class="btn btn-ghost btn-sm" data-speedtest>Run test</button></div>` : ''}</div>
        <div id="d-net"><div class="panel-body"><div class="skeleton" style="height:150px"></div></div></div></div>
      <div class="panel"><div class="panel-head"><h3>Who's home</h3></div><div id="d-home"></div></div>
      <div class="panel"><div class="panel-head"><h3>Device check</h3></div><div id="d-scan"></div></div>
    </section>
    <section class="grid g-main">
      <div class="panel">
        <div class="panel-head"><h3>DNS traffic</h3>
          <div class="seg" id="d-range">${RANGES.map(([d, l]) => `<button data-d="${d}" class="${d === range ? 'on' : ''}">${l}</button>`).join('')}</div>
          <div class="actions legend"><span><i style="background:var(--chart-base)"></i>Allowed</span><span><i style="background:var(--accent)"></i>Blocked</span></div></div>
        <div class="panel-body"><div class="chart" id="d-chart" style="height:240px"></div></div>
      </div>
      <div class="panel"><div class="panel-head"><h3>Protection</h3></div><div class="status-list" id="d-status"></div></div>
    </section>
    <section class="grid g-3">
      <div class="panel"><div class="panel-head"><h3>Most blocked</h3><div class="actions"><a class="btn btn-ghost btn-sm" href="#activity?blocked=1">Activity</a></div></div><div id="d-blocked"></div></div>
      <div class="panel"><div class="panel-head"><h3>Most active devices</h3><div class="actions"><a class="btn btn-ghost btn-sm" href="#devices">Devices</a></div></div><div id="d-devices"></div></div>
      <div class="panel"><div class="panel-head"><h3>Blocked by category</h3></div><div id="d-cats"></div></div>
    </section>
    <section class="grid g-2">
      <div class="panel">
        <div class="panel-head"><h3>Fengard certificate</h3><span class="sub">Shows the block page on HTTPS sites</span><span class="actions" id="d-trust"></span></div>
        <div class="panel-body" id="d-platforms">${platformTiles()}</div>
      </div>
      <div class="panel"><div class="panel-head"><h3>Recent alerts</h3><div class="actions"><a class="btn btn-ghost btn-sm" href="#alerts">All alerts</a></div></div><div class="feed" id="d-alerts"></div></div>
    </section>
    <section class="panel stats" id="d-system"></section>`;
  wirePlatformTiles($('#d-platforms', el));
  trustStatus().then((t) => { const b = $('#d-trust', el); if (b) b.innerHTML = trustBadge(t); });

  let series = null, lastChart = '';
  const loadSeries = async () => {
    series = await get(`/api/series?days=${Math.max(2, range * 2)}`); // double the range for the comparison
    if (!ctx.alive()) return;
    drawChart();
  };
  const drawChart = () => {
    if (!series) return;
    const pts = series.hours.slice(-range * 24);
    const sig = JSON.stringify(pts) + document.documentElement.dataset.theme + range;
    if (sig === lastChart) return;
    lastChart = sig;
    stackedBars($('#d-chart', el), bucketize(pts, range), { height: 240 });
  };

  const draw = async () => {
    const o = await get('/api/overview');
    if (!ctx.alive()) return;
    const st = o.stats;
    const hitRate = o.dns.queries ? (o.dns.cacheHits / o.dns.queries) * 100 : 0;

    // totals for this range and the one before
    const hrs = series?.hours || [];
    const cur = hrs.slice(-range * 24), prev = hrs.slice(-range * 48, -range * 24);
    const sum = (a, k) => a.reduce((s, h) => s + h[k], 0);
    const total = sum(cur, 'total'), blocked = sum(cur, 'blocked');
    const pTotal = sum(prev, 'total'), pBlocked = sum(prev, 'blocked');
    // comparing against a period fengard wasnt running for most of gives silly numbers
    const fullPrev = prev.length && prev.filter((h) => h.total > 0).length >= prev.length * 0.75;
    const delta = (a, b) => {
      if (!b || !fullPrev) return '<span class="delta">no earlier data</span>';
      const d = ((a - b) / b) * 100;
      return `<span class="delta">${d > 0 ? '+' : ''}${d.toFixed(0)}%</span> vs. previous`;
    };
    const rangeLabel = RANGES.find(([d]) => d === range)[1];

    // banners
    const banners = [];
    if (st.droppedWrites) banners.push(`<div class="banner warn">${icon('info')}<div class="banner-text"><b>Some activity was not saved</b><p>${fmt(st.droppedWrites)} query records were dropped because logging capacity or storage was unavailable. Filtering continues, but activity history may be incomplete.</p></div></div>`);
    if (o.alertDrops) banners.push(`<div class="banner warn">${icon('bell')}<div class="banner-text"><b>Some alerts were dropped</b><p>${fmt(o.alertDrops)} alerts exceeded capacity or could not be saved. Check available storage and network request volume.</p></div><a class="btn btn-sm" href="#alerts">Alerts</a></div>`);
    const up = await get('/api/update').catch(() => null);
    const upBanner = updateBanner(up, isAdmin(), esc, icon);
    if (o.firewall.error) banners.push(`<div class="banner danger">${icon('octagon-x')}<div class="banner-text"><b>Firewall rules were not applied</b><p>${esc(o.firewall.error)}</p></div><a class="btn btn-sm" href="#firewall">Details</a></div>`);
    if (o.protection?.paused) banners.push(`<div class="banner warn">${icon('pause')}<div class="banner-text"><b>Protection is paused until ${until(o.protection.pausedUntil)}</b><p>Nothing is being filtered. Device and profile pauses still apply.</p></div>
      ${isAdmin() ? `<button class="btn btn-sm" data-resume>Resume now</button>` : ''}</div>`);
    if (o.pendingDevices.length) banners.push(`<div class="banner warn">${icon('monitor-smartphone')}<div class="banner-text">
      <b>${o.devices.pending} new device${o.devices.pending > 1 ? 's are' : ' is'} waiting for approval</b>
      <p>${o.pendingDevices.map((d) => esc(d.hostname || d.vendor || d.mac)).join(', ')}</p></div>
      ${isAdmin() && o.pendingDevices.length === 1 ? `<button class="btn btn-sm btn-primary" data-approve="${esc(o.pendingDevices[0].mac)}">Approve</button>` : ''}
      <a class="btn btn-sm" href="#devices?filter=pending">Review</a></div>`);
    if (upBanner) banners.push(upBanner);
    $('#d-banners', el).innerHTML = banners.join('');
    $('[data-update]', el)?.addEventListener('click', () => startUpdate(up, draw));
    $('#d-banners', el).hidden = !banners.length;

    const stat = (label, ic, value, sub) => `<div class="stat"><div class="stat-label">${icon(ic, 'icon-sm')}${label}</div><div class="stat-value">${value}</div><div class="stat-sub">${sub}</div></div>`;
    $('#d-stats', el).innerHTML = [
      stat(`Queries · ${rangeLabel}`, 'globe', compact(total), delta(total, pTotal)),
      stat(`Blocked · ${rangeLabel}`, 'shield-ban', compact(blocked), `${pct(blocked, total)} of queries · ${delta(blocked, pBlocked)}`),
      stat('Devices online', 'monitor-smartphone', fmt(o.devices.online), `${fmt(o.devices.known)} known${o.devices.pending ? ` · ${o.devices.pending} pending` : ''}`),
      stat('Response time', 'gauge', `${st.avgMs < 10 ? st.avgMs.toFixed(1) : Math.round(st.avgMs)} ms`, `${hitRate.toFixed(0)}% from cache`),
    ].join('');

    // protection rows
    const lists = state.categories;
    const newest = lists.map((c) => c.updated).filter(isSet).sort().pop();
    const rows = [
      o.protection?.paused ? ['warn', 'Filtering', 'Paused'] : ['ok', 'Filtering', `${compact(o.blocklist)} domains`],
      o.firewall.error ? ['bad', 'Firewall', 'Error'] : o.firewall.enabled ? ['ok', 'Firewall', `Applied ${ago(o.firewall.applied)}`] : ['off', 'Firewall', 'Off (DNS-only mode)'],
      ['ok', 'Activity history', st.evictedRecords ? 'Oldest records roll off to keep storage small' : 'Within storage limits'],
      newest ? ['ok', 'Blocklists', `Updated ${ago(newest)}`] : ['warn', 'Blocklists', 'Not downloaded yet'],
      o.dns.upstreamErrors && o.dns.servedStale ? ['warn', 'Upstream DNS', `${fmt(o.dns.upstreamErrors)} errors, serving cache`] : ['ok', 'Upstream DNS', 'Reachable'],
      o.dns.rateLimited ? ['warn', 'Flood protection', `${compact(o.dns.rateLimited)} throttled`] : ['ok', 'Flood protection', 'Quiet'],
      o.protection?.tempAllows ? ['warn', 'Temporary allows', `${o.protection.tempAllows} active`] : ['ok', 'Temporary allows', 'None'],
    ];
    $('#d-status', el).innerHTML = rows.map(([s, t, d]) => `<div class="status-row"><i class="${s}"></i><b>${t}</b><small>${esc(d)}</small></div>`).join('');

    const domainRow = (it) => `<span class="mono">${esc(it.name)}</span>`;
    $('#d-blocked', el).innerHTML = barList(st.topBlocked.slice(0, 7), { accent: true, render: domainRow, emptyText: 'Nothing blocked yet' });
    $('#d-devices', el).innerHTML = barList(st.topDevices.slice(0, 7), { emptyText: 'No devices yet' });
    $('#d-cats', el).innerHTML = barList(st.categories.slice(0, 7).map((c) => ({ ...c, label: labelFor(c.name, lists) })), {
      accent: true, emptyText: 'Nothing blocked yet',
      render: (it) => `${icon(CATEGORY_ICONS[it.name] || (it.name.startsWith('app:') ? 'smartphone' : 'list'), 'icon-sm')}<span>${esc(it.label)}</span>`,
    });

    $('#d-alerts', el).innerHTML = o.recentAlerts.length ? o.recentAlerts.map((a) => `
      <div class="feed-item"><div class="feed-ico sev-${a.severity}">${icon(alertIcon(a), 'icon-sm')}</div>
        <div class="feed-text truncate"><b class="truncate">${esc(a.title)}</b><small class="truncate">${esc(a.detail || '')}</small></div>
        <div class="feed-time">${ago(a.time)}</div></div>`).join('')
      : `<p class="t-3 t-sm" style="padding:20px 14px">No alerts yet. New devices, access requests and attacks show up here.</p>`;

    const sys = o.system;
    $('#d-system', el).innerHTML = [
      stat('CPU', 'cpu', `${sys.cpuPercent.toFixed(1)}%`, `of one core · ${sys.cores} available`),
      stat('Memory', 'memory-stick', `${sys.heapMB.toFixed(1)} MB`, `${sys.memoryMB.toFixed(0)} MB reserved`),
      stat('Uptime', 'clock', duration(sys.uptimeSec), `${esc(sys.platform)} · Fengard ${esc(sys.appVersion)}`),
      stat('DNS cache', 'database', fmt(o.dns.cacheSize), `${hitRate.toFixed(0)}% hit rate`),
    ].join('');
  };

  $('#d-range', el).addEventListener('click', async (e) => {
    const b = e.target.closest('[data-d]');
    if (!b) return;
    range = Number(b.dataset.d);
    sessionStorage.setItem('fg-range', range);
    $$('#d-range button', el).forEach((x) => x.classList.toggle('on', x === b));
    await loadSeries();
    draw();
  });
  const showSpeed = (net) => {
    $('#d-net', el).innerHTML = speedCard(net);
    $('#d-net-when', el).textContent = testedAt(net);
    const run = $('[data-speedtest]', el);
    if (run) run.disabled = !net?.available || net.testing || !net.online;
  };
  // a running test refreshes quickly so ping and speed tick along live
  let following = false;
  const followTest = async () => {
    if (following) return;
    following = true;
    while (ctx.alive()) {
      await new Promise((r) => setTimeout(r, 700));
      const net = await get('/api/internet').catch(() => null);
      if (!ctx.alive()) break;
      showSpeed(net);
      if (!net?.testing) break;
    }
    following = false;
  };

  // the new cards refresh on their own so a slow call doesnt hold up the rest
  const drawHome = async () => {
    const [net, who, scan] = await Promise.all(['/api/internet', '/api/presence', '/api/scan'].map((p) => get(p).catch(() => null)));
    if (!ctx.alive()) return;
    showSpeed(net);
    if (net?.testing) followTest();
    $('#d-home', el).innerHTML = homeCard(who);
    // keep any finding the user opened open across refreshes
    const open = $$('#d-scan details[open] summary b', el).map((b) => b.textContent);
    $('#d-scan', el).innerHTML = scanCard(scan, isAdmin());
    $$('#d-scan details', el).forEach((d) => { if (open.includes($('summary b', d).textContent)) d.open = true; });
  };

  el.addEventListener('click', async (e) => {
    if (e.target.closest('[data-speedtest]') && await attempt(() => post('/api/internet/speedtest'), 'Speed test started')) drawHome();
    if (e.target.closest('[data-scan]') && await attempt(() => post('/api/scan'), 'Checking your devices')) setTimeout(drawHome, 500);
    const a = e.target.closest('[data-approve]');
    if (a && await attempt(() => patch(`/api/devices/${encodeURIComponent(a.dataset.approve)}`, { approved: true }), 'Device approved')) { refreshShell(); draw(); }
    if (e.target.closest('[data-resume]') && await attempt(() => post('/api/protection/pause', { minutes: 0 }), 'Protection resumed')) { refreshShell(); draw(); }
  });

  if (!Object.keys(appNames).length) {
    try { appNames = Object.fromEntries((await get('/api/apps')).apps.map((a) => [a.id, a.name])); } catch (e) {}
  }
  drawHome();
  await loadSeries();
  await draw();
  ctx.every(5000, draw);
  ctx.every(3000, drawHome);
  ctx.every(60000, loadSeries);
}

// real app names like TikTok instead of guessing from the id
let appNames = {};

function labelFor(id, lists) {
  if (id.startsWith('app:')) return appNames[id.slice(4)] || id.slice(4).replace(/^\w/, (c) => c.toUpperCase());
  if (id.startsWith('list:')) return 'Custom list';
  return lists.find((l) => l.id === id)?.name || id;
}
