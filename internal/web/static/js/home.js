import { esc, icon, ago, isSet } from './ui.js';

// speed test, whos home and the device check cards

export function mbps(bytesPerSec) {
  const bits = (Number(bytesPerSec) || 0) * 8;
  if (bits >= 1e9) return `${(bits / 1e9).toFixed(2)} Gbps`;
  if (bits >= 1e6) return `${(bits / 1e6).toFixed(bits >= 1e8 ? 0 : 1)} Mbps`;
  if (bits >= 1e3) return `${Math.round(bits / 1e3)} kbps`;
  return '0 kbps';
}

export function bytes(n) {
  n = Number(n) || 0;
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)} TB`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(n >= 1e10 ? 0 : 1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`;
  return `${n} B`;
}

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

export function testedAt(st) {
  if (st?.testing) return 'Testing now…';
  if (!st?.latest) return '';
  const d = new Date(st.latest.time);
  const day = d.toDateString() === new Date().toDateString() ? 'Today' : d.toLocaleDateString([], { day: 'numeric', month: 'long' });
  return `${day} at ${d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}`;
}

export function speedCard(st) {
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
export function homeCard(p) {
  if (!p || !p.devices.length) {
    return `<p class="t-3 t-sm" style="padding:16px 14px">Open a phone in <a href="#devices">Devices</a> and turn on <b>Arrival alerts</b> to see who's home and get a message when they arrive or leave.</p>`;
  }
  const list = [...p.devices].sort((a, b) => (b.home - a.home) || (a.name || '').localeCompare(b.name || ''));
  return `<div class="status-list">${list.map((d) => `
    <div class="status-row"><i class="${d.home ? 'ok' : 'off'}"></i><b class="truncate">${esc(d.name || d.mac)}</b>
      <small>${d.home ? 'Home' : 'Away'}${isSet(d.since) ? ` since ${new Date(d.since).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}` : ''}</small></div>`).join('')}</div>`;
}

const SEV = { high: ['bad', 'shield-alert'], medium: ['warn', 'triangle-alert'], low: ['off', 'info'] };

export function scanCard(s, admin) {
  if (!s || !s.available) return `<p class="t-3 t-sm" style="padding:16px 14px">Not available on this system.</p>`;
  const findings = s.devices.flatMap((d) => d.findings.map((f) => ({ ...f, device: d.name || d.ip || d.mac, mac: d.mac })));
  const serious = findings.filter((f) => f.severity !== 'low');
  const when = isSet(s.time) ? `${s.checked} device${s.checked === 1 ? '' : 's'} checked ${ago(s.time).toLowerCase()}` : 'Not checked yet';
  const head = s.running ? `<div class="scan-sum"><span class="spin">${icon('refresh-cw', 'icon-sm')}</span>Checking your devices…</div>`
    : !isSet(s.time) ? `<div class="scan-sum">${icon('scan-search', 'icon-sm')}The first check runs a few minutes after Fengard starts.</div>`
    : serious.length ? `<div class="scan-sum warn">${icon('shield-alert', 'icon-sm')}${serious.length} thing${serious.length > 1 ? 's' : ''} worth fixing</div>`
    : `<div class="scan-sum ok">${icon('shield-check', 'icon-sm')}All clear. No risky settings found.</div>`;
  return `<div class="panel-body scan">
      ${head}
      ${serious.length ? `<div class="findings">${serious.map((f) => `
        <details class="finding"><summary><i class="${SEV[f.severity][0]}"></i><b>${esc(f.title)}</b><small class="truncate">${esc(f.device)}</small></summary>
          <p>${esc(f.detail)}</p></details>`).join('')}</div>` : ''}
      <div class="scan-foot"><small class="t-3">${esc(when)} · checks itself every week</small>
        ${admin ? `<button class="btn btn-sm" data-scan ${s.running ? 'disabled' : ''}>${icon('scan-search', 'icon-sm')}Check now</button>` : ''}</div>
    </div>`;
}

export function findingsFor(s, mac) {
  return (s?.devices || []).find((d) => d.mac === mac)?.findings || [];
}

