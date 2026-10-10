import { get, post, put } from '../api.js';
import { $, $$, esc, icon, timeOf, dateShort, empty, attempt, isSet } from '../ui.js';
import { refreshShell, alertIcon, isAdmin } from '../main.js';

const SEVERITIES = [['all', 'All'], ['critical', 'Critical'], ['warning', 'Warning'], ['info', 'Info']];

export async function render(el, ctx) {
  let data = { alerts: [], readAt: null }, devices = [], groups = [], sev = 'all';
  el.innerHTML = `
    <div id="alert-drops" class="banner warn" hidden></div>
    <div class="panel">
      <div class="toolbar"><div class="seg" id="sev">${SEVERITIES.map(([id, l]) => `<button data-s="${id}" class="${id === sev ? 'on' : ''}">${l}</button>`).join('')}</div>
        <span class="spacer"></span><button class="btn btn-sm" id="read">${icon('check', 'icon-sm')}Mark all as read</button></div>
      <div id="list"></div>
    </div>`;

  const dayLabel = (t) => {
    const d = new Date(t), today = new Date();
    const y = new Date(); y.setDate(today.getDate() - 1);
    if (d.toDateString() === today.toDateString()) return 'Today';
    if (d.toDateString() === y.toDateString()) return 'Yesterday';
    return dateShort(t);
  };
  const groupOf = (mac) => { const d = devices.find((x) => x.mac === mac); return groups.find((g) => g.id === d?.group); };

  const draw = () => {
    const dropped = $('#alert-drops', el);
    dropped.hidden = !data.dropped;
    dropped.innerHTML = data.dropped ? `${icon('info')}<div class="banner-text"><b>Some alerts were dropped</b><p>${Number(data.dropped).toLocaleString()} alerts exceeded capacity or could not be saved. Check available storage and network request volume.</p></div>` : '';
    const list = data.alerts.filter((a) => sev === 'all' || a.severity === sev);
    if (!list.length) {
      $('#list', el).innerHTML = empty({ icon: 'bell', title: 'No alerts', text: sev === 'all' ? 'New devices, access requests, floods and outages show up here.' : 'Nothing at this severity.' });
      return;
    }
    let day = '';
    const readAt = isSet(data.readAt) ? new Date(data.readAt) : new Date(0);
    $('#list', el).innerHTML = `<div class="feed">${list.map((a, i) => {
      const label = dayLabel(a.time);
      const head = label !== day ? `<div class="section-title" style="padding:12px 14px 4px;margin:0;border-bottom:1px solid var(--border)">${(day = label)}</div>` : '';
      const unread = new Date(a.time) > readAt;
      let actions = '';
      if (a.kind === 'access_request' && a.domain && isAdmin()) {
        const g = groupOf(a.mac);
        actions = `<div class="feed-actions">
          <button class="btn btn-sm" data-i="${i}" data-act="temp">${icon('clock', 'icon-sm')}Allow 1 hour${g ? ` for ${esc(g.name)}` : ''}</button>
          ${g ? `<button class="btn btn-sm" data-i="${i}" data-act="group">${icon('check', 'icon-sm')}Always allow for ${esc(g.name)}</button>` : ''}
          <button class="btn btn-sm" data-i="${i}" data-act="all">Always allow for everyone</button>
          <a class="btn btn-ghost btn-sm" href="#filtering?check=${encodeURIComponent(a.domain)}">Why blocked?</a></div>`;
      } else if (a.kind === 'time_request' && isAdmin()) {
        const g = groupOf(a.mac);
        actions = g ? `<div class="feed-actions">
          ${[15, 30, 60].map((m) => `<button class="btn btn-sm" data-i="${i}" data-act="bonus" data-min="${m}" data-g="${esc(g.id)}">${icon('timer', 'icon-sm')}+${m === 60 ? '1 h' : m + ' min'} for ${esc(g.name)}</button>`).join('')}
          <a class="btn btn-ghost btn-sm" href="#profiles?id=${encodeURIComponent(g.id)}&tab=time">Time settings</a></div>` : '';
      } else if (a.kind === 'new_device' && a.mac) {
        actions = `<div class="feed-actions"><a class="btn btn-ghost btn-sm" href="#devices?mac=${encodeURIComponent(a.mac)}">View device</a></div>`;
      }
      return `${head}<div class="feed-item" style="${unread ? 'background:var(--accent-soft)' : ''}">
        <div class="feed-ico sev-${a.severity}">${icon(alertIcon(a), 'icon-sm')}</div>
        <div class="feed-text"><b>${esc(a.title)}</b>${a.detail ? `<small>${esc(a.detail)}</small>` : ''}${actions}</div>
        <div class="feed-time">${timeOf(a.time)}</div></div>`;
    }).join('')}</div>`;
  };

  const load = async () => {
    [data, devices, groups] = await Promise.all([get('/api/alerts'), get('/api/devices').catch(() => []), get('/api/groups').catch(() => [])]);
    if (ctx.alive()) draw();
  };
  $('#sev', el).addEventListener('click', (e) => {
    const b = e.target.closest('[data-s]');
    if (!b) return;
    sev = b.dataset.s;
    $$('#sev button', el).forEach((x) => x.classList.toggle('on', x === b));
    draw();
  });
  $('#read', el).addEventListener('click', async () => { await post('/api/alerts/read'); await load(); refreshShell(); });
  $('#list', el).addEventListener('click', async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const a = data.alerts.filter((x) => sev === 'all' || x.severity === sev)[+b.dataset.i];
    const g = groupOf(a.mac);
    if (b.dataset.act === 'temp') {
      if (await attempt(() => post('/api/rules/temp', { domain: a.domain, group: g?.id || '', minutes: 60 }), `${a.domain} allowed for an hour`)) load();
    } else if (b.dataset.act === 'group' && g) {
      const { devices: _, default: __, ...body } = structuredClone(g);
      body.allow = [...new Set([...(body.allow || []), a.domain])];
      if (await attempt(() => put(`/api/groups/${g.id}`, body), `${a.domain} allowed for ${g.name}`)) load();
    } else if (b.dataset.act === 'all') {
      if (await attempt(() => post('/api/rules', { kind: 'allow', domain: a.domain }), `${a.domain} allowed for everyone`)) load();
    } else if (b.dataset.act === 'bonus' && g) {
      const m = +b.dataset.min;
      if (await attempt(() => post(`/api/groups/${g.id}/bonus`, { minutes: m }), `${g.name} got ${m === 60 ? '1 hour' : m + ' minutes'} more today`)) load();
    }
  });

  await load();
  ctx.every(15000, load);
}
