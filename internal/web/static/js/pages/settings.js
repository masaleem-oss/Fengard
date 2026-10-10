import { api, get, post, put, del } from '../api.js';
import { $, $$, esc, icon, dateTime, ago, isSet, attempt, busy, modal, confirmDialog, empty, copyText, toast, switchInput } from '../ui.js';
import { platformTiles, wirePlatformTiles, trustStatus, trustBadge } from '../cert.js';
import { state, isAdmin, refreshShell } from '../main.js';
import { startUpdate } from '../update.js';

const TABS = [
  ['general', 'settings', 'General', true],
  ['account', 'user', 'Account & security', false],
  ['users', 'users', 'Users', true],
  ['notifications', 'bell', 'Notifications', true],
  ['keys', 'key-round', 'API keys', true],
  ['certificate', 'badge-check', 'Certificate', false],
  ['backup', 'archive', 'Backup & history', true],
  ['audit', 'clipboard-list', 'Audit log', true],
];

export async function render(el, ctx) {
  const admin = isAdmin();
  const tabs = TABS.filter(([, , , adminOnly]) => admin || !adminOnly);
  let tab = ctx.params.get('tab');
  if (!tabs.find((t) => t[0] === tab)) tab = tabs[0][0];
  el.innerHTML = `<div class="panel">
    <div class="tabs">${tabs.map(([id, ic, l]) => `<button data-tab="${id}" class="${tab === id ? 'on' : ''}">${icon(ic, 'icon-sm')}${l}</button>`).join('')}</div>
    <div id="tab"></div></div>`;
  $$('[data-tab]', el).forEach((b) => b.addEventListener('click', () => { location.hash = `#settings?tab=${b.dataset.tab}`; }));
  await ({ general, account, users, notifications, keys, certificate, backup, audit })[tab]($('#tab', el), ctx);
}

async function saveSettings(patch, msg = 'Settings saved') {
  const current = await get('/api/settings');
  await put('/api/settings', { ...current, ...patch });
  toast(msg);
}

// updates

async function updates(box) {
  const draw = async () => {
    const u = await get('/api/update').catch(() => null);
    if (!u) return;
    const admin = isAdmin();
    let line = 'Not checked yet.';
    if (u.disabled) line = 'Update checks are turned off.';
    else if (u.installing) line = `Updating to ${esc(u.latest)}. Fengard restarts in a moment and DNS keeps working while it does.`;
    else if (u.available) line = `<b>Fengard ${esc(u.latest)} is available.</b>${u.canInstall ? '' : ' Download the new kit from GitHub to install it.'}`;
    else if (u.newer && !u.supported) line = `Fengard ${esc(u.latest)} is out but doesn't support this router. ${esc(u.reason || '')}`;
    else if (isSet(u.checkedAt) && u.latest) line = 'You have the latest version.';
    box.innerHTML = `<div class="section-title">Updates</div>
      <dl class="kv" style="margin-bottom:10px">
        <dt>This router</dt><dd>Fengard ${esc(u.current)} <span class="t-3 mono t-xs">${esc(u.target || '')}</span></dd>
        <dt>Latest</dt><dd>${u.latest ? esc(u.latest) : '<span class="t-3">unknown</span>'}${u.url ? ` · <a href="${esc(u.url)}" target="_blank" rel="noopener">what's new</a>` : ''}${isSet(u.checkedAt) ? ` <span class="t-3 t-xs">checked ${ago(u.checkedAt).toLowerCase()}</span>` : ''}</dd>
      </dl>
      <p class="t-2 t-sm">${line}</p>
      ${u.error ? `<p class="t-sm" style="color:var(--danger)">${esc(u.error)}</p>` : ''}
      ${admin && !u.disabled ? `<div class="row" style="margin:12px 0">
          <button class="btn btn-sm" type="button" id="up-check">${icon('refresh-cw', 'icon-sm')}Check now</button>
          ${u.available && u.canInstall && !u.installing ? `<button class="btn btn-sm btn-primary" type="button" id="up-go">${icon('download', 'icon-sm')}Update to ${esc(u.latest)}</button>` : ''}
        </div>
        <div class="setting" style="padding-left:0;padding-right:0"><div class="setting-text"><b>Install updates automatically</b><small>New releases install overnight, between 3 and 5 am, once Fengard has checked they support this router. If a new version doesn't start, the old one comes back on its own.</small></div>
          ${switchInput('id="up-auto"', u.autoUpdate, 'Install updates automatically')}</div>` : ''}`;
    $('#up-check', box)?.addEventListener('click', (e) => busy(e.currentTarget, () => attempt(async () => { await post('/api/update/check'); await draw(); })));
    $('#up-go', box)?.addEventListener('click', () => startUpdate(u, draw));
    $('#up-auto', box)?.addEventListener('change', (e) => attempt(() => saveSettings({ autoUpdate: e.target.checked },
      e.target.checked ? 'Updates will install automatically' : 'Automatic updates turned off')));
  };
  await draw();
}

// general

async function general(body) {
  const [s, groups] = await Promise.all([get('/api/settings'), get('/api/groups')]);
  const zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
  body.innerHTML = `<form class="form-grid panel-body" id="f" novalidate>
    <label class="field"><span>Network name</span><input class="input" name="boxName" value="${esc(s.boxName)}" maxlength="40" required><span class="help">Shown in the sidebar, on sign-in and on the block page.</span></label>
    <label class="field"><span>Profile for new devices</span><select class="select" name="defaultGroup">${groups.map((g) => `<option value="${g.id}" ${g.id === s.defaultGroup ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}</select></label>
    <label class="field"><span>Time zone for schedules</span><input class="input" name="timezone" value="${esc(s.timezone)}" list="zones" spellcheck="false">
      <datalist id="zones"><option value="Local">${zones.map((z) => `<option value="${esc(z)}">`).join('')}</datalist>
      <span class="help">“Local” uses the gateway's clock. This browser is in ${esc(Intl.DateTimeFormat().resolvedOptions().timeZone)}.</span></label>
    <label class="field"><span>Keep activity history</span><div class="row"><input class="input" name="logRetentionDays" type="number" min="1" max="365" value="${s.logRetentionDays}" style="width:110px"><span class="t-2">days</span></div></label>
    <label class="field"><span>Per-device DNS limit</span><div class="row"><input class="input" name="clientRateQps" type="number" min="5" max="10000" value="${s.clientRateQps}" style="width:110px"><span class="t-2">queries per second</span></div>
      <span class="help">Normal devices use under 20. Raise it only for servers that make many lookups.</span></label>
    <div><button class="btn btn-primary" type="submit">Save changes</button></div></form>
    <div class="panel-body" id="upd" style="border-top:1px solid var(--border)"></div>`;
  updates($('#upd', body));
  const f = $('#f', body);
  f.addEventListener('submit', async (e) => {
    e.preventDefault();
    await busy(f.querySelector('[type=submit]'), () => attempt(async () => {
      await saveSettings({ boxName: f.boxName.value.trim(), defaultGroup: f.defaultGroup.value, timezone: f.timezone.value.trim() || 'Local', logRetentionDays: +f.logRetentionDays.value, clientRateQps: +f.clientRateQps.value });
      state.boxName = f.boxName.value.trim();
      refreshShell();
    }));
  });
}

// account and security

async function account(body) {
  const tfa = await get('/api/2fa');
  const https = location.protocol === 'https:';
  const set = isAdmin() ? await get('/api/settings').catch(() => null) : null;
  body.innerHTML = `
    <div class="section"><div class="row" style="gap:12px"><span class="avatar" style="width:40px;height:40px;font-size:14px">${esc(state.me.user.slice(0, 2))}</span>
      <div><b>${esc(state.me.user)}</b><div class="t-sm t-3">${state.me.role === 'admin' ? 'Administrator' : 'Read-only viewer'} · sessions end after 1 hour idle or 12 hours</div></div></div></div>
    <div class="section" id="tfa">
      <div class="row" style="align-items:flex-start">
        <div class="spacer"><b>Two-factor authentication</b>
          <p class="t-sm t-3">${tfa.enabled ? `On. ${tfa.recoveryLeft} recovery code${tfa.recoveryLeft === 1 ? '' : 's'} left.` : 'Off. A code from your phone is required to sign in, so a stolen password alone is not enough.'}</p></div>
        ${tfa.enabled ? `<button class="btn btn-sm" id="tfa-off">Turn off</button>` : `<button class="btn btn-primary btn-sm" id="tfa-on">Set up</button>`}
      </div>
    </div>
    <div class="section">
      <b>Dashboard over HTTPS</b>
      <p class="t-sm t-3" style="margin-bottom:8px">${https ? 'This connection is encrypted with the gateway\'s own certificate.' : 'This connection is plain HTTP. Once the Fengard certificate is installed on this computer the secure address works without warnings.'}</p>
      ${https ? '' : `<a class="btn btn-sm" href="https://${location.hostname}${location.port === '80' || !location.port ? '' : ':443'}/">${icon('lock', 'icon-sm')}Open secure dashboard</a>`}
      ${set ? `<div class="setting" style="padding-left:0;padding-right:0"><div class="setting-text"><b>Require HTTPS</b><small>${https || set.requireHttps ? 'Plain HTTP stops working for the dashboard, even on your own network. The block page and certificate downloads still work.' : 'Open the dashboard over HTTPS to turn this on, so you can\'t lock yourself out.'}</small></div>
        ${switchInput(`id="req-https"${https || set.requireHttps ? '' : ' disabled'}`, set.requireHttps, 'Require HTTPS')}</div>` : ''}
    </div>
    <form class="section form-grid" id="pw" novalidate>
      <b>Change password</b>
      <label class="field"><span>Current password</span><input class="input" type="password" name="current" autocomplete="current-password" required></label>
      <label class="field"><span>New password</span><input class="input" type="password" name="next" autocomplete="new-password" minlength="10" required><span class="help">At least 10 characters. You'll be signed out everywhere.</span></label>
      <label class="field"><span>Confirm new password</span><input class="input" type="password" name="confirm" autocomplete="new-password" required></label>
      <p class="t-sm t-danger" id="err" hidden></p>
      <div><button class="btn" type="submit">Change password</button></div>
    </form>`;
  $('#req-https', body)?.addEventListener('change', (e) => attempt(() => saveSettings({ requireHttps: e.target.checked },
    e.target.checked ? 'The dashboard now needs HTTPS' : 'Plain HTTP works on your network again')).then(() => account(body)));
  $('#tfa-on', body)?.addEventListener('click', () => enroll2FA(() => account(body)));
  $('#tfa-off', body)?.addEventListener('click', () => {
    const m = modal({ title: 'Turn off two-factor authentication', body: `<form id="off"><label class="field"><span>Confirm your password</span><input class="input" type="password" name="password" autocomplete="current-password" required autofocus></label><p class="t-sm t-danger" id="e" hidden></p></form>`,
      foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-danger-solid" id="go">Turn off</button>` });
    const submit = async () => {
      try { await post('/api/2fa/disable', { password: $('#off', m.el).password.value }); m.close(); toast('Two-factor turned off'); account(body); }
      catch (x) { $('#e', m.el).textContent = x.message; $('#e', m.el).hidden = false; }
    };
    $('#go', m.el).addEventListener('click', submit);
    $('#off', m.el).addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  });
  const f = $('#pw', body);
  f.addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = (t) => { $('#err', f).textContent = t; $('#err', f).hidden = false; };
    if (f.next.value.length < 10) return err('New password must be at least 10 characters.');
    if (f.next.value !== f.confirm.value) return err("New passwords don't match.");
    await busy(f.querySelector('[type=submit]'), async () => {
      try { await post('/api/password', { current: f.current.value, new: f.next.value }); toast('Password changed. Sign in again.'); setTimeout(() => location.reload(), 1200); }
      catch (x) { err(x.message); }
    });
  });
}

async function enroll2FA(onDone) {
  let setup;
  try { setup = await post('/api/2fa/setup'); } catch (e) { return toast(e.message, 'error'); }
  const { default: qrcode } = await import('../vendor-qrcode.mjs');
  const qr = qrcode(0, 'M');
  qr.addData(setup.uri);
  qr.make();
  const m = modal({
    title: 'Set up two-factor authentication', wide: true,
    body: `<div class="grid g-2" style="align-items:start">
      <div><div class="qr">${qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true })}</div>
        <p class="t-xs t-3" style="margin-top:8px">Can't scan? Enter this key by hand:</p><div class="fingerprint" style="margin-top:4px">${esc(setup.secret)}</div></div>
      <form id="en" class="stack" novalidate>
        <ol class="cert-steps"><li><span>Open an authenticator app: Google Authenticator, 1Password, Authy, Bitwarden or Microsoft Authenticator.</span></li>
          <li><span>Scan the code, or type the key.</span></li><li><span>Enter the 6-digit code the app shows.</span></li></ol>
        <input class="input code-input" name="code" inputmode="numeric" autocomplete="one-time-code" placeholder="000000" maxlength="6" required>
        <p class="t-sm t-danger" id="e" hidden></p>
        <button class="btn btn-primary" type="submit">Turn on</button></form></div>`,
  });
  const f = $('#en', m.el);
  f.code.focus();
  f.addEventListener('submit', async (e) => {
    e.preventDefault();
    await busy(f.querySelector('[type=submit]'), async () => {
      try {
        const r = await post('/api/2fa/enable', { code: f.code.value.trim() });
        m.close();
        const rm = modal({ title: 'Save your recovery codes', subtitle: 'Each works once if you lose your phone. This is the only time they are shown.',
          body: `<div class="recovery">${r.recoveryCodes.map((c) => `<span>${esc(c)}</span>`).join('')}</div>`,
          foot: `<button class="btn" id="cp">${icon('copy', 'icon-sm')}Copy</button><button class="btn btn-primary" data-close>I've saved them</button>`, onClose: onDone });
        $('#cp', rm.el).addEventListener('click', () => copyText(r.recoveryCodes.join('\n')));
      } catch (x) { $('#e', f).textContent = x.message; $('#e', f).hidden = false; f.code.select(); }
    });
  });
}

// users

async function users(body) {
  const list = await get('/api/users');
  body.innerHTML = `
    <div class="toolbar"><span class="t-sm t-3">Admins can change everything. Viewers can see the dashboard and activity but change nothing.</span><span class="spacer"></span>
      <button class="btn btn-primary btn-sm" id="add">${icon('user-plus', 'icon-sm')}Add user</button></div>
    <table class="table"><thead><tr><th>User</th><th>Role</th><th>Two-factor</th><th>Created</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
      ${list.map((u) => `<tr><td><div class="cell-main"><span class="avatar">${esc(u.username.slice(0, 2))}</span><span class="cell-title">${esc(u.username)}</span>${u.username === state.me.user ? '<span class="tag">You</span>' : ''}</div></td>
        <td>${u.role === 'admin' ? '<span class="badge b-accent">Administrator</span>' : '<span class="badge b-neutral">Viewer</span>'}</td>
        <td>${u.twoFactor ? '<span class="badge b-success">On</span>' : '<span class="t-3">Off</span>'}</td>
        <td class="t-2">${dateTime(u.created)}</td>
        <td class="col-actions">${u.username === state.me.user ? '' : `${u.twoFactor ? `<button class="btn btn-ghost btn-sm row-actions" data-reset="${esc(u.username)}">Reset 2FA</button>` : ''}<button class="btn btn-ghost btn-sm btn-danger row-actions" data-del="${esc(u.username)}">Remove</button>`}</td></tr>`).join('')}
    </tbody></table>`;
  $('#add', body).addEventListener('click', () => {
    const m = modal({ title: 'Add user',
      body: `<form id="uf" class="stack" novalidate>
        <label class="field"><span>Username</span><input class="input" name="username" autocapitalize="none" spellcheck="false" required autofocus></label>
        <label class="field"><span>Password</span><input class="input" type="password" name="password" autocomplete="new-password" required><span class="help">At least 10 characters.</span></label>
        <div class="field"><span>Role</span>
          <label class="option"><input type="radio" name="role" value="viewer" checked><span><b>Viewer</b><small>Sees everything, changes nothing.</small></span></label>
          <label class="option"><input type="radio" name="role" value="admin"><span><b>Administrator</b><small>Full control, including users and backups.</small></span></label></div>
        <p class="t-sm t-danger" id="uerr" hidden></p></form>`,
      foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" id="usave">Add user</button>` });
    const f = $('#uf', m.el);
    const submit = async () => {
      try { await post('/api/users', { username: f.username.value.trim(), password: f.password.value, role: f.role.value }); m.close(); toast('User added'); users(body); }
      catch (x) { $('#uerr', f).textContent = x.message; $('#uerr', f).hidden = false; }
    };
    $('#usave', m.el).addEventListener('click', submit);
    f.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  });
  body.onclick = async (e) => {
    const d = e.target.closest('[data-del]'), r = e.target.closest('[data-reset]');
    if (d) {
      const ok = await confirmDialog({ title: `Remove ${d.dataset.del}?`, danger: true, confirm: 'Remove user', message: 'They are signed out and lose access.' });
      if (ok && await attempt(() => del(`/api/users/${encodeURIComponent(d.dataset.del)}`), 'User removed')) users(body);
    }
    if (r) {
      const ok = await confirmDialog({ title: `Reset two-factor for ${r.dataset.reset}?`, confirm: 'Reset', message: 'They can sign in with their password alone until they set it up again.' });
      if (ok && await attempt(() => post(`/api/users/${encodeURIComponent(r.dataset.reset)}/reset-2fa`), 'Two-factor reset')) users(body);
    }
  };
}

// notifications

const TYPES = [
  ['discord', 'Discord', "Webhook URL from a channel's Integrations settings."],
  ['slack', 'Slack', 'Incoming webhook URL.'],
  ['telegram', 'Telegram', 'A bot token from @BotFather and the chat ID to post to.'],
  ['ntfy', 'ntfy', 'Topic URL, e.g. https://ntfy.sh/fengard-home. Push to phones for free.'],
  ['webhook', 'Webhook', 'Any HTTPS endpoint. Receives JSON with kind, severity, title and detail.'],
];

async function notifications(body) {
  const list = await get('/api/channels');
  body.innerHTML = `
    <div class="toolbar"><span class="t-sm t-3">Where alerts are sent. Each channel picks the lowest severity it wants.</span><span class="spacer"></span>
      <button class="btn btn-primary btn-sm" id="add">${icon('plus', 'icon-sm')}Add channel</button></div>
    ${list.length ? `<table class="table"><thead><tr><th>Channel</th><th>Type</th><th>Minimum level</th><th>Last sent</th><th>Enabled</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
      ${list.map((c) => `<tr data-id="${c.id}"><td class="cell-title">${esc(c.name)}${c.lastError ? `<div class="cell-sub t-danger">${esc(c.lastError)}</div>` : ''}</td>
        <td><span class="tag">${esc(TYPES.find((t) => t[0] === c.type)?.[1] || c.type)}</span></td>
        <td class="t-2">${c.minLevel}</td><td class="t-2">${isSet(c.lastSent) ? ago(c.lastSent) : 'Never'}${c.sent ? ` · ${c.sent} sent` : ''}</td>
        <td>${switchInput('data-toggle', c.enabled, `Enable ${c.name}`)}</td>
        <td class="col-actions"><button class="btn btn-ghost btn-sm" data-test>Test</button><button class="btn btn-ghost btn-sm" data-edit>Edit</button><button class="btn btn-ghost btn-sm btn-danger" data-del>${icon('trash-2', 'icon-sm')}</button></td></tr>`).join('')}</tbody></table>`
      : empty({ icon: 'bell', title: 'No channels yet', text: 'Get a Discord, Slack, Telegram or push notification when a new device joins, someone asks for access, or an attack is detected.' })}`;

  const edit = (c) => {
    const isNew = !c;
    c = c || { name: '', type: 'discord', url: '', token: '', chatId: '', minLevel: 'warning', enabled: true };
    const m = modal({ title: isNew ? 'Add channel' : 'Edit channel',
      body: `<form id="cf" class="stack" novalidate>
        <div class="form-row"><label class="field"><span>Name</span><input class="input" name="name" value="${esc(c.name)}" placeholder="Family Discord" required autofocus></label>
          <label class="field"><span>Type</span><select class="select" name="type">${TYPES.map(([id, l]) => `<option value="${id}" ${c.type === id ? 'selected' : ''}>${l}</option>`).join('')}</select></label></div>
        <div id="type-fields"></div>
        <label class="field"><span>Send alerts at or above</span><select class="select" name="minLevel">${['info', 'warning', 'critical'].map((l) => `<option ${c.minLevel === l ? 'selected' : ''}>${l}</option>`).join('')}</select>
          <span class="help">Info: new devices, access requests. Warning: failed sign-ins, quarantined devices. Critical: floods, upstream outages.</span></label>
        <p class="t-sm t-danger" id="cerr" hidden></p></form>`,
      foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" id="csave">${isNew ? 'Add channel' : 'Save'}</button>` });
    const f = $('#cf', m.el);
    const fields = () => {
      const t = f.type.value;
      const help = TYPES.find((x) => x[0] === t)[2];
      $('#type-fields', f).innerHTML = t === 'telegram'
        ? `<div class="form-row"><label class="field"><span>Bot token</span><input class="input mono" name="token" value="" placeholder="${c.token ? '•••••••• (unchanged)' : '123456:ABC…'}" spellcheck="false"></label>
           <label class="field"><span>Chat ID</span><input class="input mono" name="chatId" value="${esc(c.chatId || '')}" spellcheck="false" required></label></div><p class="t-xs t-3">${help}</p>`
        : `<label class="field"><span>URL</span><input class="input mono" name="url" value="${esc(c.url || '')}" placeholder="https://" spellcheck="false" required><span class="help">${help}</span></label>`;
    };
    fields();
    f.type.addEventListener('change', fields);
    const submit = async () => {
      const bodyData = { id: c.id, name: f.name.value.trim(), type: f.type.value, minLevel: f.minLevel.value, enabled: c.enabled,
        url: f.url?.value.trim() || '', token: f.token?.value.trim() || '', chatId: f.chatId?.value.trim() || '' };
      try { await (isNew ? post('/api/channels', bodyData) : put(`/api/channels/${c.id}`, bodyData)); m.close(); toast(isNew ? 'Channel added. Send a test to check it.' : 'Channel saved'); notifications(body); }
      catch (x) { $('#cerr', f).textContent = x.message; $('#cerr', f).hidden = false; }
    };
    $('#csave', m.el).addEventListener('click', submit);
    f.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  };
  $('#add', body).addEventListener('click', () => edit(null));
  body.onclick = async (e) => {
    const row = e.target.closest('tr[data-id]');
    if (!row || e.target.closest('#add')) return;
    const c = list.find((x) => x.id === row.dataset.id);
    if (e.target.closest('[data-edit]')) edit(c);
    if (e.target.closest('[data-test]')) await busy(e.target.closest('[data-test]'), () => attempt(() => post(`/api/channels/${c.id}/test`), `Test sent to ${c.name}`));
    if (e.target.closest('[data-del]')) {
      const ok = await confirmDialog({ title: `Remove ${c.name}?`, danger: true, confirm: 'Remove', message: 'Alerts will no longer be sent there.' });
      if (ok && await attempt(() => del(`/api/channels/${c.id}`), 'Channel removed')) notifications(body);
    }
  };
  body.onchange = async (e) => {
    if (!e.target.matches('[data-toggle]')) return;
    const c = list.find((x) => x.id === e.target.closest('tr').dataset.id);
    if (!await attempt(() => put(`/api/channels/${c.id}`, { ...c, lastSent: undefined, lastError: undefined, sent: undefined, token: '', enabled: e.target.checked }), e.target.checked ? 'Channel enabled' : 'Channel disabled')) e.target.checked = !e.target.checked;
    notifications(body);
  };
}

// api keys

async function keys(body) {
  const list = await get('/api/keys');
  body.innerHTML = `
    <div class="toolbar"><span class="t-sm t-3">For scripts and integrations. Send <code>Authorization: Bearer fg_…</code>; every <code>/api</code> endpoint works.</span><span class="spacer"></span>
      <button class="btn btn-primary btn-sm" id="add">${icon('plus', 'icon-sm')}Create key</button></div>
    ${list.length ? `<table class="table"><thead><tr><th>Name</th><th>Key</th><th>Role</th><th>Created</th><th>Last used</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
      ${list.map((k) => `<tr><td class="cell-title">${esc(k.name)}</td><td class="mono t-2">${esc(k.prefix)}…</td>
        <td>${k.role === 'admin' ? '<span class="badge b-accent">Admin</span>' : '<span class="badge b-neutral">Viewer</span>'}</td>
        <td class="t-2">${dateTime(k.created)}</td><td class="t-2">${isSet(k.lastUsed) ? ago(k.lastUsed) : 'Never'}</td>
        <td class="col-actions"><button class="btn btn-ghost btn-sm btn-danger row-actions" data-del="${k.id}" data-name="${esc(k.name)}">Revoke</button></td></tr>`).join('')}</tbody></table>`
      : empty({ icon: 'key-round', title: 'No API keys', text: 'Create one for Home Assistant, a monitoring script, or your own tools.' })}`;
  $('#add', body).addEventListener('click', () => {
    const m = modal({ title: 'Create API key',
      body: `<form id="kf" class="stack" novalidate>
        <label class="field"><span>Name</span><input class="input" name="name" placeholder="Home Assistant" required autofocus></label>
        <div class="field"><span>Access</span>
          <label class="option"><input type="radio" name="role" value="viewer" checked><span><b>Read-only</b><small>Stats, devices and activity.</small></span></label>
          <label class="option"><input type="radio" name="role" value="admin"><span><b>Full access</b><small>Can change rules, profiles and settings.</small></span></label></div>
        <p class="t-sm t-danger" id="kerr" hidden></p></form>`,
      foot: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" id="ksave">Create</button>` });
    const f = $('#kf', m.el);
    const submit = async () => {
      try {
        const r = await post('/api/keys', { name: f.name.value.trim(), role: f.role.value });
        m.close();
        const show = modal({ title: 'Copy your key now', subtitle: 'It is shown once. Store it somewhere safe.',
          body: `<div class="fingerprint" style="font-size:13px">${esc(r.key)}</div><p class="t-xs t-3" style="margin-top:10px">Example: <code>curl -H "Authorization: Bearer ${esc(r.key.slice(0, 8))}…" ${esc(location.origin)}/api/overview</code></p>`,
          foot: `<button class="btn" id="kc">${icon('copy', 'icon-sm')}Copy</button><button class="btn btn-primary" data-close>Done</button>`, onClose: () => keys(body) });
        $('#kc', show.el).addEventListener('click', () => copyText(r.key));
      } catch (x) { $('#kerr', f).textContent = x.message; $('#kerr', f).hidden = false; }
    };
    $('#ksave', m.el).addEventListener('click', submit);
    f.addEventListener('submit', (e) => { e.preventDefault(); submit(); });
  });
  body.onclick = async (e) => {
    const d = e.target.closest('[data-del]');
    if (!d) return;
    const ok = await confirmDialog({ title: `Revoke “${d.dataset.name}”?`, danger: true, confirm: 'Revoke', message: 'Anything using this key stops working immediately.' });
    if (ok && await attempt(() => del(`/api/keys/${d.dataset.del}`), 'Key revoked')) keys(body);
  };
}

// certificate

async function certificate(body) {
  const c = await get('/api/certificate');
  const admin = isAdmin();
  body.innerHTML = `<div class="grid g-2 panel-body" style="align-items:start">
    <div class="stack">
      <div><div class="row wrap" style="gap:8px"><b>${esc(c.name)}</b><span id="trust"></span></div><p class="t-sm t-3">Valid until ${new Date(c.expires).toLocaleDateString()}. Fengard restricts signing to blocked sites and its dashboard. Installing this root trusts its private key for any website; protect the router and verify the fingerprint through SSH or its console.</p></div>
      <div><div class="row t-xs t-3" style="margin-bottom:6px">SHA-256 fingerprint<button class="btn btn-ghost btn-sm" id="cp" style="margin-left:auto">${icon('copy', 'icon-sm')}Copy</button></div><div class="fingerprint">${esc(c.fingerprint)}</div></div>
      <div class="row wrap"><a class="btn btn-sm" href="/fengard-ca.crt" download>${icon('download', 'icon-sm')}PEM (.crt)</a><a class="btn btn-sm" href="/fengard-ca.der" download>${icon('download', 'icon-sm')}DER (.der)</a><a class="btn btn-sm" href="/fengard.mobileconfig" download>${icon('download', 'icon-sm')}Apple profile</a></div>
      ${admin ? `<div class="section" style="padding-top:14px;border-top:1px solid var(--border)"><b>Keep the same certificate on another gateway</b>
        <p class="t-sm t-3" style="margin:4px 0 10px">Devices trust one certificate. Move it, with its private key, to a replacement or second Fengard box and nothing needs reinstalling. Backups include it too. Keep the file private.</p>
        <div class="row wrap"><a class="btn btn-sm" href="/api/certificate/export" download>${icon('key-round', 'icon-sm')}Export with key</a>
          <label class="btn btn-sm">${icon('upload', 'icon-sm')}Import…<input type="file" id="cafile" accept=".pem,.crt,.key,.txt" hidden></label></div></div>` : ''}
    </div>
    <div><div class="label" style="margin-bottom:8px">Install guides</div><div id="pl">${platformTiles()}</div></div></div>`;
  $('#cp', body).addEventListener('click', () => copyText(c.fingerprint));
  wirePlatformTiles($('#pl', body));
  trustStatus().then((t) => { const b = $('#trust', body); if (b) b.innerHTML = trustBadge(t); });
  $('#cafile', body)?.addEventListener('change', async (e) => {
    const file = e.target.files[0];
    e.target.value = '';
    if (!file) return;
    const ok = await confirmDialog({ title: 'Replace the certificate authority?', confirm: 'Replace', danger: true, message: `The certificate from <b>${esc(file.name)}</b> takes over signing the block page and dashboard. Devices that trust the current one will need the new one unless it came from a box they already trust.` });
    if (ok && await attempt(async () => api('/api/certificate/import', { method: 'POST', raw: await file.text() }), 'Certificate replaced')) certificate(body);
  });
}

// backup

async function backup(body) {
  const hist = await get('/api/config/history');
  body.innerHTML = `
    <div class="section row wrap" style="gap:16px">
      <div class="spacer"><b>Configuration backup</b><p class="t-sm t-3">Devices, profiles, rules, records and settings as JSON. Accounts and history are not included.</p></div>
      <a class="btn btn-sm" href="/api/config/export" download>${icon('download', 'icon-sm')}Download</a>
      <label class="btn btn-sm">${icon('upload', 'icon-sm')}Restore…<input type="file" id="file" accept="application/json,.json" hidden></label>
    </div>
    <div class="table-wrap scroll-y" style="max-height:480px"><table class="table dense"><thead><tr><th>Version</th><th>Change</th><th>By</th><th>When</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
      ${hist.map((h, i) => `<tr><td class="num t-3">${h.version}</td><td>${esc(h.summary)}</td><td class="t-2">${esc(h.actor)}</td><td class="t-2" title="${esc(dateTime(h.time))}">${ago(h.time)}</td>
        <td class="col-actions">${i === 0 ? '<span class="tag">Current</span>' : `<button class="btn btn-ghost btn-sm row-actions" data-v="${h.version}">${icon('rotate-ccw', 'icon-sm')}Restore</button>`}</td></tr>`).join('')}
    </tbody></table></div>`;
  $('#file', body).addEventListener('change', async (e) => {
    const file = e.target.files[0];
    e.target.value = '';
    if (!file) return;
    const ok = await confirmDialog({ title: 'Restore this backup?', confirm: 'Restore', message: `The current configuration is replaced with <b>${esc(file.name)}</b>. It stays in history, so you can undo.` });
    if (ok && await attempt(async () => api('/api/config/import', { method: 'POST', raw: await file.text() }), 'Configuration restored')) { refreshShell(); backup(body); }
  });
  body.onclick = async (e) => {
    const b = e.target.closest('[data-v]');
    if (!b) return;
    const ok = await confirmDialog({ title: `Restore version ${b.dataset.v}?`, confirm: 'Restore', message: 'This becomes a new version, so you can always go back.' });
    if (ok && await attempt(() => post('/api/config/rollback', { version: +b.dataset.v }), `Restored version ${b.dataset.v}`)) { refreshShell(); backup(body); }
  };
}

// audit

async function audit(body) {
  const list = await get('/api/audit');
  body.innerHTML = list.length ? `<div class="table-wrap scroll-y"><table class="table dense"><thead><tr><th>When</th><th>User</th><th>Action</th><th>From</th></tr></thead><tbody>
    ${list.map((a) => `<tr><td class="t-2" style="white-space:nowrap">${dateTime(a.time)}</td><td>${esc(a.user)}</td><td>${esc(a.action)}</td><td class="mono t-3">${esc(a.ip)}</td></tr>`).join('')}
    </tbody></table></div>` : empty({ icon: 'clipboard-list', title: 'No activity yet', text: 'Every change made in the dashboard is recorded here.' });
}
