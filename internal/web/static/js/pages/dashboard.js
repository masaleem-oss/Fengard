import { get, post, put, patch } from '../api.js';
import { $, $$, esc, icon, fmt, compact, pct, ago, until, duration, attempt, busy, isSet, modal, toast } from '../ui.js';
import { stackedBars, bucketize, barList } from '../charts.js';
import { CATEGORY_ICONS } from '../meta.js';
import { platformTiles, wirePlatformTiles, trustStatus, trustBadge } from '../cert.js';
import { state, isAdmin, refreshShell, alertIcon } from '../main.js';
import { startUpdate, updateBanner } from '../update.js';
import { ADDONS, loadAddons, ready, wanted, info, install, notInstalledTip, size as dlSize } from '../addons.js';

const RANGES = [[1, '24h'], [7, '7 days'], [30, '30 days']];
const SIZES = { sm: 'Small', md: 'Medium', lg: 'Large', full: 'Full width' };
const NEXT_SIZE = { sm: 'md', md: 'lg', lg: 'full', full: 'sm' };

// the cards the overview can show, add-on cards come from their add-on
const CORE = {
  stats: { title: 'Totals', size: 'full', bare: true },
  chart: { title: 'DNS traffic', size: 'lg' },
  protection: { title: 'Protection', size: 'sm' },
  blocked: { title: 'Most blocked', size: 'sm' },
  devices: { title: 'Most active devices', size: 'sm' },
  categories: { title: 'Blocked by category', size: 'sm' },
  certificate: { title: 'Fengard certificate', size: 'md' },
  alerts: { title: 'Recent alerts', size: 'md' },
  system: { title: 'System', size: 'full', bare: true },
};
const ADDON_CARDS = ['speedtest', 'presence', 'devicecheck'];
// who's home isn't on it by default, it's under Add card
const DEFAULT = [['stats', 'full'], ['speedtest', 'sm'], ['chart', 'lg'], ['devicecheck', 'full'], ['protection', 'sm'],
  ['blocked', 'sm'], ['devices', 'sm'], ['categories', 'sm'], ['certificate', 'lg'], ['alerts', 'full'], ['system', 'full']]
  .map(([id, size]) => ({ id, size }));

// without the speed test the traffic chart takes its whole row
function standard() {
  return DEFAULT.map((w) => (w.id === 'chart' && !wanted('speedtest') ? { ...w, size: 'full' } : w));
}

const titleOf = (id) => CORE[id]?.title || ADDONS[id]?.name || id;
const known = (id) => CORE[id] || ADDON_CARDS.includes(id);

export async function render(el, ctx) {
  let range = Number(sessionStorage.getItem('fg-range') || 1);
  const [saved] = await Promise.all([get('/api/prefs/dashboard').catch(() => null), loadAddons()]);
  if (!ctx.alive()) return;
  // the saved layout keeps a removed add-on's place so installing it again puts it back
  let layout = (saved?.widgets?.length ? saved.widgets : standard()).filter((w) => known(w.id));
  let editing = false;

  el.innerHTML = `
    <div id="d-banners" class="stack" hidden></div>
    <div class="dash-bar" id="d-bar"></div>
    <div class="dash" id="d-grid"></div>`;
  const grid = $('#d-grid', el);

  // shared data, cards subscribe to what they show
  let series = null, overview = null;
  const onOverview = [], onSeries = [];

  const save = async () => {
    layout = $$('.widget', grid).map((w) => ({ id: w.dataset.w, size: w.dataset.size }))
      .concat(layout.filter((w) => ADDON_CARDS.includes(w.id) && !wanted(w.id)));
    await put('/api/prefs/dashboard', { widgets: layout }).catch((x) => toast(x.message, 'error'));
  };

  const drawBar = () => {
    $('#d-bar', el).innerHTML = editing
      ? `<span class="t-2 t-sm dash-hint">${icon('grip-vertical', 'icon-sm')}Drag a card to move it. Changes save as you go.</span>
         <button class="btn btn-sm" id="d-add">${icon('plus', 'icon-sm')}Add card</button>
         <button class="btn btn-sm btn-ghost" id="d-reset">${icon('rotate-ccw', 'icon-sm')}Reset</button>
         <button class="btn btn-sm btn-primary" id="d-done">${icon('check', 'icon-sm')}Done</button>`
      : `<button class="btn btn-sm btn-ghost" id="d-edit">${icon('sliders-horizontal', 'icon-sm')}Customize</button>`;
    grid.classList.toggle('editing', editing);
  };
  el.addEventListener('click', async (e) => {
    if (e.target.closest('#d-edit')) { editing = true; drawBar(); }
    else if (e.target.closest('#d-done')) { editing = false; drawBar(); }
    else if (e.target.closest('#d-add')) addCard();
    else if (e.target.closest('#d-reset')) {
      await put('/api/prefs/dashboard', { widgets: [] }).catch(() => {});
      layout = standard();
      mountAll();
      toast('Back to the standard overview');
    }
  });

  // one card, its frame and the edit tools around it
  const mount = (w) => {
    const id = w.id, core = CORE[id];
    const box = document.createElement('section');
    box.className = `widget w-${w.size}`;
    box.dataset.w = id;
    box.dataset.size = w.size;
    box.innerHTML = `<div class="w-tools" aria-label="${esc(titleOf(id))}">
        <button class="icon-btn w-grip" type="button" aria-label="Move ${esc(titleOf(id))}, use the arrow keys" title="Drag to move">${icon('grip-vertical')}</button>
        <button class="btn btn-ghost btn-sm" type="button" data-resize title="Change the width">${SIZES[w.size]}</button>
        <button class="icon-btn" type="button" data-hide aria-label="Remove ${esc(titleOf(id))}" title="Remove from the overview">${icon('x')}</button>
      </div>
      ${core?.bare ? `<div class="panel stats" data-body></div>` : `<div class="panel">
        <div class="panel-head"><h3>${esc(titleOf(id))}</h3><span class="sub" data-sub></span><div class="actions" data-actions></div></div>
        <div data-body></div></div>`}`;
    const card = {
      box, body: $('[data-body]', box), sub: $('[data-sub]', box) || document.createElement('span'),
      actions: $('[data-actions]', box) || document.createElement('div'), admin: isAdmin(),
      alive: () => box.isConnected && ctx.alive(),
      every: (ms, fn) => ctx.every(ms, () => (box.isConnected ? fn() : Promise.resolve())),
    };
    if (core) CARDS[id](card);
    else addonCard(id, card);
    return box;
  };

  const addonCard = async (id, card) => {
    if (!ready(id)) {
      const i = info(id);
      card.body.innerHTML = `<div class="empty addon-empty">${icon(ADDONS[id].icon, 'icon-lg')}<p>${esc(i?.busy ? 'Downloading…' : i?.error || 'Waiting to download')}</p></div>`;
      return;
    }
    try {
      const mod = await import(`/features/${id}/widget.js`);
      if (card.alive()) mod.default.render(card);
    } catch (x) {
      card.body.innerHTML = `<p class="t-3 t-sm" style="padding:16px 14px">Couldn't load this card: ${esc(x.message)}</p>`;
    }
  };

  const mountAll = () => {
    onOverview.length = onSeries.length = 0;
    grid.replaceChildren(...layout.filter((w) => !ADDON_CARDS.includes(w.id) || wanted(w.id)).map(mount));
    if (overview) onOverview.forEach((f) => f(overview));
    if (series) onSeries.forEach((f) => f(series));
  };

  // size and remove buttons, then dragging
  grid.addEventListener('click', (e) => {
    const box = e.target.closest('.widget');
    if (!box || !editing) return;
    if (e.target.closest('[data-resize]')) {
      const next = NEXT_SIZE[box.dataset.size];
      box.classList.replace(`w-${box.dataset.size}`, `w-${next}`);
      box.dataset.size = next;
      e.target.closest('[data-resize]').textContent = SIZES[next];
      save();
      // the chart redraws to its new width
      if (box.dataset.w === 'chart' && series) onSeries.forEach((f) => f(series, true));
    } else if (e.target.closest('[data-hide]')) {
      box.remove();
      save();
    }
  });
  grid.addEventListener('keydown', (e) => {
    const grip = e.target.closest('.w-grip');
    if (!grip || !editing) return;
    const box = grip.closest('.widget');
    if (['ArrowUp', 'ArrowLeft'].includes(e.key) && box.previousElementSibling) box.previousElementSibling.before(box);
    else if (['ArrowDown', 'ArrowRight'].includes(e.key) && box.nextElementSibling) box.nextElementSibling.after(box);
    else return;
    e.preventDefault();
    grip.focus();
    save();
  });
  grid.addEventListener('pointerdown', (e) => {
    const box = e.target.closest('.widget');
    if (!box || !editing || e.button > 0 || e.target.closest('button:not(.w-grip)')) return;
    // a finger anywhere else on the card scrolls the page
    if (e.pointerType === 'touch' && !e.target.closest('.w-grip')) return;
    e.preventDefault();
    const start = box.getBoundingClientRect();
    const x0 = e.clientX, y0 = e.clientY, dx = x0 - start.left, dy = y0 - start.top;
    let ghost = null, lastY = y0, raf = 0, lastOver = null;
    // keeps scrolling while the card is held near the top or bottom edge
    const edge = () => {
      const h = window.innerHeight, step = lastY < 80 ? -14 : lastY > h - 80 ? 14 : 0;
      if (step) window.scrollBy(0, step);
      raf = requestAnimationFrame(edge);
    };
    const move = (ev) => {
      lastY = ev.clientY;
      // a press that barely moves is a click, not a drag
      if (!ghost) {
        if (Math.hypot(ev.clientX - x0, ev.clientY - y0) < 5) return;
        ghost = box.cloneNode(true);
        ghost.classList.add('w-ghost');
        Object.assign(ghost.style, { width: `${start.width}px`, height: `${start.height}px` });
        document.body.append(ghost);
        box.classList.add('w-placeholder');
        raf = requestAnimationFrame(edge);
      }
      ghost.style.left = `${ev.clientX - dx}px`;
      ghost.style.top = `${ev.clientY - dy}px`;
      const over = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.widget');
      // react once per card entered, otherwise cards of different sizes swap back and forth
      if (over === lastOver) return;
      lastOver = over;
      if (!over || over === box || over.parentNode !== grid) return;
      const kids = [...grid.children];
      if (kids.indexOf(box) < kids.indexOf(over)) over.after(box);
      else over.before(box);
    };
    // on the window since moving the card in the page drops any pointer capture
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      window.removeEventListener('pointercancel', up);
      if (!ghost) return;
      cancelAnimationFrame(raf);
      ghost.remove();
      box.classList.remove('w-placeholder');
      save();
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
    window.addEventListener('pointercancel', up);
  });

  const addCard = () => {
    const shown = new Set($$('.widget', grid).map((w) => w.dataset.w));
    const rows = [...Object.keys(CORE), ...ADDON_CARDS].filter((id) => !shown.has(id)).map((id) => {
      const addon = ADDON_CARDS.includes(id);
      const missing = addon && !wanted(id);
      const tip = missing ? notInstalledTip(id) : '';
      const btn = !missing ? `<button class="btn btn-sm" data-add="${id}">${icon('plus', 'icon-sm')}Add</button>`
        : isAdmin() ? `<button class="btn btn-sm" data-get="${id}" title="${esc(tip)}">${icon('download', 'icon-sm')}Install · ${dlSize(info(id)?.download || 0)}</button>`
        : `<span class="t-3 t-xs">Not installed</span>`;
      return `<div class="addon-row${missing ? ' missing' : ''}" title="${esc(tip)}">
        <div class="addon-ico">${icon(addon ? ADDONS[id].icon : 'layout-dashboard')}</div>
        <div class="addon-text"><b>${esc(titleOf(id))}</b>${missing ? `<small class="t-3">${icon('download', 'icon-sm')} ${esc(tip)}</small>` : ''}</div>
        <div>${btn}</div></div>`;
    });
    const m = modal({ title: 'Add a card', subtitle: 'Cards from add-ons that aren\'t installed download first.',
      body: rows.length ? `<div class="addon-list">${rows.join('')}</div>` : '<p class="t-3">Every card is already on the overview.</p>' });
    m.el.addEventListener('click', async (e) => {
      const add = e.target.closest('[data-add]'), getIt = e.target.closest('[data-get]');
      const id = add?.dataset.add || getIt?.dataset.get;
      if (!id) return;
      if (getIt && !await busy(getIt, () => install(id).catch((x) => { toast(x.message, 'error'); return false; }))) return;
      const size = CORE[id]?.size || 'sm';
      layout = layout.filter((w) => w.id !== id);
      grid.append(mount({ id, size }));
      if (overview) onOverview.forEach((f) => f(overview));
      if (series) onSeries.forEach((f) => f(series));
      save();
      m.close();
    });
  };

  // core cards

  const stat = (label, ic, value, sub) => `<div class="stat"><div class="stat-label">${icon(ic, 'icon-sm')}${label}</div><div class="stat-value">${value}</div><div class="stat-sub">${sub}</div></div>`;
  const lists = () => state.categories;
  const CARDS = {
    stats(c) {
      c.body.innerHTML = '<div class="stat"><div class="skeleton" style="height:48px"></div></div>'.repeat(4);
      onOverview.push((o) => {
        const st = o.stats;
        const hitRate = o.dns.queries ? (o.dns.cacheHits / o.dns.queries) * 100 : 0;
        // totals for this range and the one before
        const hrs = series?.hours || [];
        const cur = hrs.slice(-range * 24), prev = hrs.slice(-range * 48, -range * 24);
        const sum = (a, k) => a.reduce((s, h) => s + h[k], 0);
        const total = sum(cur, 'total'), blocked = sum(cur, 'blocked');
        const pTotal = sum(prev, 'total'), pBlocked = sum(prev, 'blocked');
        // comparing against a period fengard wasnt running for most of gives silly numbers
        const fullPrev = prev.length && prev.filter((h) => h.total > 0).length >= prev.length * 0.75;
        const delta = (a, b) => {
          if (!b || !fullPrev) return '<span class="delta">no earlier data</span>';
          const d = ((a - b) / b) * 100;
          return `<span class="delta">${d > 0 ? '+' : ''}${d.toFixed(0)}%</span> vs. previous`;
        };
        const rangeLabel = RANGES.find(([d]) => d === range)[1];
        c.body.innerHTML = [
          stat(`Queries · ${rangeLabel}`, 'globe', compact(total), delta(total, pTotal)),
          stat(`Blocked · ${rangeLabel}`, 'shield-ban', compact(blocked), `${pct(blocked, total)} of queries · ${delta(blocked, pBlocked)}`),
          stat('Devices online', 'monitor-smartphone', fmt(o.devices.online), `${fmt(o.devices.known)} known${o.devices.pending ? ` · ${o.devices.pending} pending` : ''}`),
          stat('Response time', 'gauge', `${st.avgMs < 10 ? st.avgMs.toFixed(1) : Math.round(st.avgMs)} ms`, `${hitRate.toFixed(0)}% from cache`),
        ].join('');
      });
    },
    chart(c) {
      c.actions.classList.add('legend');
      c.actions.innerHTML = `<span><i style="background:var(--chart-base)"></i>Allowed</span><span><i style="background:var(--accent)"></i>Blocked</span>`;
      c.actions.insertAdjacentHTML('beforebegin', `<div class="seg" data-range>${RANGES.map(([d, l]) => `<button data-d="${d}" class="${d === range ? 'on' : ''}">${l}</button>`).join('')}</div>`);
      c.body.innerHTML = `<div class="panel-body"><div class="chart" style="height:240px"></div></div>`;
      let last = '';
      onSeries.push((s, force) => {
        const pts = s.hours.slice(-range * 24);
        const sig = JSON.stringify(pts) + document.documentElement.dataset.theme + range;
        if (sig === last && !force) return;
        last = sig;
        stackedBars($('.chart', c.body), bucketize(pts, range), { height: 240 });
      });
      $('[data-range]', c.box).addEventListener('click', async (e) => {
        const b = e.target.closest('[data-d]');
        if (!b) return;
        range = Number(b.dataset.d);
        sessionStorage.setItem('fg-range', range);
        $$('[data-d]', c.box).forEach((x) => x.classList.toggle('on', x === b));
        await loadSeries();
        draw();
      });
    },
    protection(c) {
      c.body.innerHTML = '<div class="status-list"></div>';
      onOverview.push((o) => {
        const st = o.stats;
        const newest = lists().map((x) => x.updated).filter(isSet).sort().pop();
        const rows = [
          o.protection?.paused ? ['warn', 'Filtering', 'Paused'] : ['ok', 'Filtering', `${compact(o.blocklist)} domains`],
          o.firewall.error ? ['bad', 'Firewall', 'Error'] : o.firewall.enabled ? ['ok', 'Firewall', `Applied ${ago(o.firewall.applied)}`] : ['off', 'Firewall', 'Off (DNS-only mode)'],
          ['ok', 'Activity history', st.evictedRecords ? 'Oldest records roll off to keep storage small' : 'Within storage limits'],
          newest ? ['ok', 'Blocklists', `Updated ${ago(newest)}`] : ['warn', 'Blocklists', 'Not downloaded yet'],
          o.dns.upstreamErrors && o.dns.servedStale ? ['warn', 'Upstream DNS', `${fmt(o.dns.upstreamErrors)} errors, serving cache`] : ['ok', 'Upstream DNS', 'Reachable'],
          o.dns.rateLimited ? ['warn', 'Flood protection', `${compact(o.dns.rateLimited)} throttled`] : ['ok', 'Flood protection', 'Quiet'],
          o.protection?.tempAllows ? ['warn', 'Temporary allows', `${o.protection.tempAllows} active`] : ['ok', 'Temporary allows', 'None'],
        ];
        c.body.firstElementChild.innerHTML = rows.map(([s, t, d]) => `<div class="status-row"><i class="${s}"></i><b>${t}</b><small>${esc(d)}</small></div>`).join('');
      });
    },
    blocked(c) {
      c.actions.innerHTML = `<a class="btn btn-ghost btn-sm" href="#activity?blocked=1">Activity</a>`;
      onOverview.push((o) => {
        c.body.innerHTML = barList(o.stats.topBlocked.slice(0, 7), { accent: true, render: (it) => `<span class="mono">${esc(it.name)}</span>`, emptyText: 'Nothing blocked yet' });
      });
    },
    devices(c) {
      c.actions.innerHTML = `<a class="btn btn-ghost btn-sm" href="#devices">Devices</a>`;
      onOverview.push((o) => { c.body.innerHTML = barList(o.stats.topDevices.slice(0, 7), { emptyText: 'No devices yet' }); });
    },
    categories(c) {
      onOverview.push((o) => {
        c.body.innerHTML = barList(o.stats.categories.slice(0, 7).map((x) => ({ ...x, label: labelFor(x.name, lists()) })), {
          accent: true, emptyText: 'Nothing blocked yet',
          render: (it) => `${icon(CATEGORY_ICONS[it.name] || (it.name.startsWith('app:') ? 'smartphone' : 'list'), 'icon-sm')}<span>${esc(it.label)}</span>`,
        });
      });
    },
    certificate(c) {
      c.sub.textContent = 'Shows the block page on HTTPS sites';
      c.body.innerHTML = `<div class="panel-body">${platformTiles()}</div>`;
      wirePlatformTiles(c.body.firstElementChild);
      trustStatus().then((t) => { if (c.alive()) c.actions.innerHTML = trustBadge(t); });
    },
    alerts(c) {
      c.actions.innerHTML = `<a class="btn btn-ghost btn-sm" href="#alerts">All alerts</a>`;
      c.body.className = 'feed';
      onOverview.push((o) => {
        c.body.innerHTML = o.recentAlerts.length ? o.recentAlerts.map((a) => `
          <div class="feed-item"><div class="feed-ico sev-${a.severity}">${icon(alertIcon(a), 'icon-sm')}</div>
            <div class="feed-text truncate"><b class="truncate">${esc(a.title)}</b><small class="truncate">${esc(a.detail || '')}</small></div>
            <div class="feed-time">${ago(a.time)}</div></div>`).join('')
          : `<p class="t-3 t-sm" style="padding:20px 14px">No alerts yet. New devices, access requests and attacks show up here.</p>`;
      });
    },
    system(c) {
      onOverview.push((o) => {
        const sys = o.system, hitRate = o.dns.queries ? (o.dns.cacheHits / o.dns.queries) * 100 : 0;
        c.body.innerHTML = [
          stat('CPU', 'cpu', `${sys.cpuPercent.toFixed(1)}%`, `of one core · ${sys.cores} available`),
          stat('Memory', 'memory-stick', `${sys.heapMB.toFixed(1)} MB`, `${sys.memoryMB.toFixed(0)} MB reserved`),
          stat('Uptime', 'clock', duration(sys.uptimeSec), `${esc(sys.platform)} · Fengard ${esc(sys.appVersion)}`),
          stat('DNS cache', 'database', fmt(o.dns.cacheSize), `${hitRate.toFixed(0)}% hit rate`),
        ].join('');
      });
    },
  };

  const loadSeries = async () => {
    series = await get(`/api/series?days=${Math.max(2, range * 2)}`); // double the range for the comparison
    if (ctx.alive()) onSeries.forEach((f) => f(series));
  };

  const draw = async () => {
    const o = await get('/api/overview');
    if (!ctx.alive()) return;
    overview = o;
    const st = o.stats;
    const banners = [];
    if (st.droppedWrites) banners.push(`<div class="banner warn">${icon('info')}<div class="banner-text"><b>Some activity was not saved</b><p>${fmt(st.droppedWrites)} query records were dropped because logging capacity or storage was unavailable. Filtering continues, but activity history may be incomplete.</p></div></div>`);
    if (o.alertDrops) banners.push(`<div class="banner warn">${icon('bell')}<div class="banner-text"><b>Some alerts were dropped</b><p>${fmt(o.alertDrops)} alerts exceeded capacity or could not be saved. Check available storage and network request volume.</p></div><a class="btn btn-sm" href="#alerts">Alerts</a></div>`);
    const up = await get('/api/update').catch(() => null);
    const upBanner = updateBanner(up, isAdmin(), esc, icon);
    if (o.firewall.error) banners.push(`<div class="banner danger">${icon('octagon-x')}<div class="banner-text"><b>Firewall rules were not applied</b><p>${esc(o.firewall.error)}</p></div><a class="btn btn-sm" href="#firewall">Details</a></div>`);
    if (o.protection?.paused) banners.push(`<div class="banner warn">${icon('pause')}<div class="banner-text"><b>Protection is paused until ${until(o.protection.pausedUntil)}</b><p>Nothing is being filtered. Device and profile pauses still apply.</p></div>
      ${isAdmin() ? `<button class="btn btn-sm" data-resume>Resume now</button>` : ''}</div>`);
    if (o.pendingDevices.length) banners.push(`<div class="banner warn">${icon('monitor-smartphone')}<div class="banner-text">
      <b>${o.devices.pending} new device${o.devices.pending > 1 ? 's are' : ' is'} waiting for approval</b>
      <p>${o.pendingDevices.map((d) => esc(d.hostname || d.vendor || d.mac)).join(', ')}</p></div>
      ${isAdmin() && o.pendingDevices.length === 1 ? `<button class="btn btn-sm btn-primary" data-approve="${esc(o.pendingDevices[0].mac)}">Approve</button>` : ''}
      <a class="btn btn-sm" href="#devices?filter=pending">Review</a></div>`);
    if (upBanner) banners.push(upBanner);
    $('#d-banners', el).innerHTML = banners.join('');
    $('[data-update]', el)?.addEventListener('click', () => startUpdate(up, draw));
    $('#d-banners', el).hidden = !banners.length;
    onOverview.forEach((f) => f(o));
  };

  $('#d-banners', el).addEventListener('click', async (e) => {
    const a = e.target.closest('[data-approve]');
    if (a && await attempt(() => patch(`/api/devices/${encodeURIComponent(a.dataset.approve)}`, { approved: true }), 'Device approved')) { refreshShell(); draw(); }
    if (e.target.closest('[data-resume]') && await attempt(() => post('/api/protection/pause', { minutes: 0 }), 'Protection resumed')) { refreshShell(); draw(); }
  });

  if (!Object.keys(appNames).length) {
    try { appNames = Object.fromEntries((await get('/api/apps')).apps.map((a) => [a.id, a.name])); } catch (e) {}
  }
  drawBar();
  mountAll();
  await loadSeries();
  await draw();
  ctx.every(5000, draw);
  ctx.every(60000, loadSeries);
}

// real app names like TikTok instead of guessing from the id
let appNames = {};

function labelFor(id, lists) {
  if (id.startsWith('app:')) return appNames[id.slice(4)] || id.slice(4).replace(/^\w/, (c) => c.toUpperCase());
  if (id.startsWith('list:')) return 'Custom list';
  return lists.find((l) => l.id === id)?.name || id;
}
