import { get, post, put } from '../api.js';
import { $, esc, icon, ago, attempt, busy, confirmDialog, empty, switchInput, isSet } from '../ui.js';
import { isAdmin, refreshShell } from '../main.js';

const WATCHES = {
  packages: { icon: 'archive', text: 'Programs installed or removed with opkg or apk, and new versions.' },
  files: { icon: 'file-search', text: 'Programs, libraries and startup scripts changed since the firmware was installed.' },
  ssh: { icon: 'key-round', text: 'Keys that can log in to the router over SSH.' },
  accounts: { icon: 'user', text: 'Users that can log in, and any account with full admin rights.' },
  startup: { icon: 'power', text: 'Services that start at boot, and scheduled jobs.' },
  ports: { icon: 'network', text: "Services listening on the router. Fengard's own aren't counted." },
  exposure: { icon: 'globe', text: 'Port forwards and firewall rules that let the internet in.' },
};

const KIND = {
  added: '<span class="badge b-warning">New</span>',
  changed: '<span class="badge b-accent">Changed</span>',
  removed: '<span class="badge b-neutral">Removed</span>',
};

export async function render(el, ctx) {
  const admin = isAdmin();
  let st, settings, polling = false;
  el.innerHTML = `
    <div id="sc-banner"></div>
    <div class="panel">
      <div class="panel-head"><h3>Security check</h3><span class="sub">Compares the router with what you last approved. It only notices changes, it doesn't undo them.</span>
        ${admin ? `<div class="actions"><button class="btn btn-sm" id="check">${icon('refresh-cw', 'icon-sm')}Check now</button></div>` : ''}</div>
      <div class="stats stats-3" id="summary"></div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Changes</h3><span class="sub" id="changes-sub"></span>
        ${admin ? `<div class="actions"><button class="btn btn-sm" id="approve-all" hidden>${icon('check', 'icon-sm')}Approve all</button></div>` : ''}</div>
      <div id="changes"></div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>What's watched</h3></div>
      <div id="watches"></div>
    </div>`;

  const stat = (label, ic, value, sub) => `<div class="stat"><div class="stat-label">${icon(ic, 'icon-sm')}${label}</div><div class="stat-value">${value}</div><div class="stat-sub">${sub}</div></div>`;

  const draw = () => {
    const banner = $('#sc-banner', el);
    if (!st.available) {
      banner.innerHTML = `<div class="banner info">${icon('info')}<div class="banner-text"><b>Only available on a router</b><p>Fengard is running on a computer, so there's no router firmware to watch. Install it on the router to use the security check.</p></div></div>`;
    } else if (st.errors && Object.keys(st.errors).length) {
      const names = Object.keys(st.errors).map((w) => st.watches.find((x) => x.id === w)?.label || w);
      banner.innerHTML = `<div class="banner warn">${icon('triangle-alert')}<div class="banner-text"><b>Some things couldn't be checked</b><p>${esc(names.join(', '))}: ${esc(Object.values(st.errors)[0])}</p></div></div>`;
    } else if (st.truncated) {
      banner.innerHTML = `<div class="banner warn">${icon('triangle-alert')}<div class="banner-text"><b>Not every file was checked</b><p>There were more changed files than one check reads, so the rest were listed by size only.</p></div></div>`;
    } else banner.innerHTML = '';

    const n = st.changes.length;
    const status = !st.available ? '<span class="status-pill off"><span class="dot"></span>Off</span>'
      : st.running ? '<span class="status-pill off"><span class="dot"></span>Checking…</span>'
      : !isSet(st.baseline) ? '<span class="status-pill off"><span class="dot"></span>No baseline yet</span>'
      : n ? `<span class="status-pill warn"><span class="dot"></span>${n} change${n === 1 ? '' : 's'}</span>`
      : '<span class="status-pill ok"><span class="dot"></span>No changes</span>';
    $('#summary', el).innerHTML =
      stat('Status', 'shield-check', status, st.enabled ? 'Checks every night around 2 am' : 'Nightly check is off') +
      stat('Last check', 'clock', isSet(st.lastCheck) ? esc(ago(st.lastCheck)) : 'Never', isSet(st.lastCheck) ? esc(new Date(st.lastCheck).toLocaleString()) : 'The first check becomes the baseline') +
      stat('Baseline', 'badge-check', isSet(st.baseline) ? esc(ago(st.baseline)) : 'None', esc(st.firmware || 'Updated when you approve changes'));
    const btn = $('#check', el);
    if (btn) btn.disabled = !st.available || st.running;

    $('#changes-sub', el).textContent = n ? 'Approve what you expected, look into the rest.' : '';
    const all = $('#approve-all', el);
    if (all) all.hidden = n < 2;
    if (!n) {
      $('#changes', el).innerHTML = empty({ icon: 'shield-check', title: 'Nothing changed',
        text: isSet(st.baseline) ? 'Everything matches what you last approved.' : 'The first check takes a baseline of the router to compare with later.' });
    } else {
      const groups = st.watches.filter((w) => st.changes.some((c) => c.watch === w.id));
      $('#changes', el).innerHTML = `<div class="feed">${groups.map((w) => {
        const items = st.changes.filter((c) => c.watch === w.id);
        return `<div class="section-title" style="padding:12px 14px 4px;margin:0;border-bottom:1px solid var(--border)">${esc(w.label)}</div>${items.map((c) => `
          <div class="feed-item">
            <div class="feed-ico sev-warning">${icon(WATCHES[c.watch]?.icon || 'info', 'icon-sm')}</div>
            <div class="feed-text"><b>${esc(c.what)}</b><small>${KIND[c.kind] || ''} First seen ${esc(ago(c.firstSeen).replace('Just now', 'just now'))}</small>
              ${admin ? `<div class="feed-actions"><button class="btn btn-sm" data-approve="${esc(c.id)}">${icon('check', 'icon-sm')}Approve</button></div>` : ''}</div>
            <div class="feed-time"></div></div>`).join('')}`;
      }).join('')}</div>`;
    }

    const ro = admin && st.available ? '' : 'disabled';
    $('#watches', el).innerHTML = `
      <div class="setting"><div class="setting-text"><b>Nightly check</b><small>Runs around 2 am and raises an alert when something new shows up. Check now works either way.</small></div>
        ${switchInput(`data-master ${ro}`, st.enabled, 'Nightly check')}</div>
      ${st.watches.map((w) => `<div class="setting"><div class="setting-text"><b>${esc(w.label)}</b><small>${WATCHES[w.id]?.text || ''}</small></div>
        ${switchInput(`data-watch="${esc(w.id)}" ${ro}`, w.on, w.label)}</div>`).join('')}`;
  };

  const load = async () => {
    st = await get('/api/security');
    if (!ctx.alive()) return;
    draw();
    // keep up while a check runs, it takes a minute or two on a slow router
    if (st.running && !polling) {
      polling = true;
      const stop = ctx.every(2000, async () => {
        st = await get('/api/security');
        if (!ctx.alive()) return stop();
        draw();
        if (!st.running) { stop(); polling = false; refreshShell(); }
      });
    }
  };

  el.addEventListener('click', async (e) => {
    const check = e.target.closest('#check');
    if (check) {
      if (await busy(check, () => attempt(() => post('/api/security/check'), 'Checking the router'))) {
        st.running = true;
        draw();
      }
      setTimeout(load, 1000);
      return;
    }
    const one = e.target.closest('[data-approve]');
    if (one) {
      if (await attempt(() => post('/api/security/approve', { ids: [one.dataset.approve] }), 'Approved')) load();
      return;
    }
    if (e.target.closest('#approve-all')) {
      const ok = await confirmDialog({ title: `Approve all ${st.changes.length} changes?`, confirm: 'Approve all',
        message: 'They become part of the baseline and won\'t be flagged again. Only do this if you made them.' });
      if (ok && await attempt(() => post('/api/security/approve', { all: true }), 'All changes approved')) load();
    }
  });

  el.addEventListener('change', async (e) => {
    const t = e.target;
    if (!t.matches('[data-master], [data-watch]')) return;
    const ok = await attempt(async () => {
      settings = await get('/api/settings');
      const patch = {};
      if (t.matches('[data-master]')) patch.securityCheckOff = !t.checked;
      else {
        const off = new Set(settings.securityUnwatched || []);
        if (t.checked) off.delete(t.dataset.watch); else off.add(t.dataset.watch);
        patch.securityUnwatched = [...off];
      }
      await put('/api/settings', { ...settings, ...patch });
    }, 'Saved');
    if (!ok) t.checked = !t.checked;
    load();
  });

  await load();
  ctx.every(15000, load);
}
