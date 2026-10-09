import { get, post } from '../api.js';
import { $, $$, esc, icon, timeOf, dateShort, attempt, menu, empty, statusBadge, copyText, download, switchInput } from '../ui.js';
import { deviceName } from '../meta.js';
import { isAdmin } from '../main.js';

export async function render(el, ctx) {
  const devices = await get('/api/devices').catch(() => []);
  if (!ctx.alive()) return;
  const p = ctx.params;
  const f = { mac: p.get('mac') || '', search: p.get('search') || '', status: p.get('blocked') ? 'blocked' : 'all', live: true };
  let rows = [], stopLive = () => {}, loadingMore = false;

  el.innerHTML = `
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

  restart();
}
