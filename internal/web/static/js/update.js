import { get, post } from './api.js';
import { $, esc, modal, confirmDialog, attempt } from './ui.js';

// each big version line gets a name, add the next one when it ships
const NAMES = { 1: 'Midgard' };

export function codename(version) {
  return NAMES[parseInt(version, 10)] || '';
}

// shared by the dashboard banner and settings
export async function startUpdate(u, after) {
  const ok = await confirmDialog({
    title: `Update to Fengard ${u.latest}${u.prerelease ? ' preview' : ''}?`,
    message: 'Fengard downloads the update, checks it and restarts. DNS keeps working the whole time, and if the new version doesn\'t start the current one comes back on its own. It takes about a minute.',
    confirm: 'Update now',
  });
  if (ok && await attempt(() => post('/api/update/install'), 'Update started')) after?.();
}

// the dashboard banner for an update, or nothing
export function updateBanner(u, admin, esc, icon) {
  if (!u || u.disabled) return '';
  if (u.installing) {
    return `<div class="banner info">${icon('refresh-cw')}<div class="banner-text"><b>Switching to Fengard ${esc(u.to || u.latest)}</b>
      <p>Fengard restarts in a moment. DNS keeps working while it does.</p></div></div>`;
  }
  const whatsNew = u.url ? `<a class="btn btn-sm" href="${esc(u.url)}" target="_blank" rel="noopener">What's new</a>` : '';
  if (u.available) {
    return `<div class="banner info">${icon('download')}<div class="banner-text"><b>Fengard ${esc(u.latest)}${u.prerelease ? ' preview' : ''} is available</b>
      <p>${u.canInstall ? `You're on ${esc(u.current)}. It supports this router and installs in about a minute.` : 'Download the new kit from GitHub to install it.'}</p></div>
      ${admin && u.canInstall ? `<button class="btn btn-sm btn-primary" data-update>Update now</button>` : ''}${whatsNew}</div>`;
  }
  if (u.newer && !u.supported) {
    return `<div class="banner info">${icon('info')}<div class="banner-text"><b>Fengard ${esc(u.latest)} doesn't support this router</b>
      <p>${esc(u.reason || '')} You can keep using ${esc(u.current)}.</p></div>${whatsNew}</div>`;
  }
  return '';
}

const FIRST_WITH_HOLD = '1.3.1.2';

function older(a, b) {
  const pa = String(a).split('.').map(Number), pb = String(b).split('.').map(Number);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) < (pb[i] || 0);
  }
  return false;
}

// go back to an older release, same checks as an update and automatic updates hold at it
export async function startRollback(u, after) {
  const pick = modal({
    title: 'Go back to an older version',
    body: '<p class="t-2">Looking up older versions…</p>',
    foot: '<button class="btn" data-close>Cancel</button><button class="btn btn-primary" data-ok disabled>Go back</button>',
  });
  const body = $('.modal-body', pick.el), ok = $('[data-ok]', pick.el);
  let list;
  try {
    list = (await get('/api/update/older')).filter((o) => o.supported);
  } catch (e) {
    body.innerHTML = `<p class="t-sm" style="color:var(--danger)">${esc(e.message)}</p>`;
    return;
  }
  if (!list.length) {
    body.innerHTML = `<p class="t-2">There's no older version this router can go back to. Versions before ${esc('1.3.0')} can't be picked here.</p>`;
    return;
  }
  const preferred = list.find((o) => o.version === u.previous) ? u.previous : list[0].version;
  body.innerHTML = `
    <div class="field"><label class="label" for="rb-version">Version</label>
    <select class="select" id="rb-version">${list.map((o) => `<option value="${esc(o.version)}" ${o.version === preferred ? 'selected' : ''}>Fengard ${esc(o.version)}${o.prerelease ? ' preview' : ''}${o.version === u.previous ? ' (what you had before)' : ''}</option>`).join('')}</select></div>
    <ul class="t-2 t-sm" style="margin:12px 0 0 18px;display:grid;gap:6px">
      <li>Your settings, devices and account stay. A copy of your settings from ${esc(u.current)} is saved first.</li>
      <li>Fengard checks the older version starts before switching. If it doesn't answer DNS afterwards, ${esc(u.current)} comes straight back.</li>
      <li id="rb-hold"></li>
      <li>Settings for features the older version doesn't have are ignored by it.</li>
    </ul>`;
  // versions before FIRST_WITH_HOLD would install the version just left overnight
  const hold = () => {
    const v = $('#rb-version', body).value;
    $('#rb-hold', body).textContent = older(v, FIRST_WITH_HOLD) && u.autoUpdate
      ? `${v} doesn't know to skip ${u.current}, so automatic updates get turned off. Turn them back on in Settings whenever you like.`
      : `Automatic updates won't bring ${u.current} back. Anything newer still installs, and you can update by hand any time.`;
  };
  hold();
  $('#rb-version', body).addEventListener('change', hold);
  ok.disabled = false;
  ok.addEventListener('click', async () => {
    const version = $('#rb-version', body).value;
    pick.close();
    if (await attempt(() => post('/api/update/rollback', { version }), `Going back to ${version}`)) after?.();
  });
}
