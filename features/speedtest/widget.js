import { get, post } from '/js/api.js';
import { esc, icon, isSet, attempt } from '/js/ui.js';

// the speed test card, numbers and two bars like a router app

const two = (v) => (Number(v) || 0).toFixed(2);

// a round number just above the fastest result so the bars have room
function scale(st) {
  const top = Math.max(st.latest?.downMbps || 0, st.latest?.upMbps || 0, st.typicalMbps || 0,
    ...st.history.filter((r) => !r.error).map((r) => Math.max(r.downMbps, r.upMbps)));
  return [10, 25, 50, 100, 200, 300, 500, 1000, 2000, 5000, 10000].find((s) => s >= top * 1.05) || 10000;
}

function outageLine(list) {
  const week = list.filter((o) => Date.now() - new Date(o.start) < 7 * 864e5);
  if (!week.length) return '';
  const mins = week.reduce((s, o) => s + ((isSet(o.end) ? new Date(o.end) : Date.now()) - new Date(o.start)) / 6e4, 0);
  return `${week.length} outage${week.length > 1 ? 's' : ''} this week, ${Math.max(1, Math.round(mins))} min in total`;
}

function testedAt(st) {
  if (st?.testing) return 'Testing now…';
  if (!st?.latest) return '';
  const d = new Date(st.latest.time);
  const day = d.toDateString() === new Date().toDateString() ? 'Today' : d.toLocaleDateString([], { day: 'numeric', month: 'long' });
  return `${day} at ${d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}`;
}

function card(st) {
  if (!st || !st.available) return `<p class="t-3 t-sm" style="padding:16px 14px">Not available on this system.</p>`;
  const r = st.latest;
  const last = st.history[0];
  const failed = !st.testing && last?.error && (!r || new Date(last.time) > new Date(r.time)) ? `The last test didn't work: ${last.error}.` : '';
  if (!r && !st.testing) return `<p class="t-3 t-sm" style="padding:16px 14px">${failed ? esc(failed) : `No speed test yet. ${st.daily ? 'One runs every night around 4 am, or run one now.' : 'Run one now.'}`}</p>`;
  const max = scale(st);
  const mbs = (v) => `${two(v)}<span>Mb/s</span>`;
  const dash = '<span class="t-3">–</span>';
  const out = outageLine(st.outages);
  const nums = (down, up) => `<div class="speed-nums">
        <div><small>Download <span class="dl">${icon('arrow-down', 'icon-sm')}</span></small><b>${down}</b></div>
        <div><small>Upload <span class="ul">${icon('arrow-up', 'icon-sm')}</span></small><b>${up}</b></div>
      </div>`;

  // while it runs the bars make way for live ping and jitter, then the speeds fill in
  if (st.testing) {
    const p = st.live || { phase: 'ping' };
    const phase = { ping: 'Checking ping', download: 'Testing download', upload: 'Testing upload' }[p.phase] || 'Starting';
    const ms = (v) => (v ? `${Math.round(v)}<span>ms</span>` : dash);
    return `<div class="panel-body speed">
      ${nums(p.downMbps ? mbs(p.downMbps) : dash, p.phase === 'upload' && p.upMbps ? mbs(p.upMbps) : dash)}
      <div class="speed-live"><small>${phase}…</small>
        <div class="speed-nums"><div><small>Ping</small><b>${ms(p.pingMs)}</b></div><div><small>Jitter</small><b>${ms(p.pingMs && p.jitterMs)}</b></div></div></div>
    </div>`;
  }
  return `<div class="panel-body speed">
      ${nums(mbs(r.downMbps), mbs(r.upMbps))}
      <div class="speed-bars">
        <i class="dl" style="width:${Math.min(100, (r.downMbps / max) * 100)}%"></i>
        <i class="ul" style="width:${Math.min(100, (r.upMbps / max) * 100)}%"></i>
        <span>${max >= 1000 ? `${max / 1000} Gb/s` : `${max} Mb/s`}</span>
      </div>
      <div class="speed-nums small">
        <div><small>Latency</small><b>${Math.round(r.pingMs)} ms</b></div>
        <div><small>Jitter</small><b>${Math.round(r.jitterMs || 0)} ms</b></div>
        ${r.via ? `<div style="grid-column:1/-1"><small>Server</small><b>${esc(r.via)}</b></div>` : ''}
      </div>
      ${failed ? `<p class="speed-note">${esc(failed)}</p>` : ''}
      ${out ? `<p class="speed-note">${esc(out)}</p>` : ''}
    </div>`;
}

export default {
  id: 'speedtest',
  title: 'Speed test',
  size: 'sm',
  render(w) {
    if (w.admin) w.actions.innerHTML = `<button class="btn btn-ghost btn-sm" data-speedtest>Run test</button>`;
    const show = (net) => {
      w.body.innerHTML = card(net);
      w.sub.textContent = testedAt(net);
      const run = w.actions.querySelector('[data-speedtest]');
      if (run) run.disabled = !net?.available || net.testing || !net.online;
    };
    // a running test refreshes quickly so ping and speed tick along live
    let following = false;
    const follow = async () => {
      if (following) return;
      following = true;
      while (w.alive()) {
        await new Promise((r) => setTimeout(r, 700));
        const net = await get('/api/internet').catch(() => null);
        if (!w.alive()) break;
        show(net);
        if (!net?.testing) break;
      }
      following = false;
    };
    const draw = async () => {
      const net = await get('/api/internet').catch(() => null);
      if (!w.alive()) return;
      show(net);
      if (net?.testing) follow();
    };
    w.actions.addEventListener('click', async (e) => {
      if (e.target.closest('[data-speedtest]') && await attempt(() => post('/api/internet/speedtest'), 'Speed test started')) draw();
    });
    draw();
    w.every(3000, draw);
  },
};
