import { get, post, put } from '../api.js';
import { $, esc, icon, attempt, confirmDialog, switchInput, ago, busy, empty } from '../ui.js';
import { deviceName } from '../meta.js';
import { isAdmin } from '../main.js';

export async function render(el, ctx) {
  const admin = isAdmin();
  let v = null, shown = false, qrText = '', qrSvg = '';

  el.innerHTML = `<div id="top"></div><div id="join"></div><div id="lists"></div>`;

  const statusTag = (st) => ({
    on: ['On', 'ok'], applying: ['Setting up…', 'warn'], removing: ['Turning off…', 'warn'], error: ['Didn\'t work', 'warn'],
  }[st.state] || ['Off', '']);

  const drawTop = () => {
    const st = v.status || {};
    const [label, cls] = statusTag(st);
    const busyNow = st.state === 'applying' || st.state === 'removing';
    const where = st.mode === 'existing'
      ? `The router's own guest network (<span class="mono">${esc(st.network)}</span>)`
      : st.mode === 'own' ? `A network Fengard made (<span class="mono">${esc(st.network)}</span>)` : '';
    $('#top', el).innerHTML = `
      ${!st.router ? `<div class="banner info" style="margin-bottom:16px">${icon('info')}<div class="banner-text"><b>Guest Wi-Fi needs Fengard on the router</b>
        <p>This Fengard runs on a computer, so it can't make a Wi-Fi network. Install it on the router to use this page.</p></div></div>` : ''}
      ${st.error ? `<div class="banner danger" style="margin-bottom:16px">${icon('circle-alert')}<div class="banner-text"><b>Guest Wi-Fi didn't change.</b><p>${esc(st.error)}</p></div></div>` : ''}
      ${(st.notes || []).map((n) => `<div class="banner warn" style="margin-bottom:16px">${icon('triangle-alert')}<div class="banner-text"><p>${esc(n)}</p></div></div>`).join('')}
      <div class="panel">
        <div class="panel-head"><h3>Guest Wi-Fi</h3><span class="sub">A separate Wi-Fi for visitors. They can't see your devices, and each one waits for you to approve it.</span>
          ${admin ? `<span style="margin-left:auto">${switchInput(`id="on"${st.router && !busyNow ? '' : ' disabled'}`, v.enabled, 'Guest Wi-Fi on')}</span>` : ''}</div>
        <div class="panel-body grid g-2" style="align-items:start">
          <dl class="kv">
            <dt>Status</dt><dd><span class="tag ${cls}">${label}</span>${st.step ? ` <span class="t-xs t-3">${esc(st.step)}</span>` : ''}</dd>
            <dt>Name</dt><dd>${v.ssid ? `<b>${esc(v.ssid)}</b>` : '<span class="t-3">not set</span>'}</dd>
            ${where ? `<dt>Network</dt><dd>${where}${st.subnet ? ` <span class="mono t-2">${esc(st.subnet)}</span>` : ''}</dd>` : ''}
            <dt>Profile</dt><dd>${v.group ? `<a href="#profiles?id=${encodeURIComponent(v.group)}">${esc(v.groupName || v.group)}</a>` : '<span class="t-3">made when you turn it on</span>'}</dd>
          </dl>
          ${admin ? `<form id="name" class="stack" style="gap:12px" novalidate>
            <div><label class="label" for="ssid">Wi-Fi name</label>
              <input class="input" id="ssid" maxlength="32" value="${esc(v.ssid || '')}" placeholder="Our guests">
              <p class="t-xs t-3" style="margin-top:4px">What guests see in their Wi-Fi list. Changing it restarts the guest Wi-Fi.</p></div>
            <div><button class="btn" type="submit"${st.router && !busyNow ? '' : ' disabled'}>${icon('save', 'icon-sm')}Save</button></div></form>` : ''}
        </div>
      </div>`;
    $('#on', el)?.addEventListener('change', async (e) => {
      const on = e.target.checked;
      if (!on && !await confirmDialog({ title: 'Turn guest Wi-Fi off?', confirm: 'Turn off', message: 'Guests are disconnected and the router\'s settings go back to how they were. Approved guests stay approved for next time.' })) {
        e.target.checked = true;
        return;
      }
      if (!await attempt(() => put('/api/guest', { enabled: on, ssid: $('#ssid', el)?.value.trim() || v.ssid }), on ? 'Setting up guest Wi-Fi' : 'Turning guest Wi-Fi off')) e.target.checked = !on;
      load();
    });
    $('#name', el)?.addEventListener('submit', async (e) => {
      e.preventDefault();
      const ssid = $('#ssid', el).value.trim();
      if (!ssid) return $('#ssid', el).focus();
      await busy(e.target.querySelector('[type=submit]'), async () => {
        if (await attempt(() => put('/api/guest', { enabled: v.enabled, ssid }), 'Name saved')) load();
      });
    });
  };

  const drawJoin = async () => {
    const box = $('#join', el);
    if (!v.enabled || !v.qr) { box.innerHTML = ''; return; }
    if (v.qr !== qrText) {
      const { default: qrcode } = await import('../vendor-qrcode.mjs');
      const qr = qrcode(0, 'M');
      qr.addData(v.qr);
      qr.make();
      qrText = v.qr;
      qrSvg = qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true });
    }
    if (!ctx.alive()) return;
    box.innerHTML = `<div class="panel">
      <div class="panel-head"><h3>Join the guest Wi-Fi</h3><span class="sub">Guests scan this with the phone camera. Take a screenshot to share it.</span></div>
      <div class="panel-body" style="display:flex;flex-wrap:wrap;gap:24px;align-items:center">
        <div class="qr" style="padding:16px"><div style="width:min(280px,70vw);height:min(280px,70vw)">${qrSvg}</div></div>
        <div class="stack" style="gap:10px;min-width:220px">
          <div><div class="t-xs t-3">Wi-Fi</div><div style="font-size:20px;font-weight:600">${esc(v.ssid)}</div></div>
          <div><div class="t-xs t-3">Password</div>
            <div class="row"><span class="mono" style="font-size:16px">${shown ? esc(v.password) : '••••-••••-••••-••••'}</span>
              <button class="btn btn-ghost btn-sm" id="show">${icon(shown ? 'eye-off' : 'eye', 'icon-sm')}${shown ? 'Hide' : 'Show'}</button></div></div>
          ${admin ? `<form class="stack" id="pwform" style="gap:6px">
            <label class="label" for="pw">Change the password</label>
            <div class="row" style="flex-wrap:wrap">
              <input class="input mono" id="pw" minlength="8" maxlength="63" placeholder="Your own, 8 to 63 characters" autocomplete="off" spellcheck="false" style="max-width:250px">
              <button class="btn btn-sm" type="submit">Save</button>
              <button class="btn btn-sm btn-ghost" type="button" id="newpw">${icon('refresh-cw', 'icon-sm')}Random</button></div>
            <span class="help" id="pw-help">Or leave it and press Random for a long one nobody will guess.</span></form>` : ''}
          <p class="t-xs t-3">Each new device waits on a "waiting for approval" page until you approve it below.</p>
        </div>
      </div></div>`;
    $('.qr svg', box)?.setAttribute('style', 'width:100%;height:100%');
    $('#show', box).addEventListener('click', () => { shown = !shown; drawJoin(); });
    const setPassword = async (password) => {
      const ok = await confirmDialog({ title: 'Change the password?', confirm: 'Change password', message: 'The old code and password stop working, and everyone on the guest Wi-Fi is disconnected for a moment. Approved guests stay approved, but they need to scan the new code.' });
      if (ok && await attempt(() => post('/api/guest/password', password ? { password } : {}), 'Password changed')) load();
    };
    $('#newpw', box)?.addEventListener('click', () => setPassword(''));
    $('#pwform', box)?.addEventListener('submit', (e) => {
      e.preventDefault();
      const pw = $('#pw', box).value;
      const bad = pw.length < 8 || pw.length > 63 ? 'It has to be 8 to 63 characters.'
        : /[^\x20-\x7e]/.test(pw) ? 'Use letters, numbers, spaces and the usual symbols, not emoji or accents.' : '';
      $('#pw-help', box).textContent = bad || 'Or leave it and press Random for a long one nobody will guess.';
      $('#pw-help', box).classList.toggle('t-danger', !!bad);
      if (bad) { $('#pw', box).focus(); return; }
      setPassword(pw);
    });
  };

  const row = (g, actions) => `<tr data-mac="${esc(g.mac)}">
      <td class="cell-title"><span class="row">${g.online ? '<span class="dot-on" title="Online"></span>' : ''}${esc(deviceName(g))}</span>
        ${g.vendor && g.name ? `<div class="t-xs t-3">${esc(g.vendor)}</div>` : ''}</td>
      <td class="mono t-2">${esc((g.ips || []).find((x) => x.includes('.')) || '')}</td>
      <td class="t-2">${g.firstSeen ? ago(g.firstSeen) : ''}</td>
      <td class="col-actions">${admin ? `<span class="row" style="justify-content:flex-end;gap:6px">${actions}</span>` : ''}</td></tr>`;
  const table = (list, head, actions) => `<table class="table dense"><thead><tr><th>Device</th><th>Address</th><th>${head}</th><th class="col-actions" aria-label="Actions"></th></tr></thead>
    <tbody>${list.map((g) => row(g, actions)).join('')}</tbody></table>`;

  const drawLists = () => {
    const btn = (act, label, ic, cls = '') => `<button class="btn btn-sm ${cls}" data-act="${act}">${icon(ic, 'icon-sm')}${label}</button>`;
    const waiting = v.waiting || [], guests = v.guests || [], denied = v.denied || [];
    $('#lists', el).innerHTML = `
      <div class="panel">
        <div class="panel-head"><h3>Waiting</h3><span class="sub">New devices on the guest Wi-Fi. They get no internet until you approve them.</span></div>
        <div class="table-wrap">${waiting.length ? table(waiting, 'Joined', btn('approve', 'Approve', 'check', 'btn-primary') + btn('deny', 'Deny', 'ban')) : empty({ icon: 'inbox', title: 'Nobody waiting' })}</div>
      </div>
      <div class="panel">
        <div class="panel-head"><h3>Guests</h3><span class="sub">Approved guests stay approved, even with a new password.</span></div>
        <div class="table-wrap">${guests.length ? table(guests, 'Joined', btn('kick', 'Kick', 'ban')) : empty({ icon: 'users', title: 'No guests yet' })}</div>
      </div>
      ${denied.length ? `<div class="panel">
        <div class="panel-head"><h3>Denied</h3><span class="sub">Kept off until you approve them.</span></div>
        <div class="table-wrap">${table(denied, 'Joined', btn('approve', 'Approve', 'check'))}</div></div>` : ''}`;
  };

  let timer = 0;
  const load = async () => {
    v = await get('/api/guest');
    if (!ctx.alive()) return;
    drawTop();
    await drawJoin();
    drawLists();
    // quicker while the router is being changed
    clearTimeout(timer);
    const st = v.status?.state;
    timer = setTimeout(() => { if (ctx.alive()) load(); }, st === 'applying' || st === 'removing' ? 2000 : 10000);
  };

  el.addEventListener('click', async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const mac = b.closest('tr').dataset.mac;
    const all = [...(v.waiting || []), ...(v.guests || []), ...(v.denied || [])];
    const g = all.find((x) => x.mac === mac);
    const name = g ? deviceName(g) : mac;
    const act = b.dataset.act;
    if (act === 'kick' && !await confirmDialog({ title: `Kick ${name}?`, danger: true, confirm: 'Kick', message: 'It loses internet now and stays off until you approve it again.' })) return;
    const done = { approve: `${name} approved`, deny: `${name} denied`, kick: `${name} kicked` }[act];
    await busy(b, async () => {
      if (await attempt(() => post(`/api/guest/${encodeURIComponent(mac)}/${act}`), done)) load();
    });
  });

  await load();
}
