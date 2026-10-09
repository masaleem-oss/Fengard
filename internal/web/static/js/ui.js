// everything renders from template strings so server values have to go through esc

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

export function esc(v) {
  return String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}

export const icon = (name, cls = '') => `<svg class="icon ${cls}" aria-hidden="true"><use href="#i-${name}"></use></svg>`;

// formatting

const nf = new Intl.NumberFormat();
export const fmt = (n) => nf.format(Math.round(Number(n) || 0));
export function compact(n) {
  n = Number(n) || 0;
  if (n < 10000) return fmt(n);
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);
}
export const pct = (a, b) => (b ? ((a / b) * 100).toFixed(1) : '0.0') + '%';
// placeholder ip on whatever subnet the dashboard was opened on or a common home one
export function exampleIP(host) {
  const h = location.hostname;
  const base = /^\d+\.\d+\.\d+\.\d+$/.test(h) && !h.startsWith('127.') ? h.replace(/\.\d+$/, '') : '192.168.1';
  return `${base}.${host}`;
}
export const isSet = (t) => t && !String(t).startsWith('0001');
export const inFuture = (t) => isSet(t) && new Date(t) > Date.now();

export function ago(t) {
  if (!isSet(t)) return 'Never';
  const s = (Date.now() - new Date(t)) / 1000;
  if (s < 45) return 'Just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  if (s < 86400 * 7) return `${Math.round(s / 86400)} d ago`;
  return dateShort(t);
}
export const timeOf = (t) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
export const dateShort = (t) => new Date(t).toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' });
export const dateTime = (t) => new Date(t).toLocaleString([], { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
export function until(t) {
  const d = new Date(t);
  const sameDay = d.toDateString() === new Date().toDateString();
  return sameDay ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : dateTime(t);
}
export function duration(sec) {
  const d = Math.floor(sec / 86400), h = Math.floor(sec / 3600) % 24, m = Math.floor(sec / 60) % 60;
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}

// toasts

export function toast(message, kind = 'success') {
  const el = document.createElement('div');
  el.className = 'toast' + (kind === 'error' ? ' error' : '');
  el.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  el.innerHTML = `${icon(kind === 'error' ? 'circle-alert' : 'circle-check')}<span>${esc(message)}</span>`;
  $('#toasts').append(el);
  setTimeout(() => el.remove(), kind === 'error' ? 6000 : 3500);
}

export async function attempt(fn, ok) {
  try {
    await fn();
    if (ok) toast(ok);
    return true;
  } catch (e) {
    toast(e.message || String(e), 'error');
    return false;
  }
}

export async function busy(btn, fn) {
  if (!btn) return fn();
  const html = btn.innerHTML;
  btn.disabled = true;
  btn.innerHTML = `<span class="spin"></span>${btn.textContent.trim() ? `<span>${esc(btn.textContent.trim())}</span>` : ''}`;
  try {
    return await fn();
  } finally {
    btn.disabled = false;
    btn.innerHTML = html;
  }
}

// overlays

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// backdrop and focus trap and escape to close
function overlay(className, html, { onClose, label } = {}) {
  const prev = document.activeElement;
  const backdrop = document.createElement('div');
  backdrop.className = 'backdrop';
  const el = document.createElement('div');
  el.className = className;
  el.setAttribute('role', 'dialog');
  el.setAttribute('aria-modal', 'true');
  if (label) el.setAttribute('aria-label', label);
  el.innerHTML = html;
  document.body.append(backdrop, el);

  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    backdrop.remove();
    el.remove();
    document.removeEventListener('keydown', onKey, true);
    prev?.focus?.();
    onClose?.();
  };
  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      close();
    } else if (e.key === 'Tab') {
      const items = $$(FOCUSABLE, el).filter((x) => x.offsetParent !== null);
      if (!items.length) return;
      const first = items[0], last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
  };
  document.addEventListener('keydown', onKey, true);
  backdrop.addEventListener('click', close);
  el.addEventListener('click', (e) => { if (e.target.closest('[data-close]')) close(); });
  el.tabIndex = -1;
  const first = $('[autofocus]', el) || (className === 'drawer' ? el : $$(FOCUSABLE, el).find((x) => !x.matches('[data-close]')));
  requestAnimationFrame(() => (first || el).focus({ preventScroll: true }));
  return { el, close };
}

export function modal({ title, subtitle = '', body, foot = '', wide = false, onClose }) {
  return overlay(`modal${wide ? ' wide' : ''}`, `
    <div class="modal-head"><div><h3>${esc(title)}</h3>${subtitle ? `<p>${subtitle}</p>` : ''}</div>
      <button class="icon-btn" data-close aria-label="Close">${icon('x')}</button></div>
    <div class="modal-body">${body}</div>
    ${foot ? `<div class="modal-foot">${foot}</div>` : ''}`, { onClose, label: title });
}

export function drawer(html, { onClose, label } = {}) {
  return overlay('drawer', html, { onClose, label });
}

export function confirmDialog({ title, message, confirm = 'Confirm', danger = false }) {
  return new Promise((resolve) => {
    let result = false;
    const m = modal({
      title, body: `<p class="t-2">${message}</p>`,
      foot: `<button class="btn" data-close>Cancel</button>
             <button class="btn ${danger ? 'btn-danger-solid' : 'btn-primary'}" data-ok>${esc(confirm)}</button>`,
      onClose: () => resolve(result),
    });
    $('[data-ok]', m.el).addEventListener('click', () => { result = true; m.close(); });
    $('[data-ok]', m.el).focus();
  });
}

// items are label icon onClick danger or divider or header
export function menu(anchor, items, { align = 'right' } = {}) {
  closeMenus();
  const el = document.createElement('div');
  el.className = 'menu';
  el.setAttribute('role', 'menu');
  el.innerHTML = items.map((it, i) => {
    if (it === 'divider') return '<hr>';
    if (it.header) return `<div class="menu-label">${esc(it.header)}</div>`;
    if (it.html) return it.html;
    return `<button role="menuitem" data-i="${i}" class="${it.danger ? 'danger' : ''}">${it.icon ? icon(it.icon) : ''}${esc(it.label)}</button>`;
  }).join('');
  document.body.append(el);
  place(el, anchor, align);
  el.addEventListener('click', (e) => {
    const b = e.target.closest('[data-i]');
    if (!b) return;
    closeMenus();
    items[Number(b.dataset.i)].onClick?.();
  });
  el.addEventListener('keydown', (e) => {
    const btns = $$('button', el);
    const i = btns.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { btns[(i + 1) % btns.length]?.focus(); e.preventDefault(); }
    if (e.key === 'ArrowUp') { btns[(i - 1 + btns.length) % btns.length]?.focus(); e.preventDefault(); }
  });
  setTimeout(() => $('button', el)?.focus(), 0);
  return el;
}

export function popover(anchor, html, { align = 'right' } = {}) {
  closeMenus();
  const el = document.createElement('div');
  el.className = 'popover menu-like';
  el.innerHTML = html;
  document.body.append(el);
  place(el, anchor, align);
  return el;
}

function place(el, anchor, align) {
  const r = anchor.getBoundingClientRect();
  const w = el.offsetWidth, h = el.offsetHeight;
  let left = align === 'right' ? r.right - w : r.left;
  left = Math.max(8, Math.min(left, innerWidth - w - 8));
  let top = r.bottom + 6;
  if (top + h > innerHeight - 8) top = Math.max(8, r.top - h - 6);
  el.style.left = left + 'px';
  el.style.top = top + 'px';
}

export function closeMenus() {
  $$('.menu, .popover').forEach((m) => m.remove());
}
document.addEventListener('mousedown', (e) => {
  if (!e.target.closest('.menu, .popover, [data-menu-anchor]')) closeMenus();
});
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeMenus(); });
addEventListener('resize', closeMenus);
addEventListener('scroll', closeMenus, true);

// render helpers

export function empty({ icon: ic = 'inbox', title, text = '', action = '' }) {
  return `<div class="empty">${icon(ic, 'icon-lg')}<h4>${esc(title)}</h4>${text ? `<p>${text}</p>` : ''}${action}</div>`;
}

export function switchInput(attrs = '', checked = false, label = '') {
  return `<label class="switch"><input type="checkbox" ${checked ? 'checked' : ''} ${attrs} ${label ? `aria-label="${esc(label)}"` : ''}><span></span></label>`;
}

export const statusBadge = (action) => ({
  allowed: `<span class="badge b-neutral">Allowed</span>`,
  safesearch: `<span class="badge b-info">SafeSearch</span>`,
  blocked: `<span class="badge b-accent">Blocked</span>`,
  paused: `<span class="badge b-warning">Paused</span>`,
  quarantined: `<span class="badge b-warning">Pending</span>`,
}[action] || `<span class="badge b-neutral">${esc(action)}</span>`);

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast('Copied to clipboard');
  } catch {
    toast("Couldn't copy, select the text manually", 'error');
  }
}

export function download(filename, text, type = 'text/plain') {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([text], { type }));
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}
