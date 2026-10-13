import { get, post } from '/js/api.js';
import { $$, esc, icon, ago, isSet, attempt } from '/js/ui.js';

const SEV = { high: ['bad', 'shield-alert'], medium: ['warn', 'triangle-alert'], low: ['off', 'info'] };

function card(s, admin) {
  if (!s || !s.available) return `<p class="t-3 t-sm" style="padding:16px 14px">Not available on this system.</p>`;
  const findings = s.devices.flatMap((d) => d.findings.map((f) => ({ ...f, device: d.name || d.ip || d.mac, mac: d.mac })));
  const serious = findings.filter((f) => f.severity !== 'low');
  const when = isSet(s.time) ? `${s.checked} device${s.checked === 1 ? '' : 's'} checked ${ago(s.time).toLowerCase()}` : 'Not checked yet';
  const head = s.running ? `<div class="scan-sum"><span class="spin">${icon('refresh-cw', 'icon-sm')}</span>Checking your devices…</div>`
    : !isSet(s.time) ? `<div class="scan-sum">${icon('scan-search', 'icon-sm')}The first check runs a few minutes after Fengard starts.</div>`
    : serious.length ? `<div class="scan-sum warn">${icon('shield-alert', 'icon-sm')}${serious.length} thing${serious.length > 1 ? 's' : ''} worth fixing</div>`
    : `<div class="scan-sum ok">${icon('shield-check', 'icon-sm')}All clear. No risky settings found.</div>`;
  return `<div class="panel-body scan">
      ${head}
      ${serious.length ? `<div class="findings">${serious.map((f) => `
        <details class="finding"><summary><i class="${SEV[f.severity][0]}"></i><b>${esc(f.title)}</b><small class="truncate">${esc(f.device)}</small></summary>
          <p>${esc(f.detail)}</p></details>`).join('')}</div>` : ''}
      <div class="scan-foot"><small class="t-3">${esc(when)} · checks itself every week</small>
        ${admin ? `<button class="btn btn-sm" data-scan ${s.running ? 'disabled' : ''}>${icon('scan-search', 'icon-sm')}Check now</button>` : ''}</div>
    </div>`;
}

export default {
  id: 'devicecheck',
  title: 'Device check',
  size: 'sm',
  render(w) {
    const draw = async () => {
      const s = await get('/api/scan').catch(() => null);
      if (!w.alive()) return;
      // keep any finding the user opened open across refreshes
      const open = $$('details[open] summary b', w.body).map((b) => b.textContent);
      w.body.innerHTML = card(s, w.admin);
      $$('details', w.body).forEach((d) => { if (open.includes(d.querySelector('summary b').textContent)) d.open = true; });
    };
    w.body.addEventListener('click', async (e) => {
      if (e.target.closest('[data-scan]') && await attempt(() => post('/api/scan'), 'Checking your devices')) setTimeout(draw, 500);
    });
    draw();
    w.every(5000, draw);
  },
};
