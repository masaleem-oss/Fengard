import { get } from './api.js';
import { $, esc, icon, modal, copyText, dateShort } from './ui.js';
import { PLATFORMS, brandIcon } from './meta.js';

export function platformTiles() {
  return `<div class="platforms">${PLATFORMS.map((p) => `
    <button class="platform" data-platform="${p.id}">
      <span class="p-logo">${brandIcon(p.logo)}</span>
      <span class="truncate"><b>${p.name}</b><small>${p.sub}</small></span>
    </button>`).join('')}</div>`;
}

export function wirePlatformTiles(root) {
  root.addEventListener('click', (e) => {
    const b = e.target.closest('[data-platform]');
    if (b) openGuide(b.dataset.platform);
  });
}

let certInfo = null;
let trusted; // undefined means not checked yet null means couldnt check

// fetches a tiny https thing signed by our ca no probe url means cant tell
export async function trustStatus() {
  if (trusted !== undefined) return trusted;
  certInfo ??= await get('/api/certificate').catch(() => null);
  if (!certInfo?.probe) return (trusted = null);
  try {
    const ctl = new AbortController();
    const t = setTimeout(() => ctl.abort(), 4000);
    const r = await fetch(certInfo.probe, { mode: 'cors', cache: 'no-store', signal: ctl.signal });
    clearTimeout(t);
    trusted = r.ok;
  } catch { trusted = false; }
  return trusted;
}

export function trustBadge(t) {
  if (t === true) return `<span class="tag ok">${icon('shield-check', 'icon-sm')}Installed in this browser</span>`;
  if (t === false) return `<span class="tag warn">${icon('shield-alert', 'icon-sm')}Not installed in this browser</span>`;
  return '';
}

export async function openGuide(id) {
  const p = PLATFORMS.find((x) => x.id === id);
  certInfo ??= await get('/api/certificate').catch(() => null);
  const m = modal({
    title: `Install on ${p.name}`,
    subtitle: 'Lets blocked HTTPS sites show the Fengard block page instead of a security error.',
    wide: true,
    body: `
      <div class="row" style="gap:14px;margin-bottom:18px">
        <span class="p-logo-lg">${brandIcon(p.logo)}</span>
        <div class="spacer"><b>${esc(certInfo?.name || 'Fengard Local CA')}</b>
          <div class="t-xs t-3">Unique to this gateway${certInfo ? ` · valid until ${dateShort(certInfo.expires)}` : ''}</div>
          <div style="margin-top:6px">${trustBadge(trusted)}</div></div>
        <a class="btn btn-primary" href="${p.file}" download>${icon('download')}Download</a>
      </div>
      <ol class="cert-steps">${p.steps.map((s) => `<li><span>${s}</span></li>`).join('')}</ol>
      ${p.note ? `<p class="t-xs t-3" style="margin-top:14px">${p.note}</p>` : ''}
      ${certInfo ? `<div style="margin-top:18px"><div class="row t-xs t-3" style="margin-bottom:6px">SHA-256 fingerprint
        <button class="btn btn-ghost btn-sm" data-copy style="margin-left:auto">${icon('copy', 'icon-sm')}Copy</button></div>
        <div class="fingerprint">${esc(certInfo.fingerprint)}</div>
        <p class="t-xs t-3" style="margin-top:8px">Check this matches the certificate details on the device before trusting it.</p></div>` : ''}
      <div class="banner info" style="margin-top:18px">${icon('shield-check')}<div class="banner-text">
        <p>This certificate can only be used for sites your network blocks. It never sees or decrypts your other traffic.</p></div></div>`,
    foot: `<button class="btn" data-close>Done</button>`,
  });
  $('[data-copy]', m.el)?.addEventListener('click', () => copyText(certInfo.fingerprint));
}
