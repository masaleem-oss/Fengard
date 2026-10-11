import { get, post, put, del } from '../api.js';
import { $, $$, esc, icon, fmt, compact, ago, until, isSet, attempt, busy, empty, toast, switchInput, confirmDialog, statusBadge, modal } from '../ui.js';
import { CATEGORY_ICONS, deviceName } from '../meta.js';
import { state, isAdmin } from '../main.js';
// well known lists, sizes are rough and only there to help pick
const H = 'https://raw.githubusercontent.com/hagezi/dns-blocklists/main/wildcard/';
const POPULAR = [
  { group: 'Ads and tracking', items: [
    { name: 'HaGeZi Light', size: '55k', url: H + 'light-onlydomains.txt', desc: 'Basic, almost never breaks anything' },
    { name: 'HaGeZi Normal', size: '160k', url: H + 'multi-onlydomains.txt', desc: 'All-round protection, a good default' },
    { name: 'HaGeZi Pro', size: '196k', url: H + 'pro-onlydomains.txt', desc: 'Stricter, still rarely breaks sites' },
    { name: 'HaGeZi Pro++', size: '209k', url: H + 'pro.plus-onlydomains.txt', desc: 'Aggressive, may break some sites' },
    { name: 'HaGeZi Ultimate', size: '232k', url: H + 'ultimate-onlydomains.txt', desc: 'Strictest, expect some breakage' },
    { name: 'OISD Small', size: '56k', url: 'https://small.oisd.nl/domainswild2', desc: 'Ads and tracking with very few false blocks' },
    { name: 'OISD Big', size: '237k', url: 'https://big.oisd.nl/domainswild2', desc: 'Ads, tracking, malware and phishing' },
    { name: 'AdGuard DNS filter', size: '181k', url: 'https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt', desc: 'The default list in AdGuard Home' },
    { name: 'Steven Black Unified', size: '73k', url: 'https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts', desc: 'Classic hosts file of ads and malware' },
    { name: '1Hosts Lite', size: '102k', url: 'https://raw.githubusercontent.com/badmojr/1Hosts/master/Lite/domains.wildcards', desc: 'Ads and tracking, balanced' },
    { name: "Peter Lowe's list", size: '3.5k', url: 'https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=0&mimetype=plaintext', desc: 'Small, hand-picked ad servers' },
    { name: 'Dan Pollock', size: '13k', url: 'https://someonewhocares.org/hosts/zero/hosts', desc: 'Long-running hand-kept hosts file' },
    { name: 'HaGeZi Pop-up ads', size: '47k', url: H + 'popupads-onlydomains.txt', desc: 'Annoying pop-up and pop-under ads' },
  ] },
  { group: 'Security', items: [
    { name: 'HaGeZi Fake', size: '17k', url: H + 'fake-onlydomains.txt', desc: 'Fake shops, scams and rip-offs' },
    { name: 'Phishing Army Extended', size: '142k', url: 'https://phishing.army/download/phishing_army_blocklist_extended.txt', desc: 'Phishing sites' },
  ] },
  { group: 'Device trackers', items: [
    { name: 'Samsung trackers', size: '200', url: H + 'native.samsung-onlydomains.txt', desc: 'Smart TVs and phones calling home' },
    { name: 'Amazon trackers', size: '370', url: H + 'native.amazon-onlydomains.txt', desc: 'Fire TV, Echo and Kindle' },
    { name: 'Apple trackers', size: '100', url: H + 'native.apple-onlydomains.txt', desc: 'Apple telemetry' },
    { name: 'Windows and Office trackers', size: '380', url: H + 'native.winoffice-onlydomains.txt', desc: 'Microsoft telemetry' },
  ] },
];


export async function render(el, ctx) {
  const admin = isAdmin();
  let rules = { block: [], allow: [], temp: [] }, lists = [], devices = [], groups = [];
  let kind = ctx.params.get('add') === 'allow' ? 'allow' : 'block';

  el.innerHTML = `
    <div class="panel">
      <div class="panel-head"><h3>Check a site</h3><span class="sub">What each profile would do with a domain, and why</span></div>
      <div class="panel-body">
        <form class="row wrap" id="check" novalidate>
          <div class="input-wrap" style="flex:1;min-width:240px">${icon('scan-search')}<input class="input" name="domain" placeholder="example.com" spellcheck="false" autocomplete="off" value="${esc(ctx.params.get('check') || '')}"></div>
          <select class="select" name="mac" style="width:auto;max-width:240px" aria-label="As device"><option value="">Any device</option></select>
          <button class="btn btn-primary" type="submit">Check</button>
        </form>
        <div id="check-out" style="margin-top:14px" hidden></div>
      </div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Network-wide rules</h3><span class="sub">Every profile · subdomains included · allow beats block</span></div>
      ${admin ? `<form class="toolbar" id="add" novalidate>
        <div class="seg" id="kind"><button type="button" data-k="block" class="${kind === 'block' ? 'on' : ''}">Block</button><button type="button" data-k="allow" class="${kind === 'allow' ? 'on' : ''}">Allow</button></div>
        <div class="input-wrap" style="flex:1;min-width:220px">${icon('globe')}<input class="input input-sm" id="domain" placeholder="example.com or a pasted link" spellcheck="false" autocomplete="off" aria-label="Domain"></div>
        <button class="btn btn-primary btn-sm" type="submit">Add rule</button>
      </form>` : ''}
      <div id="rules"></div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Category blocklists</h3><span class="sub">Updated every 24 hours</span>
        ${admin ? `<div class="actions"><button class="btn btn-sm" id="update">${icon('refresh-cw', 'icon-sm')}Update now</button></div>` : ''}</div>
      <div class="table-wrap" id="cats"></div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Custom blocklists</h3><span class="sub">Any hosts-file or domain list by URL, applied to every profile</span>
        ${admin ? `<div class="actions"><button class="btn btn-sm btn-primary" id="popular">${icon('list-plus', 'icon-sm')}Popular lists</button></div>` : ''}</div>
      ${admin ? `<form class="toolbar" id="add-list" novalidate>
        <input class="input input-sm" name="name" placeholder="Name" style="width:180px" aria-label="List name">
        <div class="input-wrap" style="flex:1;min-width:260px">${icon('link')}<input class="input input-sm" name="url" placeholder="https://…/hosts.txt" spellcheck="false" aria-label="List URL" required></div>
        <button class="btn btn-primary btn-sm" type="submit">Subscribe</button>
      </form>` : ''}
      <div id="lists"></div>
    </div>`;

  const groupName = (id) => groups.find((g) => g.id === id)?.name || id || 'Everyone';

  const drawRules = () => {
    const all = [
      ...rules.block.map((d) => ({ d, k: 'block' })),
      ...rules.allow.map((d) => ({ d, k: 'allow' })),
      ...(rules.temp || []).map((t) => ({ d: t.domain, k: 'temp', t })),
    ].sort((a, b) => a.d.localeCompare(b.d));
    $('#rules', el).innerHTML = all.length ? `<table class="table dense"><thead><tr><th>Domain</th><th>Rule</th><th>Applies to</th><th class="col-actions" aria-label="Actions"></th></tr></thead>
      <tbody>${all.map((r) => `<tr><td class="mono">${esc(r.d)}</td>
        <td>${r.k === 'block' ? `<span class="badge b-accent">Block</span>` : r.k === 'allow' ? `<span class="badge b-success">Allow</span>` : `<span class="badge b-warning">Allow until ${until(r.t.until)}</span>`}</td>
        <td class="t-2">${r.k === 'temp' ? esc(groupName(r.t.group)) : 'Everyone'}${r.t?.by ? ` <span class="t-3">· ${esc(r.t.by)}</span>` : ''}</td>
        <td class="col-actions">${admin ? `<button class="btn btn-ghost btn-sm btn-danger row-actions" data-rm="${r.k}" data-d="${esc(r.d)}" data-g="${esc(r.t?.group || '')}">${icon('x', 'icon-sm')}${r.k === 'temp' ? 'End now' : 'Remove'}</button>` : ''}</td></tr>`).join('')}</tbody></table>`
      : empty({ icon: 'list-filter', title: 'No network-wide rules', text: 'Block or allow a domain for every device. Per-profile rules live on the Profiles page.' });
  };

  const drawCats = () => {
    $('#cats', el).innerHTML = `<table class="table dense"><thead><tr><th>Category</th><th style="text-align:right">Domains</th><th>Updated</th><th>Status</th></tr></thead>
      <tbody>${state.categories.filter((c) => !c.id.startsWith('list:')).map((c) => `<tr>
        <td><div class="cell-main">${icon(CATEGORY_ICONS[c.id] || 'shield', 'icon-sm')}<div><div class="cell-title">${esc(c.name)}</div><div class="cell-sub">${esc(c.desc)}</div></div></div></td>
        <td class="num" style="text-align:right">${fmt(c.domains)}</td>
        <td class="t-2">${isSet(c.updated) ? ago(c.updated) : 'Built-in'}</td>
        <td>${c.error ? `<span class="badge b-danger" title="${esc(c.error)}">Update failed</span>` : '<span class="badge b-success">Current</span>'}</td>
      </tr>`).join('')}</tbody></table>`;
  };

  const drawLists = () => {
    $('#lists', el).innerHTML = lists.length ? `<table class="table dense"><thead><tr><th>List</th><th style="text-align:right">Domains</th><th>Updated</th><th>Enabled</th><th class="col-actions" aria-label="Actions"></th></tr></thead>
      <tbody>${lists.map((l) => `<tr data-id="${l.id}">
        <td><div class="cell-title">${esc(l.name)}</div><div class="cell-sub mono truncate" style="max-width:420px" title="${esc(l.url)}">${esc(l.url)}</div></td>
        <td class="num" style="text-align:right">${fmt(l.domains)}</td>
        <td class="t-2">${l.error ? `<span class="t-danger" title="${esc(l.error)}">Failed</span>` : isSet(l.updated) ? ago(l.updated) : 'Downloading…'}</td>
        <td>${switchInput(`data-toggle ${admin ? '' : 'disabled'}`, l.enabled, `Enable ${l.name}`)}</td>
        <td class="col-actions">${admin ? `<button class="btn btn-ghost btn-sm" data-refresh>${icon('refresh-cw', 'icon-sm')}</button><button class="btn btn-ghost btn-sm btn-danger" data-del>${icon('trash-2', 'icon-sm')}</button>` : ''}</td></tr>`).join('')}</tbody></table>`
      : `<p class="t-3 t-sm" style="padding:16px 14px">No custom lists yet. Pick one from Popular lists or paste any URL. Up to 16.</p>`;
  };

  const load = async () => {
    [rules, lists, devices, groups] = await Promise.all([get('/api/rules'), get('/api/lists'), get('/api/devices'), get('/api/groups')]);
    if (!ctx.alive()) return;
    const sel = $('#check select', el);
    if (sel.options.length === 1) {
      sel.insertAdjacentHTML('beforeend', devices.map((d) => `<option value="${esc(d.mac)}">${esc(deviceName(d))}</option>`).join(''));
      if (ctx.params.get('mac')) sel.value = ctx.params.get('mac');
    }
    drawRules();
    drawLists();
  };

  // check a site
  const checkForm = $('#check', el);
  const runCheck = async () => {
    const domain = checkForm.domain.value.trim();
    if (!domain) return checkForm.domain.focus();
    const out = $('#check-out', el);
    out.hidden = false;
    out.innerHTML = '<div class="skeleton" style="height:80px"></div>';
    try {
      const r = await get(`/api/check?domain=${encodeURIComponent(domain)}${checkForm.mac.value ? '&mac=' + encodeURIComponent(checkForm.mac.value) : ''}`);
      const known = [
        ...r.categories.map((c) => state.categories.find((x) => x.id === c)?.name || c),
        ...(r.lists || []).map((l) => `list “${l}”`),
        ...(r.app ? [`the app ${r.app}`] : []),
      ];
      const dev = r.device ? `<div class="verdict" style="margin-bottom:10px"><span><b>${esc(deviceName(devices.find((d) => d.mac === checkForm.mac.value) || {}))}</b> (${esc(r.device.group)}) · ${esc(r.device.reason || 'no rule matches')}</span>${statusBadge(r.device.action)}</div>` : '';
      out.innerHTML = `${dev}
        <p class="t-sm t-2" style="margin-bottom:10px">${r.local ? `<b class="mono">${esc(r.domain)}</b> is a local name on this network.` : known.length ? `<b class="mono">${esc(r.domain)}</b> is listed under ${esc(known.join(', '))}.` : `<b class="mono">${esc(r.domain)}</b> isn't on any list.`}</p>
        <table class="table dense"><thead><tr><th>Profile</th><th>Result</th><th>Why</th></tr></thead><tbody>
        ${r.profiles.map((p) => `<tr><td>${esc(p.group)}</td><td>${statusBadge(p.action)}</td><td class="t-2">${esc(p.reason || 'No rule matches')}</td></tr>`).join('')}</tbody></table>`;
    } catch (e) {
      out.innerHTML = `<p class="t-danger t-sm">${esc(e.message)}</p>`;
    }
  };
  checkForm.addEventListener('submit', (e) => { e.preventDefault(); runCheck(); });

  // rules
  const addForm = $('#add', el);
  if (addForm) {
    $('#kind', el).addEventListener('click', (e) => {
      const b = e.target.closest('[data-k]');
      if (!b) return;
      kind = b.dataset.k;
      $$('#kind button', el).forEach((x) => x.classList.toggle('on', x === b));
    });
    addForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      const input = $('#domain', el);
      if (!input.value.trim()) return input.focus();
      await busy(addForm.querySelector('[type=submit]'), async () => {
        if (await attempt(async () => { rules = await post('/api/rules', { kind, domain: input.value }); }, `${kind === 'block' ? 'Blocking' : 'Allowing'} ${input.value.trim()}`)) {
          input.value = '';
          drawRules();
        }
      });
    });
    if (ctx.params.get('add')) setTimeout(() => $('#domain', el).focus(), 50);
  }
  $('#rules', el).addEventListener('click', async (e) => {
    const b = e.target.closest('[data-rm]');
    if (!b) return;
    if (b.dataset.rm === 'temp') {
      if (await attempt(() => del('/api/rules/temp', { domain: b.dataset.d, group: b.dataset.g }), 'Temporary allow ended')) load();
    } else if (await attempt(async () => { rules = await del('/api/rules', { kind: b.dataset.rm, domain: b.dataset.d }); }, `Removed ${b.dataset.d}`)) drawRules();
  });

  // categories
  $('#update', el)?.addEventListener('click', async (e) => {
    await busy(e.currentTarget, async () => {
      const before = JSON.stringify(state.categories.map((c) => c.updated));
      await post('/api/categories/update');
      for (let i = 0; i < 30 && ctx.alive(); i++) {
        await new Promise((r) => setTimeout(r, 2000));
        state.categories = await get('/api/categories');
        if (JSON.stringify(state.categories.map((c) => c.updated)) !== before) break;
      }
      if (ctx.alive()) { drawCats(); toast('Blocklists updated'); load(); }
    });
  });

  // custom lists
  $('#add-list', el)?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.target;
    await busy(f.querySelector('[type=submit]'), async () => {
      if (await attempt(() => post('/api/lists', { name: f.name.value.trim(), url: f.url.value.trim(), enabled: true }), 'Subscribed. Downloading the list…')) {
        f.reset();
        await load();
        setTimeout(() => { if (ctx.alive()) load(); }, 6000);
      }
    });
  });
  $('#popular', el)?.addEventListener('click', () => {
    const have = () => new Set(lists.map((l) => l.url));
    const rows = () => POPULAR.map((g) => `<div class="t-xs t-3" style="margin:14px 0 6px;text-transform:uppercase;letter-spacing:.04em">${esc(g.group)}</div>
      <table class="table dense"><tbody>${g.items.map((p) => `<tr>
        <td><div class="cell-title">${esc(p.name)}</div><div class="cell-sub">${esc(p.desc)}</div></td>
        <td class="num t-2" style="text-align:right;white-space:nowrap">${esc(p.size)} domains</td>
        <td class="col-actions">${have().has(p.url) ? '<span class="badge b-success">Added</span>' : `<button class="btn btn-sm" data-pick="${esc(p.url)}">${icon('plus', 'icon-sm')}Add</button>`}</td></tr>`).join('')}</tbody></table>`).join('');
    const m = modal({ title: 'Popular lists', wide: true, subtitle: 'Pick one or two. Bigger lists block more but use more memory and break more sites.',
      body: `<div id="pop">${rows()}</div>`, foot: '<button class="btn" data-close>Done</button>' });
    $('#pop', m.el).addEventListener('click', async (e) => {
      const b = e.target.closest('[data-pick]');
      if (!b) return;
      const p = POPULAR.flatMap((g) => g.items).find((x) => x.url === b.dataset.pick);
      await busy(b, async () => {
        if (await attempt(() => post('/api/lists', { name: p.name, url: p.url, enabled: true }), `${p.name} added. Downloading the list…`)) {
          await load();
          $('#pop', m.el).innerHTML = rows();
          setTimeout(() => { if (ctx.alive()) load(); }, 6000);
        }
      });
    });
  });
  $('#lists', el).addEventListener('change', async (e) => {
    if (!e.target.matches('[data-toggle]')) return;
    const l = lists.find((x) => x.id === e.target.closest('tr').dataset.id);
    if (!await attempt(() => put(`/api/lists/${l.id}`, { id: l.id, name: l.name, url: l.url, enabled: e.target.checked }), e.target.checked ? 'List enabled' : 'List disabled')) e.target.checked = !e.target.checked;
    load();
  });
  $('#lists', el).addEventListener('click', async (e) => {
    const row = e.target.closest('tr[data-id]');
    if (!row) return;
    const l = lists.find((x) => x.id === row.dataset.id);
    if (e.target.closest('[data-refresh]')) {
      await busy(e.target.closest('[data-refresh]'), () => attempt(() => post(`/api/lists/${l.id}/update`), 'List updated'));
      load();
    }
    if (e.target.closest('[data-del]')) {
      const ok = await confirmDialog({ title: `Remove “${l.name}”?`, danger: true, confirm: 'Remove', message: 'Its domains will no longer be blocked.' });
      if (ok && await attempt(() => del(`/api/lists/${l.id}`), 'List removed')) load();
    }
  });

  drawCats();
  await load();
  if (ctx.params.get('check')) runCheck();
}
