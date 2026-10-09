import { get, post, put, del } from '../api.js';
import { $, esc, icon, attempt, confirmDialog, empty, switchInput, toast, exampleIP } from '../ui.js';
import { deviceName } from '../meta.js';
import { isAdmin } from '../main.js';

export async function render(el, ctx) {
  let fwds = [], devices = [];
  const admin = isAdmin();

  el.innerHTML = `
    <div class="panel">
      <div class="panel-head"><h3>Port forwarding</h3><span class="sub">Lets the internet reach one service on one device. Forward only what you need.</span></div>
      <div class="table-wrap" id="tbl"></div>
    </div>`;

  const devFor = (ip) => devices.find((d) => (d.ips || []).includes(ip));
  const devOptions = () => devices.filter((d) => (d.ips || []).some((ip) => ip.includes('.')))
    .map((d) => { const ip = d.ips.find((x) => x.includes('.')); return `<option value="${esc(ip)}">${esc(deviceName(d))} (${esc(ip)})</option>`; }).join('');

  const addRow = () => admin ? `<tr class="inline-form">
    <td><input class="input input-sm" id="f-name" placeholder="Minecraft server"></td>
    <td><select class="select select-sm" id="f-proto" style="width:auto"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="both">TCP + UDP</option></select></td>
    <td><input class="input input-sm" id="f-ext" type="number" min="1" max="65535" placeholder="25565" style="width:100px"></td>
    <td><div class="row"><select class="select select-sm" id="f-dev" style="width:auto;max-width:200px"><option value="">Device…</option>${devOptions()}</select>
      <input class="input input-sm mono" id="f-ip" placeholder="${exampleIP(50)}" style="width:140px"><span class="t-3">:</span><input class="input input-sm" id="f-port" type="number" min="1" max="65535" placeholder="port" style="width:80px"></div></td>
    <td></td>
    <td class="col-actions"><button class="btn btn-primary btn-sm" id="f-add">${icon('plus', 'icon-sm')}Add</button></td></tr>` : '';

  const draw = () => {
    const rows = fwds.map((f) => {
      const d = devFor(f.destIp);
      return `<tr data-id="${f.id}">
        <td class="cell-title">${esc(f.name)}</td>
        <td><span class="tag">${f.proto === 'both' ? 'TCP + UDP' : f.proto.toUpperCase()}</span></td>
        <td class="mono">${f.extPort}</td>
        <td><span class="mono">${esc(f.destIp)}:${f.destPort}</span>${d ? ` <span class="t-3">· ${esc(deviceName(d))}</span>` : ''}</td>
        <td>${switchInput(`data-toggle ${admin ? '' : 'disabled'}`, f.enabled, `Enable ${f.name}`)}</td>
        <td class="col-actions">${admin ? `<button class="icon-btn row-actions" data-del aria-label="Delete ${esc(f.name)}">${icon('trash-2')}</button>` : ''}</td></tr>`;
    }).join('');
    $('#tbl', el).innerHTML = rows || admin
      ? `<table class="table dense"><thead><tr><th>Name</th><th>Protocol</th><th>Outside port</th><th>Forwards to</th><th>Enabled</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>${addRow()}${rows}</tbody></table>
         ${!rows ? `<p class="t-3 t-sm" style="padding:12px 14px">Nothing on the network is reachable from the internet. That's the safest setup.</p>` : ''}`
      : empty({ icon: 'arrow-left-right', title: 'No port forwards', text: 'Nothing on the network is reachable from the internet.' });
    $('#f-dev', el)?.addEventListener('change', (e) => { if (e.target.value) $('#f-ip', el).value = e.target.value; });
    $('#f-ext', el)?.addEventListener('input', (e) => { const p = $('#f-port', el); if (!p.dataset.touched) p.value = e.target.value; });
    $('#f-port', el)?.addEventListener('input', (e) => (e.target.dataset.touched = '1'));
  };

  const load = async () => {
    [fwds, devices] = await Promise.all([get('/api/portforwards'), get('/api/devices')]);
    if (ctx.alive()) draw();
  };

  el.addEventListener('click', async (e) => {
    if (e.target.closest('#f-add')) {
      const body = { name: $('#f-name', el).value.trim(), proto: $('#f-proto', el).value, extPort: +$('#f-ext', el).value, destIp: $('#f-ip', el).value.trim(), destPort: +$('#f-port', el).value, enabled: true };
      if (!body.name || !body.extPort || !body.destIp || !body.destPort) return toast('Fill in the name, ports and device address', 'error');
      if (await attempt(() => post('/api/portforwards', body), `Forwarding port ${body.extPort}`)) load();
      return;
    }
    const row = e.target.closest('tr[data-id]');
    if (!row) return;
    const f = fwds.find((x) => x.id === row.dataset.id);
    if (e.target.closest('[data-del]')) {
      const ok = await confirmDialog({ title: `Delete “${f.name}”?`, danger: true, confirm: 'Delete', message: `Port ${f.extPort} will no longer be reachable from the internet.` });
      if (ok && await attempt(() => del(`/api/portforwards/${f.id}`), 'Port forward deleted')) load();
    }
  });
  el.addEventListener('keydown', (e) => { if (e.key === 'Enter' && e.target.closest('.inline-form')) { e.preventDefault(); $('#f-add', el)?.click(); } });
  el.addEventListener('change', async (e) => {
    if (!e.target.matches('[data-toggle]')) return;
    const f = fwds.find((x) => x.id === e.target.closest('tr').dataset.id);
    if (!await attempt(() => put(`/api/portforwards/${f.id}`, { ...f, enabled: e.target.checked }), e.target.checked ? 'Enabled' : 'Disabled')) e.target.checked = !e.target.checked;
    load();
  });

  await load();
}
