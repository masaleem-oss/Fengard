import { get, put, post, del } from '../api.js';
import { $, $$, esc, icon, compact, until, inFuture, attempt, menu, modal, confirmDialog, empty, switchInput } from '../ui.js';
import { CATEGORY_ICONS, deviceType, deviceName } from '../meta.js';
import { state, isAdmin, refreshShell } from '../main.js';

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const WEEK = [1, 2, 3, 4, 5, 6, 0]; // monday first

export async function render(el, ctx) {
  let groups = [], devices = [], settings = {}, appsCatalog = null;
  let current = ctx.params.get('id');
  let tab = ctx.params.get('tab') || 'filters';

  el.innerHTML = `
    <div class="page-head"><h2>Profiles</h2>
      ${isAdmin() ? `<div class="actions"><button class="btn btn-primary" id="new">${icon('plus')}New profile</button></div>` : ''}</div>
    <div class="split">
      <div class="panel"><div class="side-list" id="list"></div></div>
      <div class="panel" id="editor"></div>
    </div>`;

  const load = async () => {
    [groups, devices, settings] = await Promise.all([get('/api/groups'), get('/api/devices'), get('/api/settings')]);
    if (!ctx.alive()) return;
    if (!groups.find((g) => g.id === current)) current = groups[0]?.id;
    drawList();
    drawEditor();
  };

  const scheduleActive = (g) => (g.schedules || []).some((s) => s.enabled && activeNow(s));

  const drawList = () => {
    $('#list', el).innerHTML = groups.map((g) => `
      <button class="side-item ${g.id === current ? 'on' : ''}" data-id="${g.id}">
        <span class="dev-icon">${icon(g.id === 'kids' ? 'user' : g.id === 'iot' ? 'house' : 'users')}</span>
        <span class="truncate spacer"><b class="truncate">${esc(g.name)}</b>
          <small>${g.devices} device${g.devices === 1 ? '' : 's'}${g.default ? ' · default' : ''}</small></span>
        ${inFuture(g.pausedUntil) ? `<span class="badge b-warning">${icon('pause', 'icon-sm')}</span>`
          : scheduleActive(g) ? `<span class="badge b-accent" title="A schedule is active">${icon('clock', 'icon-sm')}</span>` : ''}
      </button>`).join('');
  };

  const save = async (mod, msg) => {
    const g = groups.find((x) => x.id === current);
    const { devices: _, default: __, ...body } = structuredClone(g);
    mod(body);
    if (await attempt(() => put(`/api/groups/${g.id}`, body), msg)) await load();
  };

  const drawEditor = async () => {
    const g = groups.find((x) => x.id === current);
    const ed = $('#editor', el);
    if (!g) { ed.innerHTML = empty({ icon: 'users', title: 'No profiles' }); return; }
    const admin = isAdmin();
    const ro = admin ? '' : 'disabled';
    const members = devices.filter((d) => d.group === g.id);

    ed.innerHTML = `
      <div class="panel-head" style="min-height:54px">
        <div class="spacer" style="min-width:0">
          ${admin ? `<input class="inline-edit" id="g-name" value="${esc(g.name)}" maxlength="40" aria-label="Profile name" style="font-size:15px;max-width:320px">` : `<b style="font-size:15px">${esc(g.name)}</b>`}
          <div class="t-xs t-3">${members.length} device${members.length === 1 ? '' : 's'}${g.default ? ' · new devices join this profile' : ''}</div></div>
        ${admin ? `<div class="actions">
          ${inFuture(g.pausedUntil) ? `<button class="btn btn-sm" id="resume">${icon('play', 'icon-sm')}Resume</button>`
            : `<button class="btn btn-sm" id="pause" data-menu-anchor>${icon('pause', 'icon-sm')}Pause${icon('chevron-down', 'icon-sm')}</button>`}
          <button class="icon-btn" id="more" aria-label="More profile actions" data-menu-anchor>${icon('ellipsis')}</button></div>` : ''}
      </div>
      ${inFuture(g.pausedUntil) ? `<div class="banner warn" style="margin:12px 14px 0">${icon('pause')}<div class="banner-text"><b>Internet is paused for this profile</b><p>Until ${until(g.pausedUntil)}</p></div></div>` : ''}
      <div class="tabs">
        ${[['filters', 'shield', 'Categories'], ['apps', 'smartphone', `Apps${g.apps?.length ? ` (${g.apps.length})` : ''}`], ['schedules', 'clock', 'Schedules'], ['time', 'timer', `Time${g.person ? (g.dailyLimit ? ` (${hm(g.dailyLimit)})` : '') : ''}`], ['lists', 'list-filter', 'Allow & block'], ['devices', 'monitor-smartphone', `Devices (${members.length})`]]
          .map(([id, ic, l]) => `<button data-tab="${id}" class="${tab === id ? 'on' : ''}">${icon(ic, 'icon-sm')}${l}</button>`).join('')}
      </div>
      <div id="tab"></div>`;

    const body = $('#tab', ed);
    if (tab === 'filters') {
      body.innerHTML = `<div class="panel-body">
        <div class="cat-grid">${state.categories.filter((c) => !c.id.startsWith('list:')).map((c) => {
          const on = g.categories.includes(c.id);
          return `<label class="cat-card ${on ? 'on' : ''}">${icon(CATEGORY_ICONS[c.id] || 'shield')}
            <span class="cat-text"><b>${esc(c.name)}</b><small>${esc(c.desc)}</small><small class="num" style="margin-top:3px">${compact(c.domains)} domains</small></span>
            ${switchInput(`data-cat="${c.id}" ${ro}`, on, `Block ${c.name}`)}</label>`;
        }).join('')}</div></div>
        <div class="setting" style="border-top:1px solid var(--border)">
          <div class="setting-text"><b>Enforce SafeSearch</b><small>Strict results on Google, Bing and DuckDuckGo, and YouTube Restricted Mode. Can't be turned off on the device.</small></div>
          ${switchInput(`id="safe" ${ro}`, g.safeSearch, 'Enforce SafeSearch')}
        </div>`;
      $$('[data-cat]', body).forEach((cb) => cb.addEventListener('change', () => {
        const ids = $$('[data-cat]:checked', body).map((x) => x.dataset.cat);
        save((b) => (b.categories = ids), `${cb.checked ? 'Now blocking' : 'Stopped blocking'} ${state.categories.find((c) => c.id === cb.dataset.cat).name}`);
      }));
      $('#safe', body).addEventListener('change', (e) => save((b) => (b.safeSearch = e.target.checked), `SafeSearch ${e.target.checked ? 'on' : 'off'}`));
    }

    if (tab === 'apps') {
      appsCatalog ??= await get('/api/apps');
      if (!ctx.alive()) return;
      const on = new Set(g.apps || []);
      body.innerHTML = `
        <div class="toolbar"><div class="input-wrap">${icon('search')}<input class="input input-sm" id="app-q" placeholder="Find an app" aria-label="Find an app"></div>
          <span class="t-sm t-3">Block a single app without blocking its whole category.</span></div>
        <div id="app-groups">${appsCatalog.groups.map((grp) => `
          <div class="section" data-group="${grp.id}"><div class="section-title">${esc(grp.name)}</div>
            <div class="app-grid">${appsCatalog.apps.filter((a) => a.group === grp.id).map((a) => `
              <label class="app-row" data-name="${esc(a.name.toLowerCase())}">${switchInput(`data-app="${a.id}" ${ro}`, on.has(a.id), `Block ${a.name}`)}<span>${esc(a.name)}</span></label>`).join('')}</div>
          </div>`).join('')}</div>`;
      $$('[data-app]', body).forEach((cb) => cb.addEventListener('change', () => {
        const ids = $$('[data-app]:checked', body).map((x) => x.dataset.app);
        const name = appsCatalog.apps.find((a) => a.id === cb.dataset.app).name;
        save((b) => (b.apps = ids), `${cb.checked ? 'Blocked' : 'Unblocked'} ${name}`);
      }));
      $('#app-q', body).addEventListener('input', (e) => {
        const q = e.target.value.trim().toLowerCase();
        $$('.app-row', body).forEach((r) => (r.hidden = q && !r.dataset.name.includes(q)));
        $$('[data-group]', body).forEach((s) => (s.hidden = q && !$$('.app-row:not([hidden])', s).length));
      });
    }

    if (tab === 'time') {
      appsCatalog ??= await get('/api/apps');
      const usage = g.person ? await get('/api/screentime').then((l) => l.find((p) => p.id === g.id)).catch(() => null) : null;
      if (!ctx.alive()) return;
      const limits = g.appLimits || [];
      const PRESETS = [[0, 'No limit'], [30, '30 min'], [60, '1 hour'], [90, '1 h 30'], [120, '2 hours'], [180, '3 hours'], [240, '4 hours'], [300, '5 hours'], [360, '6 hours'], [480, '8 hours']];
      const limitOpts = (sel) => PRESETS.map(([m, l]) => `<option value="${m}" ${m === sel ? 'selected' : ''}>${l}</option>`).join('') + (PRESETS.some(([m]) => m === sel) ? '' : `<option value="${sel}" selected>${hm(sel)}</option>`);
      const targetName = (t) => t.startsWith('app:') ? (appsCatalog.apps.find((a) => a.id === t.slice(4))?.name || t) : (state.categories.find((c) => c.id === t.slice(4))?.name || t);
      const usedFor = (t) => usage?.limits?.find((l) => l.target === t)?.used || 0;
      body.innerHTML = `
        <div class="setting">
          <div class="setting-text"><b>This profile is a person</b><small>Screen time is counted once across all their devices, and they can see it, with any limits, at <span class="mono">http://fengard.lan/me</span> from any of their devices.</small></div>
          ${switchInput(`id="person" ${ro}`, g.person, 'This profile is a person')}
        </div>
        ${g.person ? `
        <div class="setting">
          <div class="setting-text"><b>Online time per day</b><small>Across every device in the profile. When it runs out, the internet pauses for them until midnight and their page explains why.</small></div>
          <select class="select select-sm" id="daily" ${ro} style="width:auto">${limitOpts(g.dailyLimit || 0)}</select>
        </div>
        ${usage ? `<div class="panel-body" style="padding-top:12px">
          <div class="row" style="justify-content:space-between;margin-bottom:6px"><b>Today</b><span class="t-2 num">${hm(usage.used)}${g.dailyLimit ? ` of ${hm(g.dailyLimit + usage.bonus)}${usage.bonus ? ` <span class="t-3">(incl. ${hm(usage.bonus)} extra)</span>` : ''}` : ''}</span></div>
          ${g.dailyLimit ? `<div class="meter"><i style="width:${usage.pct}%" class="${usage.left === 0 ? 'full' : ''}"></i></div>` : ''}
          ${usage.devices.length ? `<div class="t-xs t-3" style="margin-top:8px">${usage.devices.map((d) => `${esc(d.name)} ${d.used ? hm(d.used) : '—'}`).join(' · ')}</div>` : ''}
          ${admin ? `<div class="row wrap" style="margin-top:12px;gap:6px"><span class="t-sm t-3">Add time today:</span>
            ${[15, 30, 60].map((m) => `<button class="btn btn-sm" data-bonus="${m}">+${m === 60 ? '1 h' : m + ' min'}</button>`).join('')}
            ${usage.bonus ? `<button class="btn btn-sm btn-ghost" data-bonus="${-usage.bonus}">Take back ${hm(usage.bonus)}</button>` : ''}</div>` : ''}
        </div>` : ''}
        <div class="section" style="border-top:1px solid var(--border)"><div class="section-title">App and category limits</div>
          <div class="table-wrap"><table class="table dense"><thead><tr><th>App or category</th><th>Per day</th><th>Today</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
            ${admin ? `<tr class="inline-form"><td><select class="select select-sm" id="lt" style="width:auto;max-width:260px">
                <optgroup label="Apps">${appsCatalog.apps.filter((a) => !limits.some((l) => l.target === 'app:' + a.id)).map((a) => `<option value="app:${a.id}">${esc(a.name)}</option>`).join('')}</optgroup>
                <optgroup label="Categories">${state.categories.filter((c) => !c.id.startsWith('list:') && !limits.some((l) => l.target === 'cat:' + c.id)).map((c) => `<option value="cat:${c.id}">${esc(c.name)}</option>`).join('')}</optgroup></select></td>
              <td><select class="select select-sm" id="lm" style="width:auto">${PRESETS.filter(([m]) => m > 0).map(([m, l]) => `<option value="${m}" ${m === 120 ? 'selected' : ''}>${l}</option>`).join('')}</select></td>
              <td></td><td class="col-actions"><button class="btn btn-primary btn-sm" id="ladd">${icon('plus', 'icon-sm')}Add</button></td></tr>` : ''}
            ${limits.map((l) => { const u = usedFor(l.target); return `<tr data-target="${esc(l.target)}"><td class="cell-title">${esc(targetName(l.target))}</td><td>${hm(l.minutes)}</td>
              <td class="t-2">${usage ? `${hm(u)}${u >= l.minutes ? ' <span class="tag warn">used up</span>' : ''}` : ''}</td>
              <td class="col-actions">${admin ? `<button class="icon-btn row-actions" data-ldel aria-label="Remove limit">${icon('trash-2')}</button>` : ''}</td></tr>`; }).join('')}
          </tbody></table>${!limits.length ? `<p class="t-3 t-sm" style="padding:10px 14px">No app limits. A limit counts minutes with activity in that app, on any of their devices.</p>` : ''}</div>
        </div>` : `<p class="t-3 t-sm" style="padding:12px 14px">Turn this on for a child or family member. Shared devices and smart-home gear stay as they are.</p>`}`;
      $('#person', body).addEventListener('change', (e) => save((b) => (b.person = e.target.checked), e.target.checked ? `${g.name} is now a person` : 'Screen time off'));
      $('#daily', body)?.addEventListener('change', (e) => save((b) => (b.dailyLimit = +e.target.value), +e.target.value ? `Daily limit ${hm(+e.target.value)}` : 'Daily limit removed'));
      $('#ladd', body)?.addEventListener('click', () => {
        const target = $('#lt', body).value, minutes = +$('#lm', body).value;
        if (!target) return toast('Pick an app or category', 'error');
        save((b) => (b.appLimits = [...(b.appLimits || []), { target, minutes }]), `${targetName(target)} limited to ${hm(minutes)} a day`);
      });
      body.addEventListener('click', async (e) => {
        const del = e.target.closest('[data-ldel]');
        if (del) { const t = del.closest('tr').dataset.target; return save((b) => (b.appLimits = (b.appLimits || []).filter((l) => l.target !== t)), `Limit removed`); }
        const bonus = e.target.closest('[data-bonus]');
        if (bonus && await attempt(() => post(`/api/groups/${g.id}/bonus`, { minutes: +bonus.dataset.bonus }), +bonus.dataset.bonus > 0 ? `Added ${hm(+bonus.dataset.bonus)} for today` : 'Extra time taken back')) drawEditor();
      });
    }

    if (tab === 'schedules') {
      const scheds = g.schedules || [];
      body.innerHTML = `
        <div class="panel-body">${weekView(scheds)}</div>
        <div>${scheds.length ? scheds.map((s, i) => `
          <div class="setting">
            ${switchInput(`data-toggle="${i}" ${ro}`, s.enabled, `Enable ${s.name}`)}
            <div class="setting-text"><b>${esc(s.name)} ${s.enabled && activeNow(s) ? '<span class="badge b-accent" style="margin-left:6px">Active now</span>' : ''}</b>
              <small>${daysLabel(s.days)} · ${s.start}–${s.end} · ${s.blockAll ? 'No internet' : 'Blocks ' + s.categories.map((c) => state.categories.find((x) => x.id === c)?.name || c).join(', ')}</small></div>
            ${admin ? `<button class="btn btn-ghost btn-sm" data-edit="${i}">Edit</button><button class="icon-btn" data-del="${i}" aria-label="Delete ${esc(s.name)}">${icon('trash-2')}</button>` : ''}
          </div>`).join('') : empty({ icon: 'clock', title: 'No schedules', text: 'Switch the internet off at bedtime, or block games during homework.' })}</div>
        ${admin ? `<div class="panel-foot"><button class="btn btn-sm" id="add-sched">${icon('plus', 'icon-sm')}Add schedule</button></div>` : ''}`;
      $('#add-sched', body)?.addEventListener('click', () => scheduleModal(null, (s) => save((b) => b.schedules.push(s), 'Schedule added')));
      body.addEventListener('click', (e) => {
        const ed2 = e.target.closest('[data-edit]'), dl = e.target.closest('[data-del]');
        if (ed2) { const i = +ed2.dataset.edit; scheduleModal(scheds[i], (s) => save((b) => (b.schedules[i] = s), 'Schedule updated')); }
        if (dl) save((b) => b.schedules.splice(+dl.dataset.del, 1), 'Schedule deleted');
      });
      $$('[data-toggle]', body).forEach((cb) => cb.addEventListener('change', () => save((b) => (b.schedules[+cb.dataset.toggle].enabled = cb.checked), cb.checked ? 'Schedule on' : 'Schedule off')));
    }

    if (tab === 'lists') {
      const list = (key, title, desc) => `<div class="section">
        <div class="section-title">${title}</div><p class="t-xs t-3" style="margin:-6px 0 10px">${desc}</p>
        ${admin ? `<form class="row" data-add="${key}" style="margin-bottom:10px;max-width:420px"><input class="input input-sm" placeholder="example.com" spellcheck="false" aria-label="${title}" required><button class="btn btn-sm">${icon('plus', 'icon-sm')}Add</button></form>` : ''}
        <div class="chips">${(g[key] || []).map((d) => `<span class="chip">${esc(d)}${admin ? `<button data-rm="${key}" data-d="${esc(d)}" aria-label="Remove ${esc(d)}">${icon('x', 'icon-sm')}</button>` : ''}</span>`).join('') || '<span class="t-3 t-sm">None</span>'}</div></div>`;
      body.innerHTML = `${list('block', 'Always block', 'Blocked for this profile even when no category covers it. Subdomains included.')}${list('allow', 'Always allow', 'Overrides categories, apps and network-wide blocks for this profile.')}`;
      $$('[data-add]', body).forEach((f) => f.addEventListener('submit', (e) => {
        e.preventDefault();
        const v = $('input', f).value.trim();
        save((b) => (b[f.dataset.add] = [...(b[f.dataset.add] || []), v]), `Added ${v}`);
      }));
      body.addEventListener('click', (e) => {
        const r = e.target.closest('[data-rm]');
        if (r) save((b) => (b[r.dataset.rm] = b[r.dataset.rm].filter((x) => x !== r.dataset.d)), `Removed ${r.dataset.d}`);
      });
    }

    if (tab === 'devices') {
      body.innerHTML = members.length ? members.map((d) => `
        <div class="setting"><span class="dev-icon">${icon(deviceType(d).icon)}</span>
          <div class="setting-text"><b>${esc(deviceName(d))}</b><small class="mono">${esc((d.ips || [])[0] || d.mac)}</small></div>
          <a class="btn btn-ghost btn-sm" href="#devices?mac=${encodeURIComponent(d.mac)}">Open</a></div>`).join('')
        : empty({ icon: 'monitor-smartphone', title: 'No devices in this profile', text: 'Move a device here from the Devices page.' });
    }

    $$('[data-tab]', ed).forEach((b) => b.addEventListener('click', () => { tab = b.dataset.tab; drawEditor(); }));
    $('#g-name', ed)?.addEventListener('change', (e) => { const v = e.target.value.trim(); if (v && v !== g.name) save((b) => (b.name = v), 'Profile renamed'); });
    $('#resume', ed)?.addEventListener('click', async () => { if (await attempt(() => post(`/api/groups/${g.id}/pause`, { minutes: 0 }), 'Internet resumed')) load(); });
    $('#pause', ed)?.addEventListener('click', (e) => menu(e.currentTarget, [30, 60, 120, 1440].map((m) => ({
      label: m < 60 ? `${m} minutes` : m === 1440 ? '24 hours' : `${m / 60} hour${m > 60 ? 's' : ''}`, icon: 'clock',
      onClick: async () => { if (await attempt(() => post(`/api/groups/${g.id}/pause`, { minutes: m }), `Paused ${g.name}`)) load(); },
    }))));
    $('#more', ed)?.addEventListener('click', (e) => menu(e.currentTarget, [
      ...(g.default ? [] : [{ label: 'Make default for new devices', icon: 'badge-check', onClick: async () => {
        if (await attempt(() => put('/api/settings', { ...settings, defaultGroup: g.id }), `New devices now join ${g.name}`)) load();
      } }]),
      ...(g.default ? [{ html: `<div class="menu-head t-xs t-3">This is the default profile.</div>` }] : ['divider', { label: 'Delete profile', icon: 'trash-2', danger: true, onClick: async () => {
        const ok = await confirmDialog({ title: `Delete ${g.name}?`, danger: true, confirm: 'Delete profile',
          message: `Its ${members.length} device${members.length === 1 ? '' : 's'} will move to the default profile.` });
        if (ok && await attempt(() => del(`/api/groups/${g.id}`), 'Profile deleted')) { current = null; load(); refreshShell(); }
      } }]),
    ]));
  };

  $('#list', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-id]');
    if (b) { current = b.dataset.id; drawList(); drawEditor(); }
  });
  $('#new', el)?.addEventListener('click', () => {
    const m = modal({
      title: 'New profile', subtitle: 'Starts by blocking malware. Add filters right after.',
      body: `<form id="rf"><label class="field"><span>Name</span><input class="input" name="name" placeholder="Teenagers" maxlength="40" required autofocus></label></form>`,
      foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" id="rf-save">Create profile</button>`,
    });
    const f = $('#rf', m.el);
    const submit = async () => {
      const name = f.name.value.trim();
      if (!name) return f.name.focus();
      let created;
      if (await attempt(async () => { created = await post('/api/groups', { name, categories: ['malware'], apps: [], block: [], allow: [], schedules: [] }); }, 'Profile created')) {
        m.close();
        current = created.id;
        tab = 'filters';
        load();
      }
    };
    $('#rf-save', m.el).addEventListener('click', submit);
    f.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  });

  await load();
}

const hm = (m) => (m < 60 ? `${m} min` : m % 60 ? `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')}` : `${m / 60} h`);

function activeNow(s) {
  const now = new Date(), day = now.getDay(), m = now.getHours() * 60 + now.getMinutes();
  const st = toMin(s.start), en = toMin(s.end);
  if (st <= en) return s.days.includes(day) && m >= st && m < en;
  return (s.days.includes(day) && m >= st) || (s.days.includes((day + 6) % 7) && m < en);
}
const toMin = (t) => +t.slice(0, 2) * 60 + +t.slice(3);

function daysLabel(days) {
  const set = [...days].sort().join(',');
  if (set === '0,1,2,3,4,5,6') return 'Every day';
  if (set === '1,2,3,4,5') return 'Weekdays';
  if (set === '0,6') return 'Weekends';
  if (set === '0,1,2,3,4') return 'School nights';
  return WEEK.filter((d) => days.includes(d)).map((d) => DAYS[d]).join(', ');
}

function weekView(scheds) {
  const blocks = Object.fromEntries(WEEK.map((d) => [d, []]));
  for (const s of scheds) {
    if (!s.enabled) continue;
    const st = toMin(s.start), en = toMin(s.end), cls = s.blockAll ? 'all' : 'cat';
    for (const d of s.days) {
      if (st <= en) blocks[d].push([st, en, cls, s.name]);
      else { blocks[d].push([st, 1440, cls, s.name]); blocks[(d + 1) % 7].push([0, en, cls, s.name]); }
    }
  }
  const now = new Date();
  const nowPct = ((now.getHours() * 60 + now.getMinutes()) / 1440) * 100;
  return `<div class="week">
    ${WEEK.map((d) => `<div class="day">${DAYS[d]}</div><div class="track">
      ${blocks[d].map(([a, b, cls, name]) => `<div class="blk ${cls}" style="left:${(a / 1440) * 100}%;width:${((b - a) / 1440) * 100}%" title="${esc(name)}"></div>`).join('')}
      ${d === now.getDay() ? `<div style="position:absolute;top:-3px;bottom:-3px;left:${nowPct}%;width:2px;background:var(--text);border-radius:2px" title="Now"></div>` : ''}
    </div>`).join('')}
    <div class="hours"><span>00:00</span><span>06:00</span><span>12:00</span><span>18:00</span><span>24:00</span></div>
  </div>
  <div class="legend" style="margin-top:10px"><span><i style="background:var(--accent)"></i>No internet</span><span><i style="background:color-mix(in srgb, var(--accent) 40%, transparent)"></i>Extra categories blocked</span></div>`;
}

function scheduleModal(s, onSave) {
  s = s || { name: '', days: [0, 1, 2, 3, 4], start: '21:30', end: '07:00', blockAll: true, categories: [], enabled: true };
  const m = modal({
    title: s.name ? 'Edit schedule' : 'Add schedule',
    body: `<form id="sf" class="stack" novalidate>
      <label class="field"><span>Name</span><input class="input" name="name" value="${esc(s.name)}" placeholder="Bedtime" required autofocus></label>
      <div class="field"><span>Days</span><div class="days-picker">${WEEK.map((d) => `<label><input type="checkbox" name="day" value="${d}" ${s.days.includes(d) ? 'checked' : ''}><span>${DAYS[d]}</span></label>`).join('')}</div></div>
      <div class="form-row"><label class="field"><span>From</span><input class="input" type="time" name="start" value="${s.start}" required></label>
        <label class="field"><span>Until</span><input class="input" type="time" name="end" value="${s.end}" required><span class="help">Can end the next morning.</span></label></div>
      <div class="field"><span>During this time</span>
        <label class="option"><input type="radio" name="mode" value="all" ${s.blockAll ? 'checked' : ''}><span><b>No internet</b><small>Everything is blocked, at the firewall too.</small></span></label>
        <label class="option"><input type="radio" name="mode" value="cats" ${s.blockAll ? '' : 'checked'}><span><b>Block extra categories</b><small>On top of the profile's normal filters.</small></span></label>
        <div id="cats" class="app-grid" style="margin-top:4px" ${s.blockAll ? 'hidden' : ''}>
          ${state.categories.filter((c) => !c.id.startsWith('list:')).map((c) => `<label class="app-row"><input type="checkbox" name="cat" value="${c.id}" ${s.categories?.includes(c.id) ? 'checked' : ''} style="accent-color:var(--accent)"><span>${esc(c.name)}</span></label>`).join('')}</div></div>
      <p class="t-sm t-danger" id="sf-err" hidden></p>
    </form>`,
    foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" id="sf-save">Save schedule</button>`,
  });
  const f = $('#sf', m.el);
  $$('[name=mode]', f).forEach((r) => r.addEventListener('change', () => ($('#cats', f).hidden = f.mode.value === 'all')));
  const submit = () => {
    const days = $$('[name=day]:checked', f).map((x) => +x.value);
    const cats = $$('[name=cat]:checked', f).map((x) => x.value);
    const err = (t) => { $('#sf-err', f).textContent = t; $('#sf-err', f).hidden = false; };
    if (!f.name.value.trim()) return err('Give the schedule a name.');
    if (!days.length) return err('Pick at least one day.');
    if (f.start.value === f.end.value) return err('Start and end times must differ.');
    if (f.mode.value === 'cats' && !cats.length) return err('Pick at least one category to block.');
    m.close();
    onSave({ name: f.name.value.trim(), days, start: f.start.value, end: f.end.value, blockAll: f.mode.value === 'all', categories: f.mode.value === 'all' ? [] : cats, enabled: s.enabled ?? true });
  };
  $('#sf-save', m.el).addEventListener('click', submit);
  f.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
}
