import { post } from './api.js';
import { confirmDialog, attempt } from './ui.js';

// each big version line gets a name, add the next one when it ships
const NAMES = { 1: 'Midgard' };

export function codename(version) {
  return NAMES[parseInt(version, 10)] || '';
}

// shared by the dashboard banner and settings
export async function startUpdate(u, after) {
  const ok = await confirmDialog({
    title: `Update to Fengard ${u.latest}?`,
    message: 'Fengard downloads the update, checks it and restarts. DNS keeps working the whole time, and if the new version doesn\'t start the current one comes back on its own. It takes about a minute.',
    confirm: 'Update now',
  });
  if (ok && await attempt(() => post('/api/update/install'), 'Update started')) after?.();
}

// the dashboard banner for an update, or nothing
export function updateBanner(u, admin, esc, icon) {
  if (!u || u.disabled) return '';
  if (u.installing) {
    return `<div class="banner info">${icon('refresh-cw')}<div class="banner-text"><b>Updating to Fengard ${esc(u.latest)}</b>
      <p>Fengard restarts in a moment. DNS keeps working while it does.</p></div></div>`;
  }
  const whatsNew = u.url ? `<a class="btn btn-sm" href="${esc(u.url)}" target="_blank" rel="noopener">What's new</a>` : '';
  if (u.available) {
    return `<div class="banner info">${icon('download')}<div class="banner-text"><b>Fengard ${esc(u.latest)} is available</b>
      <p>${u.canInstall ? `You're on ${esc(u.current)}. It supports this router and installs in about a minute.` : 'Download the new kit from GitHub to install it.'}</p></div>
      ${admin && u.canInstall ? `<button class="btn btn-sm btn-primary" data-update>Update now</button>` : ''}${whatsNew}</div>`;
  }
  if (u.newer && !u.supported) {
    return `<div class="banner info">${icon('info')}<div class="banner-text"><b>Fengard ${esc(u.latest)} doesn't support this router</b>
      <p>${esc(u.reason || '')} You can keep using ${esc(u.current)}.</p></div>${whatsNew}</div>`;
  }
  return '';
}
