// colors come from css vars so both themes work
import { esc, fmt, compact } from './ui.js';

// rounds the axis max up to a nice number
function niceMax(v) {
  if (v <= 0) return 4;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

// folds hourly points into daily when theres too many
export function bucketize(hours, days) {
  if (days <= 2) return hours.map((h) => ({ ...h, span: 'hour' }));
  const byDay = new Map();
  for (const h of hours) {
    const d = new Date(h.time);
    const key = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const b = byDay.get(key) || { time: new Date(key).toISOString(), total: 0, blocked: 0, span: 'day' };
    b.total += h.total;
    b.blocked += h.blocked;
    byDay.set(key, b);
  }
  return [...byDay.values()];
}

export function stackedBars(container, data, { height = 220 } = {}) {
  const W = container.clientWidth || 600, H = height;
  const pad = { l: 44, r: 8, t: 10, b: 26 };
  const iw = W - pad.l - pad.r, ih = H - pad.t - pad.b;
  const max = niceMax(Math.max(...data.map((d) => d.total), 0));
  const n = Math.max(1, data.length);
  const slot = iw / n;
  const bw = Math.max(2, Math.min(28, slot - 4));
  const y = (v) => pad.t + ih - (v / max) * ih;
  const daily = data[0]?.span === 'day';
  const every = daily ? Math.ceil(n / (W < 520 ? 5 : 10)) : (W < 520 ? 6 : 3);
  const label = (d) => {
    const t = new Date(d.time);
    return daily ? t.toLocaleDateString([], { day: 'numeric', month: 'short' }) : `${String(t.getHours()).padStart(2, '0')}:00`;
  };

  const ticks = [0, max / 4, max / 2, (3 * max) / 4, max];
  let svg = `<svg viewBox="0 0 ${W} ${H}" height="${H}" role="img" aria-label="Queries over time">`;
  for (const t of ticks) {
    svg += `<line class="grid-line" x1="${pad.l}" x2="${W - pad.r}" y1="${y(t)}" y2="${y(t)}"/>`;
    svg += `<text class="axis-label" x="${pad.l - 8}" y="${y(t) + 4}" text-anchor="end">${compact(t)}</text>`;
  }
  data.forEach((d, i) => {
    const x = pad.l + i * slot + (slot - bw) / 2;
    const allowed = d.total - d.blocked;
    const hA = (allowed / max) * ih, hB = (d.blocked / max) * ih;
    if (hB > 0) svg += `<rect class="b-accent" x="${x}" y="${pad.t + ih - hB}" width="${bw}" height="${hB}" rx="${Math.min(3, bw / 3)}"/>`;
    if (hA > 0) {
      const gap = hB > 0 ? 2 : 0;
      svg += `<rect class="b-base" x="${x}" y="${pad.t + ih - hB - gap - hA}" width="${bw}" height="${Math.max(0, hA)}" rx="${Math.min(3, bw / 3)}"/>`;
    }
    if (i % every === 0) {
      svg += `<text class="axis-label" x="${pad.l + i * slot + slot / 2}" y="${H - 6}" text-anchor="middle">${label(d)}</text>`;
    }
    svg += `<rect class="hover-col" data-i="${i}" x="${pad.l + i * slot}" y="${pad.t}" width="${slot}" height="${ih}" rx="4"/>`;
  });
  svg += '</svg><div class="tooltip" hidden></div>';
  container.innerHTML = svg;

  const tip = container.querySelector('.tooltip');
  const svgEl = container.querySelector('svg');
  const cols = [...container.querySelectorAll('.hover-col')];
  const show = (i) => {
    cols.forEach((c, j) => c.classList.toggle('on', j === i));
    const d = data[i];
    const start = new Date(d.time);
    let when;
    if (daily) when = start.toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' });
    else {
      const f = (t) => t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      when = `${f(start)} – ${f(new Date(start.getTime() + 3600e3))}`;
    }
    tip.innerHTML = `<b>${esc(when)}</b>
      <div class="tt-row"><i style="background:var(--chart-base)"></i><span>Allowed</span><span>${fmt(d.total - d.blocked)}</span></div>
      <div class="tt-row"><i style="background:var(--accent)"></i><span>Blocked</span><span>${fmt(d.blocked)}</span></div>`;
    tip.hidden = false;
    const scale = svgEl.getBoundingClientRect().width / W;
    let left = (pad.l + i * slot + slot / 2) * scale;
    left = Math.max(70, Math.min(left, container.clientWidth - 70));
    tip.style.left = left + 'px';
    tip.style.top = pad.t * scale + 'px';
  };
  container.onmousemove = (e) => {
    const c = e.target.closest('.hover-col');
    if (c) show(Number(c.dataset.i));
  };
  container.onmouseleave = () => { tip.hidden = true; cols.forEach((c) => c.classList.remove('on')); };
}

export function barList(items, { accent = false, render = (it) => esc(it.name), emptyText = 'Nothing yet' } = {}) {
  if (!items.length) return `<p class="t-3 t-sm" style="padding:16px 14px">${esc(emptyText)}</p>`;
  const max = Math.max(...items.map((i) => i.count), 1);
  return `<div class="bar-list">${items.map((it) => `
    <div class="bar-item ${accent ? 'accent' : ''}" title="${esc(it.name)}: ${fmt(it.count)}">
      <div class="bar-fill" style="width:${Math.max(2, (it.count / max) * 100)}%"></div>
      <div class="name">${render(it)}</div>
      <div class="val">${fmt(it.count)}</div>
    </div>`).join('')}</div>`;
}
