import { get, put } from '../api.js';
import { $, esc, icon, ago, attempt, copyText, switchInput, isSet } from '../ui.js';
import { isAdmin, refreshShell } from '../main.js';

export async function render(el, ctx) {
  const admin = isAdmin();
  el.innerHTML = `
    <div id="fw-banner"></div>
    <div class="grid g-2">
      <div class="panel"><div class="panel-head"><h3>Protections</h3></div><div id="toggles"></div></div>
      <div class="panel"><div class="panel-head"><h3>Always on</h3><span class="sub">Part of every ruleset</span></div><div class="status-list" id="always"></div></div>
    </div>
    <div class="panel">
      <div class="panel-head"><h3>Active ruleset</h3><span class="sub" id="applied"></span>
        <div class="actions"><button class="btn btn-sm" id="copy">${icon('copy', 'icon-sm')}Copy</button></div></div>
      <div class="panel-body"><div class="code" id="rules"></div></div>
    </div>`;

  let settings, status;
  const draw = () => {
    $('#fw-banner', el).innerHTML = status.error
      ? `<div class="banner danger">${icon('octagon-x')}<div class="banner-text"><b>The ruleset couldn't be applied</b><p class="mono">${esc(status.error)}</p></div></div>`
      : status.enabled ? '' : `<div class="banner info">${icon('info')}<div class="banner-text"><b>DNS-only mode</b><p>Fengard is running on a computer rather than on the router, so it filters by DNS but doesn't apply firewall rules: port forwards, bypass blocking and flood limits need a router install. The rules below are what a router install would apply.</p></div></div>`;
    $('#applied', el).textContent = status.enabled ? (isSet(status.applied) ? `Applied ${ago(status.applied)}` : 'Not applied yet') : 'Preview';

    const ro = admin ? '' : 'disabled';
    $('#toggles', el).innerHTML = `
      <div class="setting"><div class="setting-text"><b>Stop filter bypass</b><small>Redirects DNS sent elsewhere back to Fengard, blocks encrypted DNS (DoH, DoT, DoQ), and turns off Firefox DoH and iCloud Private Relay.</small></div>
        ${switchInput(`data-set="blockBypass" ${ro}`, settings.blockBypass, 'Stop filter bypass')}</div>
      <div class="setting"><div class="setting-text"><b>Quarantine new devices</b><small>New devices get no internet until an admin approves them.</small></div>
        ${switchInput(`data-set="quarantineNew" ${ro}`, settings.quarantineNew, 'Quarantine new devices')}</div>
      <div class="setting"><div class="setting-text"><b>Per-device DNS limit</b><small>${settings.clientRateQps} queries/second before a device is throttled; the kernel drops floods at twice that.</small></div>
        <a class="btn btn-ghost btn-sm" href="#settings">Change</a></div>`;

    const rows = [
      ['Service ports closed to the internet', 'DNS, block page and dashboard answer only the local network'],
      ['SYN flood limit', 'New connections per source from the internet'],
      ['Ping flood limit', 'ICMP echo requests from the internet'],
      ['DNS flood limit', 'A device\'s DNS packets are dropped in the kernel when it floods'],
      ['Invalid packet drop', 'Packets outside any valid connection'],
    ];
    $('#always', el).innerHTML = rows.map(([t, d]) => `<div class="status-row"><i class="${status.enabled ? 'ok' : 'off'}"></i><b>${t}</b><small>${d}</small></div>`).join('');

    $('#rules', el).innerHTML = (status.ruleset || '# Not generated yet').split('\n').map((l) => {
      let h = esc(l);
      if (/^\s*#/.test(l)) h = `<span class="c">${h}</span>`;
      else h = h.replace(/\b(table|chain|set|type|hook|priority|policy|elements|flags|drop|accept|reject|dnat|counter|update|limit)\b/g, '<span class="k">$1</span>');
      return `<span class="ln">${h}</span>`;
    }).join('');
  };

  const load = async () => {
    [settings, status] = await Promise.all([get('/api/settings'), get('/api/firewall')]);
    if (ctx.alive()) draw();
  };

  el.addEventListener('change', async (e) => {
    const key = e.target.dataset.set;
    if (!key) return;
    const value = e.target.checked;
    if (await attempt(() => put('/api/settings', { ...settings, [key]: value }), 'Saved')) { setTimeout(load, 500); refreshShell(); }
    else e.target.checked = !value;
  });
  $('#copy', el).addEventListener('click', () => copyText(status?.ruleset || ''));

  await load();
  ctx.every(15000, load);
}
