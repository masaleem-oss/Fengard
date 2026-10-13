import { get, put, del } from './api.js';
import { esc, icon, toast } from './ui.js';

// what each add-on is, the router says which are installed
export const ADDONS = {
  vpn: { name: 'Remote access VPN', icon: 'globe-lock', page: 'vpn',
    about: 'A WireGuard server so phones get the same filtering on mobile data, plus the Tailscale page.' },
  vendors: { name: 'Manufacturer names', icon: 'monitor-smartphone',
    about: 'Shows who made each device, like Apple or Samsung, from the IEEE registry. Uses about 0.7 MB of memory once the devices list is opened.' },
  speedtest: { name: 'Speed test', icon: 'gauge', widget: true,
    about: 'Tests the internet speed every night around 4 am, or when you press it, and shows it on the overview.' },
  devicecheck: { name: 'Device check', icon: 'scan-search', widget: true,
    about: 'Checks your devices every week for risky open doors like Telnet, Android debugging or a camera with no password.' },
  presence: { name: "Who's home", icon: 'house', widget: true,
    about: 'Messages you when a phone arrives or leaves, and shows who is home on the overview.' },
  welcome: { name: 'Captive portal', icon: 'smartphone',
    about: 'The first time a new iPhone or iPad joins, it shows a page saying the network is protected, with a Join button.' },
};

let list = [];

export const addons = () => list;
export const info = (id) => list.find((a) => a.id === id);
// wanted and its files are on the router
export const ready = (id) => !!info(id)?.ready;
export const wanted = (id) => !!info(id)?.wanted;

export async function loadAddons() {
  try { list = await get('/api/features'); } catch { list = []; }
  return list;
}

export function size(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// the download icon and size people see on things that aren't installed
export function notInstalledTip(id) {
  const a = info(id);
  return a ? `Not installed · ${size(a.download)} download` : 'Not installed';
}

// installs and waits for the files, the router fetches them from GitHub
export async function install(id, { quiet = false } = {}) {
  list = await put(`/api/features/${id}`);
  // an error left from an earlier try clears once the router has another go
  for (let i = 0; i < 60 && !ready(id) && !(i >= 3 && info(id)?.error && !info(id)?.busy); i++) {
    await new Promise((r) => setTimeout(r, 1000));
    await loadAddons();
  }
  const a = info(id);
  if (!a?.ready) {
    if (!quiet) toast(`${ADDONS[id].name} will finish installing once the router can reach GitHub`, 'warning');
    return false;
  }
  if (!quiet) toast(`${ADDONS[id].name} installed`);
  window.dispatchEvent(new CustomEvent('fg-addons'));
  return true;
}

export async function remove(id) {
  list = await del(`/api/features/${id}`);
  toast(`${ADDONS[id].name} removed`);
  window.dispatchEvent(new CustomEvent('fg-addons'));
}

// shown in place of a page or a card whose add-on isn't installed
export function placeholder(id, admin) {
  const a = ADDONS[id], i = info(id);
  const state = i?.wanted && !i.ready
    ? `<p class="t-sm" style="color:var(--warning)">${esc(i.busy ? 'Downloading…' : i.error || 'Waiting to download')}</p>`
    : '';
  return `<div class="empty addon-empty">${icon(a.icon, 'icon-lg')}<h4>${esc(a.name)} isn't installed</h4><p>${esc(a.about)}</p>${state}
    ${admin && !i?.wanted ? `<button class="btn btn-primary btn-sm" data-install="${id}">${icon('download', 'icon-sm')}Install · ${size(i?.download || 0)}</button>` : ''}
    ${!admin ? '<p class="t-3 t-sm">Ask an admin to install it.</p>' : ''}</div>`;
}
