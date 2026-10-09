import { get, post, put, setUnauthorizedHandler } from './api.js';
import { $, $$, esc, icon, toast, busy, menu, popover, closeMenus, closeOverlays, ago, until, attempt, inFuture } from './ui.js';
import { deviceName, deviceType } from './meta.js';

// state

export const state = {
  me: null,          // user and role
  boxName: 'Fengard',
  version: '',
  categories: [],
  shell: { pending: 0, unread: 0, firewallError: '', online: true, paused: false, pausedUntil: null },
};
export const isAdmin = () => state.me?.role === 'admin';

const PAGES = [
  { section: 'Monitor' },
  { id: 'dashboard', label: 'Dashboard', icon: 'layout-dashboard' },
  { id: 'activity', label: 'Activity', icon: 'activity' },
  { id: 'alerts', label: 'Alerts', icon: 'bell' },
  { section: 'Protection' },
  { id: 'devices', label: 'Devices', icon: 'monitor-smartphone' },
  { id: 'profiles', label: 'Profiles', icon: 'users' },
  { id: 'filtering', label: 'Filtering', icon: 'shield' },
  { section: 'Network' },
  { id: 'dns', label: 'DNS', icon: 'globe' },
  { id: 'forwarding', label: 'Port forwarding', icon: 'arrow-left-right' },
  { id: 'firewall', label: 'Firewall', icon: 'brick-wall' },
  { id: 'vpn', label: 'VPN', icon: 'globe-lock' },
  { section: 'System' },
  { id: 'settings', label: 'Settings', icon: 'settings' },
];
const pageById = Object.fromEntries(PAGES.filter((p) => p.id).map((p) => [p.id, p]));
const loaders = {
  dashboard: () => import('./pages/dashboard.js'),
  activity: () => import('./pages/activity.js'),
  alerts: () => import('./pages/alerts.js'),
  devices: () => import('./pages/devices.js'),
  profiles: () => import('./pages/profiles.js'),
  filtering: () => import('./pages/filtering.js'),
  dns: () => import('./pages/dns.js'),
  forwarding: () => import('./pages/forwarding.js'),
  firewall: () => import('./pages/firewall.js'),
  vpn: () => import('./pages/vpn.js'),
  settings: () => import('./pages/settings.js'),
};

const pref = {
  get: (k) => { try { return localStorage.getItem(k); } catch { return null; } },
  set: (k, v) => { try { localStorage.setItem(k, v); } catch {} },
};

// boot

async function boot() {
  const sprite = await fetch('img/icons.svg').then((r) => r.text()).catch(() => '');
  document.body.insertAdjacentHTML('afterbegin', sprite);
  setUnauthorizedHandler(() => { if (state.me) { state.me = null; showSignIn('Your session ended. Sign in again.'); } });

  let s;
  try {
    s = await get('/api/session');
  } catch (e) {
    $('#root').innerHTML = `<div class="auth-main"><p class="t-2">${esc(e.message)}</p></div>`;
    return;
  }
  state.boxName = s.boxName || 'Fengard';
  state.version = s.version || '';
  if (s.setupNeeded) return showSetup();
  if (!s.user) return showSignIn();
  state.me = { user: s.user, role: s.role };
  startApp();
}

// sign in and setup

const logoFilter = () => (document.documentElement.dataset.theme === 'light' ? 'none' : 'invert(1)');

function authFrame(body) {
  $('#root').innerHTML = `
    <div class="auth">
      <aside class="auth-side">
        <div class="brand"><img src="img/logo.svg" alt="" style="filter:${logoFilter()}"><b>FENGARD</b></div>
        <div>
          <h2>${esc(state.boxName)}</h2>
          <p>Network protection that runs on your gateway, not in someone's cloud.</p>
          <ul class="auth-points">
            <li>${icon('shield')}<span>Every device is covered the moment it connects. Nothing to install.</span></li>
            <li>${icon('users')}<span>Profiles decide what each person can reach, and when.</span></li>
            <li>${icon('lock')}<span>Filtering, firewall and logs stay on this box.</span></li>
          </ul>
        </div>
        <div class="foot">Fengard ${esc(state.version)}</div>
      </aside>
      <main class="auth-main"><div class="auth-panel">${body}</div></main>
    </div>`;
}

function passwordField(name, label, autocomplete, extra = '') {
  return `<label class="field"><span>${label}</span>
    <div class="input-wrap">${icon('lock')}
      <input class="input" type="password" name="${name}" autocomplete="${autocomplete}" required ${extra}>
      <button type="button" class="icon-btn input-action" data-reveal aria-label="Show password">${icon('eye')}</button>
    </div></label>`;
}

function wirePasswordUI(form) {
  $$('[data-reveal]', form).forEach((b) => b.addEventListener('click', () => {
    const input = b.previousElementSibling;
    const show = input.type === 'password';
    input.type = show ? 'text' : 'password';
    b.innerHTML = icon(show ? 'eye-off' : 'eye');
    b.setAttribute('aria-label', show ? 'Hide password' : 'Show password');
  }));
  const caps = $('[data-caps]', form);
  form.addEventListener('keyup', (e) => { if (caps && e.getModifierState) caps.hidden = !e.getModifierState('CapsLock'); });
}

function showSignIn(notice = '') {
  authFrame(`<form id="signin" novalidate>
    <div><h1>Sign in</h1><p class="lead">Manage protection for ${esc(state.boxName)}.</p></div>
    ${notice ? `<div class="auth-alert warn">${icon('info')}<span>${esc(notice)}</span></div>` : ''}
    <div class="auth-alert" id="err" hidden>${icon('circle-alert')}<span></span></div>
    <label class="field"><span>Username</span>
      <div class="input-wrap">${icon('user')}<input class="input" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required></div></label>
    ${passwordField('password', 'Password', 'current-password')}
    <div class="t-xs t-warning row" data-caps hidden>${icon('triangle-alert', 'icon-sm')}Caps Lock is on</div>
    <button class="btn btn-primary btn-block" type="submit">Sign in</button>
  </form>`);
  const f = $('#signin');
  f.style.display = 'grid';
  f.style.gap = '16px';
  wirePasswordUI(f);
  f.username.focus();
  const err = (m) => { $('#err').hidden = false; $('#err').lastElementChild.textContent = m; };
  f.addEventListener('submit', async (e) => {
    e.preventDefault();
    $('#err').hidden = true;
    if (!f.username.value || !f.password.value) return err('Enter your username and password.');
    await busy(f.querySelector('[type=submit]'), async () => {
      try {
        const r = await post('/api/login', { username: f.username.value.trim(), password: f.password.value });
        if (r.twoFactor) return showTwoFactor(r.token);
        state.me = { user: r.user, role: r.role };
        startApp();
      } catch (x) {
        err(x.status === 429 ? 'Too many attempts. Wait a minute and try again.' : x.message);
        f.password.value = '';
        f.password.focus();
      }
    });
  });
}

function showTwoFactor(token) {
  authFrame(`<form id="tfa" novalidate style="display:grid;gap:16px">
    <div><h1>Two-factor code</h1><p class="lead">Enter the 6-digit code from your authenticator app, or a recovery code.</p></div>
    <div class="auth-alert" id="err" hidden>${icon('circle-alert')}<span></span></div>
    <input class="input code-input" name="code" inputmode="numeric" autocomplete="one-time-code" placeholder="000000" maxlength="12" required>
    <button class="btn btn-primary btn-block" type="submit">Verify</button>
    <button class="btn btn-ghost btn-block" type="button" id="back">Back to sign in</button>
  </form>`);
  const f = $('#tfa');
  f.code.focus();
  $('#back').addEventListener('click', () => showSignIn());
  f.addEventListener('submit', async (e) => {
    e.preventDefault();
    await busy(f.querySelector('[type=submit]'), async () => {
      try {
        const r = await post('/api/login/2fa', { token, code: f.code.value.trim() });
        state.me = { user: r.user, role: r.role };
        startApp();
      } catch (x) {
        $('#err').hidden = false;
        $('#err').lastElementChild.textContent = x.message;
        if (/expired/.test(x.message)) setTimeout(() => showSignIn('Sign-in expired. Start again.'), 1500);
        f.code.select();
      }
    });
  });
}

function strength(pw) {
  let s = 0;
  if (pw.length >= 10) s++;
  if (pw.length >= 14) s++;
  if (/[A-Z]/.test(pw) && /[a-z]/.test(pw)) s++;
  if (/\d/.test(pw) && /[^A-Za-z0-9]/.test(pw)) s++;
  return pw.length < 10 ? Math.min(s, 1) : Math.max(1, s);
}

const PRESETS = [
  { id: 'standard', name: 'Standard', desc: 'Block malware, phishing and scams on every device.', cats: ['malware'], safe: false },
  { id: 'privacy', name: 'Privacy', desc: 'Standard, plus ads and trackers across the network.', cats: ['malware', 'ads'], safe: false },
  { id: 'family', name: 'Family', desc: 'Privacy, plus adult content, gambling, filter bypass, and enforced SafeSearch.', cats: ['malware', 'ads', 'adult', 'gambling', 'bypass'], safe: true },
];

function showSetup() {
  let step = 1;
  const draw = () => {
    authFrame(`<form id="setup" novalidate style="display:grid;gap:16px">
      <div class="steps"><i class="on"></i><i class="${step === 2 ? 'on' : ''}"></i></div>
      <div><h1>${step === 1 ? 'Create the admin account' : 'Choose a starting point'}</h1>
        <p class="lead">${step === 1 ? 'This account manages everything on the gateway.' : 'Applies to every device. Profiles can refine it later.'}</p></div>
      <div class="auth-alert" id="err" hidden>${icon('circle-alert')}<span></span></div>
      ${step === 1 ? `
        <label class="field"><span>Username</span>
          <div class="input-wrap">${icon('user')}<input class="input" name="username" value="admin" autocomplete="username" autocapitalize="none" spellcheck="false" required></div></label>
        ${passwordField('password', 'Password', 'new-password')}
        <div><div class="strength" id="strength"><i></i><i></i><i></i><i></i></div>
          <div class="t-xs t-3" style="margin-top:6px">At least 10 characters. A passphrase works well.</div></div>
        ${passwordField('confirm', 'Confirm password', 'new-password')}
        <div class="t-xs t-warning row" data-caps hidden>${icon('triangle-alert', 'icon-sm')}Caps Lock is on</div>
        <button class="btn btn-primary btn-block" type="submit">Continue</button>
      ` : `
        <label class="field"><span>Network name</span>
          <input class="input" name="boxName" value="${esc(state.boxName === 'Fengard' ? 'Home network' : state.boxName)}" maxlength="40" required>
          <span class="help">Shown in the dashboard and on the block page.</span></label>
        <div class="field"><span>Protection for everyone</span>
          ${PRESETS.map((p, i) => `<label class="option"><input type="radio" name="preset" value="${p.id}" ${i === 0 ? 'checked' : ''}>
            <span><b>${p.name}</b><small>${p.desc}</small></span></label>`).join('')}</div>
        <button class="btn btn-primary btn-block" type="submit">Finish setup</button>
        <button class="btn btn-ghost btn-block" type="button" id="skip">Skip for now</button>
      `}
    </form>`);
    const f = $('#setup');
    const err = (m) => { $('#err').hidden = false; $('#err').lastElementChild.textContent = m; };
    if (step === 1) {
      wirePasswordUI(f);
      f.password.focus();
      f.password.addEventListener('input', () => { $('#strength').className = 'strength s' + strength(f.password.value); });
      f.addEventListener('submit', async (e) => {
        e.preventDefault();
        $('#err').hidden = true;
        if (f.password.value.length < 10) return err('Password must be at least 10 characters.');
        if (f.password.value !== f.confirm.value) return err("Passwords don't match.");
        await busy(f.querySelector('[type=submit]'), async () => {
          try {
            const r = await post('/api/setup', { username: f.username.value.trim(), password: f.password.value });
            state.me = { user: r.user, role: r.role };
            step = 2;
            draw();
          } catch (x) { err(x.message); }
        });
      });
    } else {
      $('#skip').addEventListener('click', startApp);
      f.addEventListener('submit', async (e) => {
        e.preventDefault();
        const preset = PRESETS.find((p) => p.id === f.preset.value);
        await busy(f.querySelector('[type=submit]'), async () => {
          try {
            const settings = await get('/api/settings');
            await put('/api/settings', { ...settings, boxName: f.boxName.value.trim() || 'Fengard' });
            const groups = await get('/api/groups');
            const def = groups.find((g) => g.default);
            if (def) {
              const { devices, default: _, ...g } = def;
              await put(`/api/groups/${def.id}`, { ...g, categories: preset.cats, safeSearch: preset.safe });
            }
            state.boxName = f.boxName.value.trim();
            startApp();
            toast('Fengard is protecting your network');
          } catch (x) { err(x.message); }
        });
      });
    }
  };
  draw();
}

// app shell

let shellTimer = 0;

async function startApp() {
  const collapsed = pref.get('fg-sidebar') === 'collapsed';
  $('#root').innerHTML = `
  <div class="app ${collapsed ? 'collapsed' : ''}" id="app">
    <aside class="sidebar" aria-label="Main navigation">
      <div class="sb-brand">
        <img class="sb-mark" src="img/logo.svg" alt="" style="filter:${logoFilter()}">
        <div class="sb-brand-text"><div class="sb-word">FENGARD</div><div class="sb-box truncate" id="sb-box">${esc(state.boxName)}</div></div>
      </div>
      <nav class="sb-nav">${PAGES.map((p) => p.section
        ? `<div class="sb-section">${p.section}</div>`
        : `<a class="sb-link" href="#${p.id}" data-page="${p.id}" data-tip="${p.label}">${icon(p.icon)}<span class="sb-label">${p.label}</span></a>`).join('')}
      </nav>
      <div class="sb-foot">
        <button class="sb-collapse" id="collapse" data-tip="Expand" aria-label="Collapse sidebar">
          ${icon(collapsed ? 'panel-left-open' : 'panel-left-close')}<span class="sb-label">Collapse</span></button>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <button class="icon-btn tb-menu" id="mobile-menu" aria-label="Open menu">${icon('list')}</button>
        <div class="tb-title"><div class="tb-crumb" id="crumb"></div><h1 id="title"></h1></div>
        <button class="tb-search" id="search-btn">${icon('search')}<span>Search</span><span class="kbd">Ctrl K</span></button>
        <button class="status-pill ok" id="status" data-menu-anchor><span class="dot"></span><span class="label">Protected</span></button>
        <button class="icon-btn" id="alerts-btn" aria-label="Alerts" data-menu-anchor>${icon('bell')}<span class="dot" id="alerts-dot" hidden></span></button>
        <button class="icon-btn" id="theme-btn" aria-label="Toggle theme">${icon(document.documentElement.dataset.theme === 'light' ? 'moon' : 'sun')}</button>
        <button class="user-btn" id="user-btn" aria-label="Account menu" data-menu-anchor><span class="avatar">${esc(state.me.user.slice(0, 2))}</span></button>
      </header>
      <main class="content" id="view"></main>
    </div>
  </div>`;

  const app = $('#app');
  $('#collapse').addEventListener('click', () => {
    const c = app.classList.toggle('collapsed');
    pref.set('fg-sidebar', c ? 'collapsed' : 'expanded');
    $('#collapse').innerHTML = `${icon(c ? 'panel-left-open' : 'panel-left-close')}<span class="sb-label">Collapse</span>`;
  });
  $('#mobile-menu').addEventListener('click', () => app.classList.toggle('mobile-open'));
  app.addEventListener('click', (e) => { if (e.target.closest('.sb-link')) app.classList.remove('mobile-open'); });
  $('#theme-btn').addEventListener('click', toggleTheme);
  $('#user-btn').addEventListener('click', (e) => userMenu(e.currentTarget));
  $('#alerts-btn').addEventListener('click', (e) => alertsPopover(e.currentTarget));
  $('#search-btn').addEventListener('click', openPalette);
  $('#status').addEventListener('click', (e) => protectionMenu(e.currentTarget));

  try { state.categories = await get('/api/categories'); } catch {}
  if (!location.hash || !pageById[routeOf()]) location.hash = '#dashboard';
  render();
  refreshShell();
  shellTimer = setInterval(() => { if (!document.hidden) refreshShell(); }, 15000);
}

document.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k' && state.me) {
    e.preventDefault();
    openPalette();
  }
});

function toggleTheme() {
  const t = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
  document.documentElement.dataset.theme = t;
  pref.set('fg-theme', t);
  $('#theme-btn').innerHTML = icon(t === 'light' ? 'moon' : 'sun');
  $$('img[src="img/logo.svg"]').forEach((i) => (i.style.filter = logoFilter()));
  render(); // charts read theme colors
}

export async function refreshShell() {
  if (!state.me) return;
  try {
    const [o, a] = await Promise.all([get('/api/overview'), get('/api/alerts')]);
    state.shell = {
      pending: o.devices.pending, unread: a.unread, firewallError: o.firewall.error, online: true, overview: o,
      paused: o.protection?.paused, pausedUntil: o.protection?.pausedUntil,
    };
    if (o.system.boxName && o.system.boxName !== state.boxName) {
      state.boxName = o.system.boxName;
      $('#sb-box').textContent = state.boxName;
    }
  } catch {
    state.shell.online = false;
  }
  const dot = $('#alerts-dot');
  if (!dot) return;
  dot.hidden = !state.shell.unread;
  dot.textContent = state.shell.unread > 9 ? '9+' : state.shell.unread;
  const link = $('.sb-link[data-page="devices"]');
  link.querySelector('.count')?.remove();
  link.classList.toggle('has-count', !!state.shell.pending);
  if (state.shell.pending) link.insertAdjacentHTML('beforeend', `<span class="count">${state.shell.pending}</span>`);
  const st = $('#status');
  const [cls, label] = !state.shell.online ? ['bad', 'Offline']
    : state.shell.firewallError ? ['bad', 'Firewall error']
    : state.shell.paused ? ['off', `Paused until ${until(state.shell.pausedUntil)}`]
    : state.shell.pending ? ['warn', `${state.shell.pending} pending`]
    : ['ok', 'Protected'];
  st.className = `status-pill ${cls}`;
  st.querySelector('.label').textContent = label;
}

function protectionMenu(anchor) {
  if (!isAdmin()) return (location.hash = '#firewall');
  const pause = (m, label) => ({ label, icon: 'pause', onClick: async () => {
    if (await attempt(() => post('/api/protection/pause', { minutes: m }), `Protection paused for ${label.toLowerCase()}`)) { refreshShell(); render(); }
  } });
  menu(anchor, state.shell.paused
    ? [{ html: `<div class="menu-head"><b>Protection paused</b><div class="t-xs t-3">Until ${until(state.shell.pausedUntil)}. Devices can reach everything.</div></div>` },
       { label: 'Resume protection', icon: 'play', onClick: async () => { if (await attempt(() => post('/api/protection/pause', { minutes: 0 }), 'Protection resumed')) { refreshShell(); render(); } } }]
    : [{ html: `<div class="menu-head"><b>Protection is on</b><div class="t-xs t-3">Pause all filtering if something breaks. Device pauses still apply.</div></div>` },
       { header: 'Pause filtering' }, pause(10, '10 minutes'), pause(30, '30 minutes'), pause(60, '1 hour'),
       'divider', { label: 'Firewall status', icon: 'brick-wall', onClick: () => (location.hash = '#firewall') }]);
}

function userMenu(anchor) {
  menu(anchor, [
    { html: `<div class="menu-head"><b>${esc(state.me.user)}</b><div class="t-xs t-3">${state.me.role === 'admin' ? 'Administrator' : 'Read-only viewer'}</div></div>` },
    { label: 'Account & security', icon: 'user', onClick: () => (location.hash = '#settings?tab=account') },
    { label: document.documentElement.dataset.theme === 'light' ? 'Dark theme' : 'Light theme', icon: 'moon', onClick: toggleTheme },
    'divider',
    { label: 'Sign out', icon: 'log-out', onClick: signOut },
  ]);
}

async function signOut() {
  await post('/api/logout').catch(() => {});
  state.me = null;
  clearInterval(shellTimer);
  showSignIn();
}

const SEV_ICON = { info: 'info', warning: 'triangle-alert', critical: 'octagon-x' };
export const alertIcon = (a) => ({ new_device: 'monitor-smartphone', access_request: 'send', time_request: 'timer', login_failed: 'key-round', dns_flood: 'zap', upstream_down: 'unplug', test: 'bell' }[a.kind] || SEV_ICON[a.severity] || 'info');

async function alertsPopover(anchor) {
  if ($('.popover')) return closeMenus();
  const el = popover(anchor, `<div class="panel-head"><h3>Alerts</h3><div class="actions"><button class="btn btn-ghost btn-sm" id="pop-read">Mark all read</button></div></div>
    <div class="feed" id="pop-feed"><div style="padding:14px"><div class="skeleton" style="height:36px"></div></div></div>
    <div class="panel-foot"><a href="#alerts" class="btn btn-sm" style="width:100%">All alerts</a></div>`);
  const data = await get('/api/alerts').catch(() => ({ alerts: [] }));
  const list = data.alerts.slice(0, 6);
  $('#pop-feed', el).innerHTML = list.length ? list.map((a) => `
    <div class="feed-item"><div class="feed-ico sev-${a.severity}">${icon(alertIcon(a), 'icon-sm')}</div>
      <div class="feed-text truncate"><b class="truncate">${esc(a.title)}</b><small class="truncate">${esc(a.detail || '')}</small></div>
      <div class="feed-time">${ago(a.time)}</div></div>`).join('')
    : `<p class="t-3 t-sm" style="padding:20px;text-align:center">Nothing needs your attention.</p>`;
  $('#pop-read', el).addEventListener('click', async () => { await post('/api/alerts/read'); closeMenus(); refreshShell(); });
  el.addEventListener('click', (e) => { if (e.target.closest('a')) closeMenus(); });
}

// command palette

async function openPalette() {
  if ($('.palette')) return;
  const backdrop = document.createElement('div');
  backdrop.className = 'backdrop';
  const el = document.createElement('div');
  el.className = 'palette';
  el.setAttribute('role', 'dialog');
  el.setAttribute('aria-label', 'Search');
  el.innerHTML = `<div class="palette-input">${icon('search')}<input placeholder="Pages, devices, or a domain to check…" aria-label="Search" autocomplete="off" spellcheck="false"><span class="kbd">Esc</span></div><div class="palette-list" role="listbox"></div>`;
  document.body.append(backdrop, el);
  const input = $('input', el), list = $('.palette-list', el);
  const prev = document.activeElement;
  const close = () => { backdrop.remove(); el.remove(); prev?.focus?.(); };
  backdrop.addEventListener('click', close);

  let devices = [];
  get('/api/devices').then((d) => { devices = d; draw(); }).catch(() => {});
  let items = [], sel = 0;
  const draw = () => {
    const q = input.value.trim().toLowerCase();
    const pages = PAGES.filter((p) => p.id && (!q || p.label.toLowerCase().includes(q)))
      .map((p) => ({ group: 'Pages', icon: p.icon, label: p.label, run: () => (location.hash = '#' + p.id) }));
    const devs = devices.filter((d) => q && `${deviceName(d)} ${d.hostname || ''} ${d.mac} ${(d.ips || []).join(' ')} ${d.vendor || ''}`.toLowerCase().includes(q))
      .slice(0, 6).map((d) => ({ group: 'Devices', icon: deviceType(d).icon, label: deviceName(d), hint: (d.ips || [])[0] || d.mac, run: () => (location.hash = `#devices?mac=${d.mac}`) }));
    const domain = /^[a-z0-9.-]+\.[a-z]{2,}$/i.test(q)
      ? [{ group: 'Check', icon: 'scan-search', label: `Why is ${q} allowed or blocked?`, run: () => (location.hash = `#filtering?check=${encodeURIComponent(q)}`) },
         { group: 'Check', icon: 'activity', label: `Activity for ${q}`, run: () => (location.hash = `#activity?search=${encodeURIComponent(q)}`) }]
      : [];
    const actions = isAdmin() && (!q || 'block a domain'.includes(q) || 'pause protection'.includes(q))
      ? [{ group: 'Actions', icon: 'ban', label: 'Block a domain…', run: () => (location.hash = '#filtering?add=block') }]
      : [];
    items = [...domain, ...devs, ...pages, ...actions];
    sel = Math.min(sel, Math.max(0, items.length - 1));
    let group = '';
    list.innerHTML = items.length ? items.map((it, i) => {
      const head = it.group !== group ? `<div class="palette-group">${(group = it.group)}</div>` : '';
      return `${head}<div class="palette-item ${i === sel ? 'on' : ''}" role="option" data-i="${i}">${icon(it.icon)}<span class="truncate">${esc(it.label)}</span>${it.hint ? `<small>${esc(it.hint)}</small>` : ''}</div>`;
    }).join('') : `<p class="t-3 t-sm" style="padding:14px">No results</p>`;
  };
  const run = (i) => { const it = items[i]; if (it) { close(); it.run(); } };
  input.addEventListener('input', () => { sel = 0; draw(); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { sel = Math.min(sel + 1, items.length - 1); draw(); e.preventDefault(); }
    else if (e.key === 'ArrowUp') { sel = Math.max(sel - 1, 0); draw(); e.preventDefault(); }
    else if (e.key === 'Enter') run(sel);
    else if (e.key === 'Escape') close();
  });
  list.addEventListener('click', (e) => { const it = e.target.closest('[data-i]'); if (it) run(Number(it.dataset.i)); });
  draw();
  input.focus();
}

// router

export const routeOf = () => location.hash.slice(1).split('?')[0] || 'dashboard';
export const routeParams = () => new URLSearchParams(location.hash.split('?')[1] || '');
let renderSeq = 0;

export async function render() {
  if (!state.me || !$('#view')) return;
  const id = pageById[routeOf()] ? routeOf() : 'dashboard';
  const page = pageById[id];
  const seq = ++renderSeq;
  $$('.sb-link').forEach((a) => a.classList.toggle('on', a.dataset.page === id));
  $('#title').textContent = page.label;
  $('#crumb').textContent = PAGES.slice(0, PAGES.indexOf(page)).reverse().find((p) => p.section)?.section || '';
  document.title = `${page.label} · ${state.boxName}`;
  closeMenus();

  // fresh container per render so a slow page writes into a detached el not over the current one
  const el = document.createElement('div');
  el.className = 'page';
  $('#view').replaceChildren(el);
  const ctx = {
    params: routeParams(),
    every(ms, fn) {
      const t = setInterval(() => {
        if (!el.isConnected) return clearInterval(t);
        if (!document.hidden) fn().catch(() => {});
      }, ms);
      return () => clearInterval(t);
    },
    alive: () => el.isConnected && seq === renderSeq,
    rerender: render,
  };
  try {
    const mod = await loaders[id]();
    if (!ctx.alive()) return;
    await mod.render(el, ctx);
  } catch (e) {
    if (ctx.alive() && state.me) {
      el.innerHTML = `<div class="panel"><div class="empty">${icon('circle-alert', 'icon-lg')}<h4>Couldn't load this page</h4><p>${esc(e.message)}</p>
        <button class="btn btn-sm" data-retry style="margin-top:8px">Try again</button></div></div>`;
      $('[data-retry]', el).addEventListener('click', render);
    }
  }
}

window.addEventListener('hashchange', () => { closeOverlays(); if (state.me) render(); });

boot();
