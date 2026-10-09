import { get, post, put, del } from '../api.js';
import { $, esc, icon, attempt, confirmDialog, modal, switchInput, toast, ago, copyText, busy } from '../ui.js';
import { deviceName } from '../meta.js';
import { isAdmin } from '../main.js';

const bytes = (n) => {
  n = Number(n) || 0;
  if (n < 1024) return `${n} B`;
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return `${n.toFixed(n < 10 ? 1 : 0)} ${u[i]}`;
};

export async function render(el, ctx) {
  const admin = isAdmin();
  let v = null, devices = [], groups = [];

  el.innerHTML = `<div id="top"></div>
    <div id="ts"></div>
    <div class="panel">
      <div class="panel-head"><h3>Devices</h3><span class="sub">Each phone or laptop gets its own key. Delete one to cut it off.</span>
        ${admin ? `<button class="btn btn-primary btn-sm" id="add" style="margin-left:auto">${icon('plus', 'icon-sm')}Add device</button>` : ''}</div>
      <div class="table-wrap" id="peers"></div>
    </div>`;

  const groupName = (id) => { const g = groups.find((x) => x.id === id); return esc(g ? g.name : 'default'); };
  const filteredAs = (p) => {
    if (p.device) { const d = devices.find((x) => x.mac === p.device); return d ? `Same as ${esc(deviceName(d))}` : `Device ${esc(p.device)}`; }
    const own = devices.find((x) => x.mac === p.identity);
    return `Profile: ${groupName(own ? own.group : p.group)}`;
  };

  const drawTop = () => {
    const r = v.reach || {};
    const status = !v.supported ? ['Not available on this system', 'warn']
      : !v.enabled ? ['Off', '']
      : v.running ? ['Running', 'ok'] : ['Starting…', 'warn'];
    $('#top', el).innerHTML = `
      ${v.enabled && r.doubleNat ? `<div class="banner warn" style="margin-bottom:16px">${icon('triangle-alert')}<div class="banner-text"><b>Another router sits between this gateway and the internet.</b>
        <p>Its WAN address is <span class="mono">${esc(r.wanIp)}</span>, a private one. On that router, forward <b>UDP ${v.port}</b> to <span class="mono">${esc(r.wanIp)}</span>, or phones won't be able to connect from outside. ${r.publicIp ? `Your public address is <span class="mono">${esc(r.publicIp)}</span>.` : ''} Can't change that router? <b>Use Tailscale below</b>: it needs no port forwarding.</p></div></div>` : ''}
      ${v.enabled && !v.effectiveEndpoint && !r.detecting ? `<div class="banner danger" style="margin-bottom:16px">${icon('circle-alert')}<div class="banner-text"><b>No address for devices to connect to.</b><p>${esc(r.error || 'The public address could not be detected.')} Enter a hostname or address below.</p></div></div>` : ''}
      <div class="panel">
        <div class="panel-head"><h3>WireGuard</h3><span class="sub">Built in. Needs one UDP port reachable from the internet; phones get a QR code.</span>
          ${admin ? `<span style="margin-left:auto">${switchInput('id="on"' + (v.supported ? '' : ' disabled'), v.enabled, 'VPN on')}</span>` : ''}</div>
        <div class="panel-body grid g-2" style="align-items:start">
          <dl class="kv">
            <dt>Status</dt><dd><span class="tag ${status[1]}">${status[0]}</span></dd>
            <dt>Devices connect to</dt><dd class="mono">${v.effectiveEndpoint ? esc(v.effectiveEndpoint) : '<span class="t-3">not set</span>'}</dd>
            <dt>Public address</dt><dd class="mono">${r.publicIp ? esc(r.publicIp) : `<span class="t-3">${r.detecting ? 'detecting…' : esc(r.error || 'unknown')}</span>`}</dd>
            <dt>WAN address</dt><dd class="mono">${r.wanIp ? esc(r.wanIp) : '<span class="t-3">none</span>'}</dd>
            <dt>Tunnel network</dt><dd class="mono">${esc(v.subnet)}</dd>
            <dt>Server key</dt><dd class="row"><span class="mono truncate" style="max-width:260px">${esc(v.publicKey || '—')}</span>${v.publicKey ? `<button class="btn btn-ghost btn-sm" id="cpk">${icon('copy', 'icon-sm')}</button>` : ''}</dd>
          </dl>
          ${admin ? `<form id="srv" class="stack" style="gap:12px" novalidate>
            <div><label class="label" for="ep">Public hostname or address</label>
              <input class="input" id="ep" placeholder="${esc(r.publicIp || 'home.example.net')}" value="${esc(v.endpoint)}">
              <p class="t-xs t-3" style="margin-top:4px">Leave empty to use the detected public address. Set a dynamic-DNS name if your address changes.</p></div>
            <div><label class="label" for="port">Port (UDP)</label><input class="input" id="port" type="number" min="1" max="65535" value="${v.port}" style="width:120px"></div>
            <div><button class="btn" type="submit">${icon('save', 'icon-sm')}Save</button></div></form>` : ''}
        </div>
      </div>`;
    $('#cpk', el)?.addEventListener('click', () => copyText(v.publicKey));
    $('#on', el)?.addEventListener('change', async (e) => {
      const on = e.target.checked;
      if (!await attempt(() => put('/api/vpn', { enabled: on, port: v.port, endpoint: v.endpoint }), on ? 'VPN turned on' : 'VPN turned off')) e.target.checked = !on;
      load();
    });
    $('#srv', el)?.addEventListener('submit', async (e) => {
      e.preventDefault();
      const body = { enabled: v.enabled, port: +$('#port', el).value, endpoint: $('#ep', el).value.trim() };
      if (await attempt(() => put('/api/vpn', body), 'VPN settings saved')) load();
    });
  };

  const drawTailscale = () => {
    const t = v.tailscale || {};
    const tag = !t.supported ? ['Not installed on this gateway', ''] : t.running ? ['Running', 'ok'] : [t.state === 'NeedsLogin' ? 'Needs login' : 'Not running', 'warn'];
    const self = t.self || {};
    const selfIP = (self.ips || []).find((x) => x.includes('.')) || "the gateway's Tailscale address";
    const rows = (t.peers || []).map((p) => `<tr>
        <td class="cell-title"><span class="row">${p.online ? '<span class="dot-on" title="Online"></span>' : ''}${esc(p.name)}</span></td>
        <td class="t-2">${esc(p.os || '')}</td>
        <td class="mono">${esc((p.ips || []).find((x) => x.includes('.')) || '')}</td>
        <td class="t-2">${p.online ? 'now' : p.lastSeen && !p.lastSeen.startsWith('0001') ? ago(p.lastSeen) : '<span class="t-3">never</span>'}</td>
        <td class="t-2 mono">${p.rxBytes || p.txBytes ? `${bytes(p.rxBytes)} ↓ ${bytes(p.txBytes)} ↑` : '—'}</td></tr>`).join('');
    $('#ts', el).innerHTML = `<div class="panel">
      <div class="panel-head"><h3>Tailscale</h3><span class="sub">Works behind any router, no port forwarding: the phone uses this gateway as its exit node.</span><span class="actions"><span class="tag ${tag[1]}">${tag[0]}</span></span></div>
      <div class="panel-body grid g-2" style="align-items:start">
        <dl class="kv">
          <dt>This gateway</dt><dd>${t.running ? `<b>${esc(self.name || '')}</b> <span class="mono t-2">${esc((self.ips || []).find((x) => x.includes('.')) || '')}</span>` : '<span class="t-3">—</span>'}</dd>
          <dt>Exit node</dt><dd>${!t.running ? '<span class="t-3">—</span>' : t.exitNode ? '<span class="tag ok">Offered</span>' : '<span class="tag warn">Not offered</span> <span class="t-xs t-3">offer this router as an exit node, step 1</span>'}</dd>
          <dt>DNS</dt><dd>${!t.running ? '<span class="t-3">—</span>' : v.tailscaleDns ? '<span class="tag ok">Going through Fengard</span>' : (t.peers || []).some((p) => p.online) ? `<span class="tag warn">Bypassing Fengard</span> <span class="t-xs t-3">set the tailnet DNS to <span class="mono">${esc(selfIP)}</span>, step 3</span>` : '<span class="t-3">no device connected yet</span>'}</dd>
          <dt>Filtering</dt><dd class="t-2">${t.supported ? 'Every Tailscale device that routes through this gateway is filtered like a device on the Wi‑Fi; it appears in Devices to approve and assign a profile.' : 'Install Tailscale on the gateway to use this.'}</dd>
        </dl>
        <ol class="cert-steps">
          <li><span>Install <b>Tailscale</b> on the router and sign in with a free Tailscale account, offering the router as an exit node. Routers with a Tailscale page in their own admin panel (GL.iNet: Applications → Tailscale, turn on <b>Allow remote access WAN</b>) do it there. On OpenWrt, install the <span class="mono">tailscale</span> package and run <span class="mono">tailscale up --advertise-exit-node</span>.</span></li>
          <li><span>At <b>login.tailscale.com</b> → Machines → this router → <b>Edit route settings</b> → tick <b>Use as exit node</b>.</span></li>
          <li><span>Still in the console: <b>DNS → Nameservers → Add nameserver → Custom</b> → <span class="mono">${esc(selfIP)}</span>, and turn on <b>Override DNS servers</b>. Without this, Tailscale resolves names on the router itself and skips Fengard.</span></li>
          <li><span>On the phone install <b>Tailscale</b>, sign in with the same account, tap <b>Exit node</b> and choose this router. Leave it on; it reconnects by itself on mobile data.</span></li>
          <li><span>Approve the phone when it appears under <b>Devices</b> and put it in a profile.</span></li>
        </ol>
      </div>
      ${rows ? `<div class="table-wrap"><table class="table dense"><thead><tr><th>Tailscale device</th><th>System</th><th>Address</th><th>Last seen</th><th>Transfer</th></tr></thead><tbody>${rows}</tbody></table></div>` : ''}
    </div>`;
  };

  const drawPeers = () => {
    if (!v.peers.length) {
      $('#peers', el).innerHTML = `<div class="empty" style="padding:28px 16px;text-align:center"><p class="t-2">No devices yet.</p>
        <p class="t-sm t-3">${admin ? 'Add your phone, scan the code with the WireGuard app, and it stays protected on mobile data.' : 'An admin can add devices.'}</p></div>`;
      return;
    }
    $('#peers', el).innerHTML = `<table class="table dense"><thead><tr><th>Name</th><th>Address</th><th>Filtered as</th><th>Last connected</th><th>Transfer</th><th>Enabled</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>
      ${v.peers.map((p) => `<tr data-id="${p.id}">
        <td class="cell-title"><span class="row">${p.connected ? `<span class="dot-on" title="Connected"></span>` : ''}${esc(p.name)}</span></td>
        <td class="mono">${esc(p.ip)}</td>
        <td>${filteredAs(p)}</td>
        <td class="t-2">${p.status.lastHandshake ? ago(p.status.lastHandshake) : '<span class="t-3">never</span>'}${p.status.endpoint ? ` <span class="t-3 mono t-xs">from ${esc(p.status.endpoint.replace(/:\d+$/, ''))}</span>` : ''}</td>
        <td class="t-2 mono">${p.status.rxBytes || p.status.txBytes ? `${bytes(p.status.rxBytes)} ↓ ${bytes(p.status.txBytes)} ↑` : '—'}</td>
        <td>${switchInput(`data-toggle ${admin ? '' : 'disabled'}`, p.enabled, `Enable ${p.name}`)}</td>
        <td class="col-actions"><span class="row row-actions">
          ${admin ? `<button class="icon-btn" data-qr aria-label="Show QR code">${icon('qr-code')}</button>
          <a class="icon-btn" href="/api/vpn/peers/${p.id}/config?download=1" download aria-label="Download profile">${icon('download')}</a>
          <button class="icon-btn" data-del aria-label="Delete ${esc(p.name)}">${icon('trash-2')}</button>` : ''}</span></td></tr>`).join('')}
    </tbody></table>`;
  };

  const load = async () => {
    [v, devices, groups] = await Promise.all([get('/api/vpn'), get('/api/devices'), get('/api/groups')]);
    if (!ctx.alive()) return;
    drawTop();
    drawTailscale();
    drawPeers();
  };

  const showProfile = async (id) => {
    const c = await get(`/api/vpn/peers/${id}/config`);
    const { default: qrcode } = await import('../vendor-qrcode.mjs');
    const qr = qrcode(0, 'M');
    qr.addData(c.config);
    qr.make();
    const m = modal({
      title: `${c.name} · WireGuard profile`, wide: true,
      subtitle: c.ready ? `Connects to ${c.endpoint} and routes everything through ${esc(v.subnet.split('/')[0].replace(/0$/, '1'))}.` : 'The public address is not known yet; set it on the VPN page before scanning.',
      body: `<div class="grid g-2" style="align-items:start">
        <div><div class="qr">${qr.createSvgTag({ cellSize: 3, margin: 0, scalable: true })}</div>
          <div class="row wrap" style="margin-top:10px;gap:8px"><a class="btn btn-sm" href="/api/vpn/peers/${id}/config?download=1" download>${icon('download', 'icon-sm')}Download .conf</a><button class="btn btn-sm btn-ghost" id="cpc">${icon('copy', 'icon-sm')}Copy</button></div></div>
        <ol class="cert-steps">
          <li><span>Install <b>WireGuard</b> from the App Store or Google Play (on a laptop, from wireguard.com).</span></li>
          <li><span>Open it, tap <b>+</b> → <b>Create from QR code</b>, scan this code and name the tunnel <b>Fengard</b>.</span></li>
          <li><span>Turn the tunnel on. Browsing now goes through your gateway: blocked sites show the Fengard block page, exactly as at home.</span></li>
          <li><span>To make it automatic: on iPhone tap the tunnel → <b>Edit</b> → <b>On-Demand</b>, turn on <b>Cellular</b> and <b>Wi‑Fi</b>, and add your home network under <b>SSID exclusions</b>. On Android use the app's <b>Always-on VPN</b> setting.</span></li>
        </ol>
        <p class="t-xs t-3" style="grid-column:1/-1">This code contains the device's private key. Anyone who scans it can connect as this device, so don't share screenshots of it; you can delete the device at any time.</p></div>`,
      foot: `<button class="btn" data-close>Done</button>`,
    });
    $('#cpc', m.el)?.addEventListener('click', () => copyText(c.config));
  };

  const addDevice = () => {
    const opts = [
      `<optgroup label="Same rules as a device on the network">${devices.map((d) => `<option value="dev:${esc(d.mac)}">${esc(deviceName(d))}</option>`).join('')}</optgroup>`,
      `<optgroup label="Its own profile">${groups.map((g) => `<option value="grp:${esc(g.id)}">${esc(g.name)}</option>`).join('')}</optgroup>`,
    ].join('');
    const m = modal({
      title: 'Add a device to the VPN',
      body: `<form id="np" class="stack" style="gap:12px" novalidate>
        <div><label class="label" for="pn">Name</label><input class="input" id="pn" placeholder="Sam's iPhone" maxlength="40" required></div>
        <div><label class="label" for="pf">Filter as</label><select class="select" id="pf">${opts}</select>
          <p class="t-xs t-3" style="margin-top:4px">Pick the same phone from the device list so it keeps one identity, at home and away.</p></div>
        <p class="t-sm t-danger" id="pe" hidden></p>
        <div class="row" style="justify-content:flex-end;gap:8px"><button class="btn" type="button" data-close>Cancel</button><button class="btn btn-primary" type="submit">Create profile</button></div></form>`,
    });
    const f = $('#np', m.el);
    f.pn.focus();
    f.addEventListener('submit', async (e) => {
      e.preventDefault();
      const name = f.pn.value.trim();
      if (!name) { f.pn.focus(); return; }
      const [kind, val] = f.pf.value.split(/:(.*)/);
      const body = { name, device: kind === 'dev' ? val : '', group: kind === 'grp' ? val : '' };
      await busy(f.querySelector('[type=submit]'), async () => {
        try {
          const c = await post('/api/vpn/peers', body);
          m.close();
          toast(`${name} added`);
          await load();
          showProfile(c.id);
        } catch (x) { $('#pe', f).textContent = x.message; $('#pe', f).hidden = false; }
      });
    });
  };

  el.addEventListener('click', async (e) => {
    if (e.target.closest('#add')) return addDevice();
    const row = e.target.closest('tr[data-id]');
    if (!row) return;
    const p = v.peers.find((x) => x.id === row.dataset.id);
    if (e.target.closest('[data-qr]')) return showProfile(p.id);
    if (e.target.closest('[data-del]')) {
      const ok = await confirmDialog({ title: `Remove “${p.name}” from the VPN?`, danger: true, confirm: 'Remove', message: 'Its key stops working immediately. The device will need a new profile to connect again.' });
      if (ok && await attempt(() => del(`/api/vpn/peers/${p.id}`), 'Device removed')) load();
    }
  });
  el.addEventListener('change', async (e) => {
    if (!e.target.matches('[data-toggle]')) return;
    const p = v.peers.find((x) => x.id === e.target.closest('tr').dataset.id);
    if (!await attempt(() => put(`/api/vpn/peers/${p.id}`, { enabled: e.target.checked }), e.target.checked ? 'Enabled' : 'Disabled')) e.target.checked = !e.target.checked;
    load();
  });

  await load();
  const timer = setInterval(() => { if (ctx.alive()) load(); else clearInterval(timer); }, 15000);
}
