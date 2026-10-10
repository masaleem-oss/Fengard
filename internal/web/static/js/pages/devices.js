import { get, patch, post, del } from '../api.js';
import { $, $$, esc, icon, fmt, ago, until, dateTime, timeOf, inFuture, isSet, attempt, menu, drawer, confirmDialog, empty, statusBadge, switchInput } from '../ui.js';
import { barList } from '../charts.js';
import { deviceType, deviceName } from '../meta.js';
import { isAdmin, refreshShell } from '../main.js';
import { bytes, mbps, findingsFor } from '../home.js';

const FILTERS = [['all', 'All'], ['online', 'Online'], ['pending', 'Pending'], ['restricted', 'Paused / blocked']];

export async function render(el, ctx) {
  let devices = [], groups = [];
  let filter = ctx.params.get('filter') || 'all';
  let search = '', group = '';

  el.innerHTML = `
    <div class="panel">
      <div class="toolbar">
        <div class="input-wrap">${icon('search')}<input class="input input-sm" id="q" placeholder="Name, IP, MAC or vendor" aria-label="Search devices"></div>
        <div class="seg" id="filters">${FILTERS.map(([id, l]) => `<button data-f="${id}" class="${id === filter ? 'on' : ''}">${l}</button>`).join('')}</div>
        <select class="select select-sm" id="group" style="width:auto" aria-label="Filter by profile"><option value="">All profiles</option></select>
        <span class="spacer"></span><span class="t-sm t-3" id="count"></span>
      </div>
      <div class="table-wrap" id="table"></div>
    </div>`;

  const load = async () => {
    [devices, groups] = await Promise.all([get('/api/devices'), get('/api/groups')]);
    if (!ctx.alive()) return;
    const sel = $('#group', el);
    if (sel.options.length === 1) sel.insertAdjacentHTML('beforeend', groups.map((g) => `<option value="${g.id}">${esc(g.name)}</option>`).join(''));
    draw();
  };

  const matches = (d) => {
    if (filter === 'online' && !d.online) return false;
    if (filter === 'pending' && d.approved) return false;
    if (filter === 'restricted' && d.status !== 'paused' && d.status !== 'quarantined') return false;
    if (group && d.group !== group) return false;
    if (search) {
      const hay = `${deviceName(d)} ${d.hostname || ''} ${d.mac} ${(d.ips || []).join(' ')} ${d.vendor || ''}`.toLowerCase();
      if (!hay.includes(search)) return false;
    }
    return true;
  };
  const groupName = (id) => groups.find((g) => g.id === id)?.name || id;

  const draw = () => {
    const rows = devices.filter(matches);
    $('#count', el).textContent = `${rows.length} of ${devices.length}`;
    if (!devices.length) {
      $('#table', el).innerHTML = empty({ icon: 'monitor-smartphone', title: 'No devices yet', text: 'Devices appear here as soon as they use the network.' });
      return;
    }
    if (!rows.length) {
      $('#table', el).innerHTML = empty({ icon: 'search', title: 'No matching devices' });
      return;
    }
    $('#table', el).innerHTML = `<table class="table">
      <thead><tr><th>Device</th><th>Profile</th><th>IP address</th><th>MAC address</th><th>Last seen</th><th>Status</th><th class="col-actions" aria-label="Actions"></th></tr></thead>
      <tbody>${rows.map((d) => {
        const t = deviceType(d);
        return `<tr class="clickable" data-mac="${esc(d.mac)}" tabindex="0">
          <td><div class="cell-main"><span class="dev-icon">${icon(t.icon)}</span>
            <div class="truncate"><div class="cell-title truncate">${esc(deviceName(d))}</div>
            <div class="cell-sub truncate">${esc([d.vendor, d.hostname && d.hostname !== deviceName(d) ? d.hostname : ''].filter(Boolean).join(' · ') || t.label)}</div></div></div></td>
          <td><span class="tag">${esc(groupName(d.group))}</span></td>
          <td class="mono">${esc((d.ips || [])[0] || '–')}${(d.ips || []).length > 1 ? ` <span class="t-3">+${d.ips.length - 1}</span>` : ''}</td>
          <td class="mono t-2">${esc(d.mac)}${d.randomized ? ' <span class="tag" title="Randomized Wi-Fi address">Private</span>' : ''}</td>
          <td class="t-2">${d.online ? '<span class="dot-on"></span>Online' : `<span class="dot-off"></span>${ago(d.lastSeen)}`}</td>
          <td>${statusBadge(d.status)}${inFuture(d.pausedUntil) ? `<div class="cell-sub">until ${until(d.pausedUntil)}</div>` : ''}</td>
          <td class="col-actions">${isAdmin() ? `<button class="icon-btn" data-actions aria-label="Actions for ${esc(deviceName(d))}" data-menu-anchor>${icon('ellipsis')}</button>` : ''}</td>
        </tr>`;
      }).join('')}</tbody></table>`;
  };

  $('#q', el).addEventListener('input', (e) => { search = e.target.value.trim().toLowerCase(); draw(); });
  $('#group', el).addEventListener('change', (e) => { group = e.target.value; draw(); });
  $('#filters', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-f]');
    if (!b) return;
    filter = b.dataset.f;
    $$('#filters button', el).forEach((x) => x.classList.toggle('on', x === b));
    draw();
  });
  $('#table', el).addEventListener('click', (e) => {
    const row = e.target.closest('tr[data-mac]');
    if (!row) return;
    const d = devices.find((x) => x.mac === row.dataset.mac);
    if (e.target.closest('[data-actions]')) {
      e.stopPropagation();
      menu(e.target.closest('[data-actions]'), deviceActions(d, groups, load));
    } else openDevice(d.mac, { onChange: load });
  });
  $('#table', el).addEventListener('keydown', (e) => {
    const row = e.target.closest('tr[data-mac]');
    if (row && e.key === 'Enter' && e.target === row) openDevice(row.dataset.mac, { onChange: load });
  });

  await load();
  if (ctx.params.get('mac')) openDevice(ctx.params.get('mac'), { onChange: load });
  ctx.every(10000, load);
}

function minutesUntilMorning() {
  const t = new Date();
  t.setHours(7, 0, 0, 0);
  if (t <= new Date()) t.setDate(t.getDate() + 1);
  return Math.ceil((t - Date.now()) / 60000);
}

export function deviceActions(d, groups, onChange) {
  const path = `/api/devices/${encodeURIComponent(d.mac)}`;
  const run = async (fn, ok) => { if (await attempt(fn, ok)) { onChange?.(); refreshShell(); } };
  const pause = (m, label) => ({ label, icon: 'pause', onClick: () => run(() => post(path + '/pause', { minutes: m }), `${deviceName(d)} paused`) });
  if (!d.approved) {
    return [
      { label: 'Approve', icon: 'check', onClick: () => run(() => patch(path, { approved: true }), 'Device approved') },
      'divider',
      { label: 'Forget device', icon: 'trash-2', danger: true, onClick: () => forget(d, onChange) },
    ];
  }
  return [
    ...(inFuture(d.pausedUntil)
      ? [{ label: 'Resume internet', icon: 'play', onClick: () => run(() => post(path + '/pause', { minutes: 0 }), 'Internet resumed') }]
      : [{ header: 'Pause internet' }, pause(30, 'For 30 minutes'), pause(60, 'For 1 hour'), pause(minutesUntilMorning(), 'Until 7:00 tomorrow')]),
    'divider',
    { header: 'Move to profile' },
    ...groups.filter((g) => g.id !== d.group).map((g) => ({ label: g.name, icon: 'users', onClick: () => run(() => patch(path, { group: g.id }), `Moved to ${g.name}`) })),
    'divider',
    { label: 'Check a site for this device', icon: 'scan-search', onClick: () => (location.hash = `#filtering?mac=${encodeURIComponent(d.mac)}`) },
    { label: 'Wake up (Wake-on-LAN)', icon: 'power', onClick: () => attempt(() => post(path + '/wake'), 'Wake-up packet sent') },
    { label: 'Block (require approval)', icon: 'ban', onClick: () => run(() => patch(path, { approved: false }), 'Device blocked') },
    { label: 'Forget device', icon: 'trash-2', danger: true, onClick: () => forget(d, onChange) },
  ];
}

async function forget(d, onChange) {
  const ok = await confirmDialog({
    title: 'Forget this device?', danger: true, confirm: 'Forget device',
    message: `<b>${esc(deviceName(d))}</b> will be removed. If it connects again it's treated as a new device.`,
  });
  if (ok && await attempt(() => del(`/api/devices/${encodeURIComponent(d.mac)}`), 'Device removed')) {
    onChange?.();
    refreshShell();
    document.querySelector('.drawer [data-close]')?.click();
  }
}

export async function openDevice(mac, { onChange } = {}) {
  const [devices, groups] = await Promise.all([get('/api/devices'), get('/api/groups')]);
  const d = devices.find((x) => x.mac === mac);
  if (!d) return;
  const t = deviceType(d);
  const path = `/api/devices/${encodeURIComponent(mac)}`;
  const admin = isAdmin();

  const dr = drawer(`
    <div class="drawer-head">
      <span class="dev-icon lg">${icon(t.icon)}</span>
      <div class="spacer" style="min-width:0">
        ${admin ? `<input class="inline-edit" id="dv-name" value="${esc(d.name)}" placeholder="${esc(deviceName(d))}" aria-label="Device name" style="font-size:15px;width:100%">`
          : `<b style="font-size:15px">${esc(deviceName(d))}</b>`}
        <div class="t-xs t-3">${esc([t.label, d.vendor].filter(Boolean).join(' · '))}</div>
      </div>
      ${admin ? `<button class="icon-btn" id="dv-more" aria-label="More actions" data-menu-anchor>${icon('ellipsis')}</button>` : ''}
      <button class="icon-btn" data-close aria-label="Close">${icon('x')}</button>
    </div>
    <div class="drawer-body">
      <div class="section">
        <div class="row wrap" style="margin-bottom:${admin ? '12px' : '0'}">
          ${statusBadge(d.status)}
          ${d.online ? '<span class="badge b-success">Online</span>' : `<span class="badge b-neutral">Last seen ${ago(d.lastSeen)}</span>`}
          ${inFuture(d.pausedUntil) ? `<span class="t-xs t-3">Paused until ${until(d.pausedUntil)}</span>` : ''}
        </div>
        ${admin ? `<div class="row wrap">
          ${!d.approved ? `<button class="btn btn-primary btn-sm" data-act="approve">${icon('check', 'icon-sm')}Approve device</button>`
            : inFuture(d.pausedUntil) ? `<button class="btn btn-primary btn-sm" data-act="resume">${icon('play', 'icon-sm')}Resume internet</button>`
            : `<button class="btn btn-sm" data-act="pause" data-menu-anchor>${icon('pause', 'icon-sm')}Pause internet${icon('chevron-down', 'icon-sm')}</button>`}
          <a class="btn btn-sm" href="#filtering?mac=${encodeURIComponent(mac)}" data-close>${icon('scan-search', 'icon-sm')}Check a site</a>
        </div>` : ''}
      </div>
      <div class="section">
        <h4>Details</h4>
        <dl class="kv">
          <dt>Profile</dt><dd>${admin ? `<select class="select select-sm" id="dv-group" style="width:auto">${groups.map((g) => `<option value="${g.id}" ${g.id === d.group ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}</select>` : esc(groups.find((g) => g.id === d.group)?.name || d.group)}</dd>
          <dt>Arrival alerts</dt><dd>${admin ? switchInput('id="dv-presence"', d.presence, 'Arrival alerts') : d.presence ? 'On' : 'Off'}</dd>
          <dt>Data today</dt><dd id="dv-data" class="t-3">–</dd>
          <dt>IP address</dt><dd class="mono">${esc((d.ips || []).join(', ') || '–')}</dd>
          <dt>MAC address</dt><dd class="mono">${esc(d.mac)}${d.randomized ? ' <span class="tag">Private address</span>' : ''}</dd>
          <dt>Manufacturer</dt><dd>${esc(d.vendor || (d.randomized ? 'Hidden (private address)' : 'Unknown'))}</dd>
          <dt>Hostname</dt><dd class="mono">${esc(d.hostname || '–')}</dd>
          <dt>First seen</dt><dd>${isSet(d.firstSeen) ? dateTime(d.firstSeen) : '–'}</dd>
        </dl>
      </div>
      <div id="dv-risks"></div>
      <div id="dv-summary"><div class="section"><h4>Last 24 hours</h4><div class="skeleton" style="height:100px"></div></div></div>
    </div>`, { label: deviceName(d) });

  const changed = () => { onChange?.(); refreshShell(); };
  const el = dr.el;
  $('#dv-name', el)?.addEventListener('change', async (e) => {
    if (await attempt(() => patch(path, { name: e.target.value.trim() }), 'Device renamed')) changed();
  });
  $('#dv-group', el)?.addEventListener('change', async (e) => {
    if (await attempt(() => patch(path, { group: e.target.value }), 'Profile changed')) changed();
  });
  $('#dv-presence', el)?.addEventListener('change', async (e) => {
    const on = e.target.checked;
    if (await attempt(() => patch(path, { presence: on }), on ? 'You\'ll get a message when it arrives or leaves' : 'Arrival alerts off')) changed();
    else e.target.checked = !on;
  });
  Promise.all([get('/api/traffic').catch(() => null), get('/api/scan').catch(() => null)]).then(([t, s]) => {
    if (!el.isConnected) return;
    const use = t?.devices?.find((x) => x.mac === mac);
    if (t?.available) $('#dv-data', el).innerHTML = use ? `${bytes(use.today.down)} down · ${bytes(use.today.up)} up${use.downRate + use.upRate > 0 ? ` <span class="t-3">· ${mbps(use.downRate)} now</span>` : ''}` : 'None yet';
    const risks = findingsFor(s, mac);
    if (risks.length) $('#dv-risks', el).innerHTML = `<div class="section"><h4>Risky settings</h4><div class="findings">${risks.map((f) => `
      <details class="finding" open><summary><i class="${f.severity === 'high' ? 'bad' : f.severity === 'medium' ? 'warn' : 'off'}"></i><b>${esc(f.title)}</b><small>port ${f.port}</small></summary><p>${esc(f.detail)}</p></details>`).join('')}</div></div>`;
  });
  $('#dv-more', el)?.addEventListener('click', (e) => menu(e.currentTarget, deviceActions(d, groups, () => { changed(); dr.close(); })));
  el.addEventListener('click', async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const reopen = async () => { changed(); dr.close(); openDevice(mac, { onChange }); };
    if (b.dataset.act === 'approve' && await attempt(() => patch(path, { approved: true }), 'Device approved')) reopen();
    if (b.dataset.act === 'resume' && await attempt(() => post(path + '/pause', { minutes: 0 }), 'Internet resumed')) reopen();
    if (b.dataset.act === 'pause') {
      menu(b, [30, 60, 120].map((m) => ({ label: m < 60 ? `${m} minutes` : `${m / 60} hour${m > 60 ? 's' : ''}`, icon: 'clock',
        onClick: async () => { if (await attempt(() => post(path + '/pause', { minutes: m }), 'Internet paused')) reopen(); } }))
        .concat([{ label: 'Until 7:00 tomorrow', icon: 'moon', onClick: async () => { if (await attempt(() => post(path + '/pause', { minutes: minutesUntilMorning() }), 'Internet paused')) reopen(); } }]), { align: 'left' });
    }
  });

  try {
    const s = await get(`${path}/summary`);
    if (!el.isConnected) return;
    const mono = (i) => `<span class="mono">${esc(i.name)}</span>`;
    $('#dv-summary', el).innerHTML = `
      <div class="section">
        <h4>Last 24 hours</h4>
        <div class="row" style="gap:24px"><div><div class="stat-value">${fmt(s.total)}${s.sampled ? '+' : ''}</div><div class="t-xs t-3">queries</div></div>
          <div><div class="stat-value t-accent">${fmt(s.blocked)}</div><div class="t-xs t-3">blocked</div></div></div>
      </div>
      <div class="section" style="padding:0"><div class="panel-head" style="border:0"><h3>Most visited</h3></div>${barList(s.topDomains, { render: mono, emptyText: 'No activity in the last 24 hours' })}</div>
      ${s.topBlocked.length ? `<div class="section" style="padding:0"><div class="panel-head" style="border:0"><h3>Most blocked</h3></div>${barList(s.topBlocked, { accent: true, render: mono })}</div>` : ''}
      <div class="section" style="padding:0"><div class="panel-head" style="border:0"><h3>Recent activity</h3><div class="actions"><a class="btn btn-ghost btn-sm" href="#activity?mac=${encodeURIComponent(mac)}" data-close>All activity</a></div></div>
        ${s.recent.length ? `<table class="table dense"><tbody>${s.recent.slice(0, 12).map((r) => `<tr>
          <td class="t-3 num" style="width:1%;white-space:nowrap">${timeOf(r.time)}</td>
          <td class="mono truncate" style="max-width:220px" title="${esc(r.domain)}">${esc(r.domain)}</td>
          <td style="text-align:right">${statusBadge(r.action)}</td></tr>`).join('')}</tbody></table>`
          : `<p class="t-3 t-sm" style="padding:0 14px 14px">No activity recorded yet.</p>`}</div>`;
  } catch (e) {
    $('#dv-summary', el).innerHTML = `<p class="t-danger t-sm" style="padding:14px">${esc(e.message)}</p>`;
  }
}
