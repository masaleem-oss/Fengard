import { get, post } from '../api.js';
import { $, $$, esc, icon, timeOf, dateShort, attempt, menu, empty, statusBadge, copyText, download, switchInput, modal } from '../ui.js';
import { stackedBars } from '../charts.js';
import { deviceName } from '../meta.js';
import { isAdmin } from '../main.js';
import { mbps, bytes } from '../home.js';

export async function render(el, ctx) {
  const devices = await get('/api/devices').catch(() => []);
  if (!ctx.alive()) return;
  const p = ctx.params;
  const f = { mac: p.get('mac') || '', search: p.get('search') || '', status: p.get('blocked') ? 'blocked' : 'all', live: true };
  let rows = [], stopLive = () => {}, loadingMore = false;

  el.innerHTML = `
    <section class="panel stats stats-3" id="bw" hidden></section>
    <div class="panel">
      <div class="toolbar">
        <div class="input-wrap">${icon('search')}<input class="input input-sm" id="search" placeholder="Filter by domain" value="${esc(f.search)}" spellcheck="false" aria-label="Filter by domain"></div>
        <select class="select select-sm" id="mac" style="width:auto;max-width:220px" aria-label="Device">
          <option value="">All devices</option>
          ${devices.map((d) => `<option value="${esc(d.mac)}" ${d.mac === f.mac ? 'selected' : ''}>${esc(deviceName(d))}</option>`).join('')}</select>
        <div class="seg" id="status">${[['all', 'All'], ['blocked', 'Blocked'], ['allowed', 'Allowed']].map(([id, l]) => `<button data-s="${id}" class="${f.status === id ? 'on' : ''}">${l}</button>`).join('')}</div>
        <span class="spacer"></span>
        <label class="row t-sm t-2">${switchInput('id="live"', true, 'Live updates')}<span id="live-label"><span class="live"></span> Live</span></label>
        <button class="btn btn-sm" id="export">${icon('download', 'icon-sm')}CSV</button>
      </div>
      <div class="table-wrap scroll-y" style="max-height:calc(100vh - 180px)" id="tbl"></div>
      <div class="panel-foot" id="foot" hidden><button class="btn btn-sm" id="older">Load older</button><span class="t-xs t-3" id="range"></span></div>
    </div>`;

  const params = (before) => {
    const q = new URLSearchParams({ limit: '200' });
    if (f.mac) q.set('mac', f.mac);
    if (f.search) q.set('search', f.search);
    if (f.status === 'blocked') q.set('blocked', '1');
    if (f.live && !before) q.set('source', 'recent');
    if (before) q.set('before', before);
    return q;
  };
  const visible = (list) => (f.status === 'allowed' ? list.filter((r) => r.action === 'allowed' || r.action === 'safesearch') : list);

  const draw = () => {
    const list = visible(rows);
    if (!list.length) {
      $('#tbl', el).innerHTML = empty({ icon: 'activity', title: 'No matching activity', text: f.live ? 'New lookups appear here as they happen.' : 'Try a different filter.' });
    } else {
      $('#tbl', el).innerHTML = `<table class="table dense"><thead><tr>
        <th>Time</th><th>Device</th><th>Domain</th><th>Type</th><th>Result</th><th>Reason</th><th style="text-align:right">Response</th><th class="col-actions" aria-label="Actions"></th></tr></thead>
        <tbody>${list.map((r, i) => `<tr data-i="${i}">
          <td class="t-3 num" style="white-space:nowrap" title="${esc(dateShort(r.time))}">${timeOf(r.time)}</td>
          <td class="truncate" style="max-width:170px" title="${esc(r.client)}">${esc(r.device || r.client)}</td>
          <td class="mono truncate" style="max-width:320px" title="${esc(r.domain)}">${esc(r.domain)}</td>
          <td class="t-3 t-xs">${esc(r.type)}</td>
          <td>${statusBadge(r.action)}</td>
          <td class="t-2 t-sm truncate" style="max-width:200px">${esc(r.reason || (r.cached ? 'Cached' : ''))}</td>
          <td class="t-3 num t-sm" style="text-align:right">${r.ms < 1 ? '<1' : Math.round(r.ms)} ms</td>
          <td class="col-actions"><button class="icon-btn row-actions" data-menu aria-label="Actions" data-menu-anchor>${icon('ellipsis')}</button></td>
        </tr>`).join('')}</tbody></table>`;
    }
    $('#foot', el).hidden = f.live || rows.length < 200;
    if (rows.length) $('#range', el).textContent = `${visible(rows).length} lookups since ${timeOf(rows[rows.length - 1].time)}`;
  };

  const load = async () => {
    const data = await get('/api/queries?' + params());
    if (!ctx.alive()) return;
    rows = data;
    draw();
  };
  const restart = () => {
    stopLive();
    load().catch((e) => ($('#tbl', el).innerHTML = `<p class="t-danger" style="padding:14px">${esc(e.message)}</p>`));
    if (f.live) stopLive = ctx.every(2500, load);
  };

  let t;
  $('#search', el).addEventListener('input', (e) => { clearTimeout(t); t = setTimeout(() => { f.search = e.target.value.trim(); restart(); }, 250); });
  $('#mac', el).addEventListener('change', (e) => { f.mac = e.target.value; restart(); });
  $('#status', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-s]');
    if (!b) return;
    f.status = b.dataset.s;
    $$('#status button', el).forEach((x) => x.classList.toggle('on', x === b));
    restart();
  });
  $('#live', el).addEventListener('change', (e) => {
    f.live = e.target.checked;
    $('#live-label', el).innerHTML = f.live ? '<span class="live"></span> Live' : 'Paused';
    restart();
  });
  $('#older', el).addEventListener('click', async (e) => {
    if (loadingMore || !rows.length) return;
    loadingMore = true;
    try {
      const more = await get('/api/queries?' + params(rows[rows.length - 1].time));
      rows = rows.concat(more);
      draw();
      if (more.length < 200) e.target.hidden = true;
    } finally { loadingMore = false; }
  });
  $('#tbl', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-menu]');
    if (!b) return;
    const r = visible(rows)[+b.closest('tr').dataset.i];
    const items = [
      { label: 'Copy domain', icon: 'copy', onClick: () => copyText(r.domain) },
      { label: 'Why allowed or blocked?', icon: 'scan-search', onClick: () => (location.hash = `#filtering?check=${encodeURIComponent(r.domain)}${r.mac ? '&mac=' + encodeURIComponent(r.mac) : ''}`) },
      ...(r.mac ? [{ label: 'Only this device', icon: 'funnel', onClick: () => { f.mac = r.mac; $('#mac', el).value = r.mac; restart(); } }] : []),
    ];
    if (isAdmin()) {
      items.push('divider', r.action === 'allowed'
        ? { label: `Block ${r.domain} for everyone`, icon: 'ban', onClick: () => attempt(() => post('/api/rules', { kind: 'block', domain: r.domain }), `Blocked ${r.domain}`) }
        : { label: `Allow ${r.domain} for everyone`, icon: 'check', onClick: () => attempt(() => post('/api/rules', { kind: 'allow', domain: r.domain }), `Allowed ${r.domain}`) });
    }
    menu(b, items);
  });
  $('#export', el).addEventListener('click', () => {
    const list = visible(rows);
    const q = (v) => `"${String(v ?? '').replace(/"/g, '""')}"`;
    const csv = ['time,device,client,mac,domain,type,status,reason,ms', ...list.map((r) => [r.time, r.device, r.client, r.mac, r.domain, r.type, r.action, r.reason, r.ms].map(q).join(','))].join('\n');
    download(`fengard-activity-${new Date().toISOString().slice(0, 10)}.csv`, csv, 'text/csv');
  });

  // bandwidth for the whole house, or the picked device
  const bandwidth = async () => {
    const t = await get('/api/traffic').catch(() => null);
    const bw = $('#bw', el);
    if (!bw || !t?.available) return;
    const d = f.mac ? t.devices.find((x) => x.mac === f.mac) || { downRate: 0, upRate: 0, today: { down: 0, up: 0 } } : t;
    const stat = (label, ic, value, sub, tag = 'div') => `<${tag} class="stat"${tag === 'button' ? ' type="button" id="usage-open"' : ''}><div class="stat-label">${icon(ic, 'icon-sm')}${label}</div><div class="stat-value">${value}</div><div class="stat-sub">${sub}</div></${tag}>`;
    bw.innerHTML = [
      stat('Download now', 'arrow-down', mbps(d.downRate), f.mac ? 'this device' : 'whole network'),
      stat('Upload now', 'arrow-up', mbps(d.upRate), f.mac ? 'this device' : 'whole network'),
      stat('Used today', 'chart-column', bytes(d.today.down + d.today.up), `${bytes(d.today.down)} down · ${bytes(d.today.up)} up`, 'button'),
    ].join('');
    $('#usage-open', bw).onclick = () => usage(f.mac);
    bw.hidden = false;
  };
  bandwidth();
  ctx.every(3000, bandwidth);

  restart();
}

// data used per device and per day, for any range the router still remembers
const day = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
const RANGES = [
  { k: 'today', name: 'Today', from: () => new Date() },
  { k: 'week', name: '7 days', from: () => new Date(Date.now() - 6 * 864e5) },
  { k: 'month', name: '30 days', from: () => new Date(Date.now() - 29 * 864e5) },
  { k: 'this', name: 'This month', from: () => new Date(new Date().getFullYear(), new Date().getMonth(), 1) },
  { k: 'custom', name: 'Custom' },
];

function usage(mac) {
  let range = 'today';
  const m = modal({ title: 'Data used', wide: true, subtitle: 'Internet traffic per device. The router keeps about a year of history.',
    body: `<div class="row wrap" style="gap:8px;align-items:center">
        <div class="seg" id="u-range">${RANGES.map((r) => `<button type="button" data-k="${r.k}" class="${r.k === range ? 'on' : ''}">${r.name}</button>`).join('')}</div>
        <span id="u-custom" class="row" style="gap:6px" hidden><input class="input input-sm" type="date" id="u-from" aria-label="From"><span class="t-3">to</span><input class="input input-sm" type="date" id="u-to" aria-label="To"></span>
      </div>
      <div class="stat-value" id="u-total" style="margin-top:14px"></div><div class="stat-sub" id="u-split"></div>
      <div id="u-chart" class="chart" style="margin-top:10px" hidden></div>
      <div id="u-devs" style="margin-top:12px"></div>`,
    foot: '<button class="btn" data-close>Close</button>' });
  const from = $('#u-from', m.el), to = $('#u-to', m.el);
  to.value = day(new Date());
  from.value = day(RANGES[2].from());
  const load = async () => {
    const r = RANGES.find((x) => x.k === range);
    const a = r.from ? day(r.from()) : from.value, b = r.from ? day(new Date()) : to.value;
    if (!a || !b) return;
    const u = await get(`/api/traffic/usage?from=${a}&to=${b}`).catch(() => null);
    if (!u || !m.el.isConnected) return;
    const devs = mac ? u.devices.filter((d) => d.mac === mac) : u.devices;
    const down = devs.reduce((s, d) => s + d.down, 0), up = devs.reduce((s, d) => s + d.up, 0);
    $('#u-total', m.el).textContent = bytes(down + up);
    $('#u-split', m.el).textContent = `${bytes(down)} down · ${bytes(up)} up${mac ? ' · this device' : ''}`;
    // one bar per day, upload stacked on top
    const chart = $('#u-chart', m.el);
    chart.hidden = mac || u.days.length < 2;
    if (!chart.hidden) {
      stackedBars(chart, u.days.map((d) => ({ time: d.day + 'T00:00', total: d.down + d.up, blocked: d.up, span: 'day' })),
        { height: 180, names: ['Download', 'Upload'], value: bytes, tick: bytes, title: 'Data used per day' });
    }
    const total = Math.max(1, down + up);
    $('#u-devs', m.el).innerHTML = devs.length ? `<table class="table dense"><thead><tr><th>Device</th><th style="text-align:right">Down</th><th style="text-align:right">Up</th><th style="text-align:right">Total</th><th style="width:22%">Share</th></tr></thead>
      <tbody>${devs.map((d) => `<tr><td><div class="cell-title">${esc(d.name || d.mac)}</div>${d.vendor ? `<div class="cell-sub">${esc(d.vendor)}</div>` : ''}</td>
        <td class="num t-2" style="text-align:right">${bytes(d.down)}</td><td class="num t-2" style="text-align:right">${bytes(d.up)}</td>
        <td class="num" style="text-align:right"><b>${bytes(d.down + d.up)}</b></td>
        <td><div class="bar-list"><div class="bar-item"><div class="bar-fill" style="width:${Math.max(2, ((d.down + d.up) / total) * 100)}%"></div><div class="name t-xs">${Math.round(((d.down + d.up) / total) * 100)}%</div></div></div></td></tr>`).join('')}</tbody></table>`
      : `<p class="t-3 t-sm" style="padding:16px 0">No traffic in this range.</p>`;
  };
  $('#u-range', m.el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-k]');
    if (!b) return;
    range = b.dataset.k;
    $$('#u-range button', m.el).forEach((x) => x.classList.toggle('on', x === b));
    $('#u-custom', m.el).hidden = range !== 'custom';
    load();
  });
  from.addEventListener('change', load);
  to.addEventListener('change', load);
  load();
}
