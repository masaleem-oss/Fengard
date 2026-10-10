import { get, post, put, del } from '../api.js';
import { $, $$, esc, icon, attempt, busy, empty, switchInput, copyText, exampleIP } from '../ui.js';
import { isAdmin } from '../main.js';

const PRESETS = [
  { name: 'Cloudflare', servers: ['1.1.1.1:53', '1.0.0.1:53'] },
  { name: 'Quad9', servers: ['9.9.9.9:53', '149.112.112.112:53'] },
  { name: 'Google', servers: ['8.8.8.8:53', '8.8.4.4:53'] },
  { name: 'Cloudflare DoT', servers: ['tls://1.1.1.1:853@cloudflare-dns.com', 'tls://1.0.0.1:853@cloudflare-dns.com'] },
  { name: 'Cloudflare DoH', servers: ['https://cloudflare-dns.com/dns-query'] },
  { name: 'Quad9 DoH', servers: ['https://dns.quad9.net/dns-query'] },
];

export async function render(el, ctx) {
  const admin = isAdmin();
  const ro = admin ? '' : 'disabled';
  let info;

  el.innerHTML = `
    <div class="grid g-2">
      <div class="panel">
        <div class="panel-head"><h3>Upstream resolvers</h3><span class="sub">Where lookups Fengard allows are sent</span></div>
        <form class="panel-body stack" id="up" novalidate>
          <div class="row wrap" id="presets">${PRESETS.map((p, i) => `<button type="button" class="btn btn-sm" data-preset="${i}" ${ro}>${esc(p.name)}</button>`).join('')}</div>
          <textarea class="textarea" name="upstreams" rows="4" spellcheck="false" ${ro}></textarea>
          <p class="t-xs t-3">One per line, tried in order. Plain <code>1.1.1.1</code>, encrypted <code>tls://1.1.1.1:853@cloudflare-dns.com</code> or <code>https://cloudflare-dns.com/dns-query</code>.</p>
          <div class="setting" style="padding:12px 0;border-top:1px solid var(--border);border-bottom:0">
            <div class="setting-text"><b>Request DNSSEC records</b><small>Send the DNSSEC OK bit. Fengard does not validate signatures locally. Protection requires a validating upstream over trusted DoH or DoT; plain DNS can be altered in transit.</small></div>
            ${switchInput(`name="dnssec" ${ro}`, false, 'DNSSEC')}
          </div>
          ${admin ? `<div><button class="btn btn-primary" type="submit">Save resolvers</button></div>` : ''}
        </form>
      </div>
      <div class="panel">
        <div class="panel-head"><h3>Local zone</h3><span class="sub">Devices are reachable by name</span></div>
        <form class="panel-body stack" id="zone" novalidate>
          <label class="field"><span>Zone</span>
            <div class="row"><input class="input" name="localZone" style="max-width:200px" ${ro}><span class="t-2 t-sm">so a device named “Leo's iPad” answers at <code id="zone-eg">leos-ipad.lan</code></span></div>
            <span class="help">A single label. Don't use <code>local</code>, which Apple and Android reserve for mDNS.</span></label>
          ${admin ? `<div><button class="btn" type="submit">Save zone</button></div>` : ''}
        </form>
        <div class="table-wrap scroll-y" id="names" style="max-height:300px"></div>
      </div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Local records</h3><span class="sub">Names answered here instead of upstream. Overrides work for any domain.</span></div>
      <div class="table-wrap" id="records"></div>
    </div>`;

  const load = async () => {
    info = await get('/api/dns');
    if (!ctx.alive()) return;
    const up = $('#up', el);
    if (document.activeElement !== up.upstreams) up.upstreams.value = info.upstreams.join('\n');
    up.dnssec.checked = info.dnssec;
    const z = $('#zone', el);
    if (document.activeElement !== z.localZone) z.localZone.value = info.zone;
    $('#zone-eg', el).textContent = `leos-ipad.${info.zone}`;
    $('#names', el).innerHTML = info.names.length ? `<table class="table dense"><thead><tr><th>Name</th><th>Address</th></tr></thead><tbody>
      ${info.names.map((n) => `<tr><td class="mono">${esc(n.name)}</td><td class="mono t-2">${esc(n.ips.join(', ') || 'offline')}</td></tr>`).join('')}</tbody></table>`
      : `<p class="t-3 t-sm" style="padding:14px">Name a device on the Devices page and it appears here.</p>`;
    drawRecords();
  };

  const drawRecords = () => {
    const rows = info.records.map((r) => `<tr>
      <td class="mono">${esc(r.name)}</td><td><span class="tag">${esc(r.type)}</span></td><td class="mono">${esc(r.value)}</td>
      <td class="col-actions">${admin ? `<button class="btn btn-ghost btn-sm btn-danger row-actions" data-del data-name="${esc(r.name)}" data-type="${esc(r.type)}">${icon('trash-2', 'icon-sm')}</button>` : ''}</td></tr>`).join('');
    const add = admin ? `<tr class="inline-form"><td><input class="input input-sm" id="r-name" placeholder="nas or nas.${esc(info.zone)}" spellcheck="false"></td>
      <td><select class="select select-sm" id="r-type" style="width:auto"><option>A</option><option>AAAA</option><option>CNAME</option></select></td>
      <td><input class="input input-sm" id="r-value" placeholder="${exampleIP(40)}" spellcheck="false"></td>
      <td class="col-actions"><button class="btn btn-primary btn-sm" id="r-add">${icon('plus', 'icon-sm')}Add</button></td></tr>` : '';
    $('#records', el).innerHTML = rows || add ? `<table class="table dense"><thead><tr><th>Name</th><th>Type</th><th>Value</th><th class="col-actions" aria-label="Actions"></th></tr></thead><tbody>${add}${rows}</tbody></table>`
      : empty({ icon: 'globe', title: 'No local records' });
    $('#r-type', el)?.addEventListener('change', (e) => { $('#r-value', el).placeholder = { A: exampleIP(40), AAAA: 'fd00::40', CNAME: `nas.${info.zone}` }[e.target.value]; });
  };

  $('#up', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-preset]');
    if (b) $('#up', el).upstreams.value = PRESETS[+b.dataset.preset].servers.join('\n');
  });
  $('#up', el).addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.target;
    await busy(f.querySelector('[type=submit]'), () => attempt(async () => {
      const s = await get('/api/settings');
      await put('/api/settings', { ...s, upstreams: f.upstreams.value.split('\n').map((x) => x.trim()).filter(Boolean), dnssec: f.dnssec.checked });
    }, 'Resolvers saved'));
    load();
  });
  $('#zone', el).addEventListener('submit', async (e) => {
    e.preventDefault();
    const f = e.target;
    await busy(f.querySelector('[type=submit]'), () => attempt(async () => {
      const s = await get('/api/settings');
      await put('/api/settings', { ...s, localZone: f.localZone.value.trim() });
    }, 'Local zone saved'));
    load();
  });
  $('#records', el).addEventListener('click', async (e) => {
    if (e.target.closest('#r-add')) {
      const name = $('#r-name', el).value.trim(), type = $('#r-type', el).value, value = $('#r-value', el).value.trim();
      if (!name || !value) return toastMissing();
      if (await attempt(() => post('/api/records', { name, type, value }), `Added ${name}`)) load();
    }
    const d = e.target.closest('[data-del]');
    if (d && await attempt(() => del('/api/records', { name: d.dataset.name, type: d.dataset.type }), 'Record removed')) load();
  });
  $('#records', el).addEventListener('keydown', (e) => { if (e.key === 'Enter' && e.target.matches('#r-name, #r-value')) { e.preventDefault(); $('#r-add', el).click(); } });

  await load();
  ctx.every(15000, load);
}

function toastMissing() {
  import('../ui.js').then(({ toast }) => toast('Enter a name and a value', 'error'));
}
