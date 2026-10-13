import { get } from '/js/api.js';
import { esc, isSet } from '/js/ui.js';

function card(p) {
  if (!p || !p.devices.length) {
    return `<p class="t-3 t-sm" style="padding:16px 14px">Open a phone in <a href="#devices">Devices</a> and turn on <b>Arrival alerts</b> to see who's home and get a message when they arrive or leave.</p>`;
  }
  const list = [...p.devices].sort((a, b) => (b.home - a.home) || (a.name || '').localeCompare(b.name || ''));
  return `<div class="status-list">${list.map((d) => `
    <div class="status-row"><i class="${d.home ? 'ok' : 'off'}"></i><b class="truncate">${esc(d.name || d.mac)}</b>
      <small>${d.home ? 'Home' : 'Away'}${isSet(d.since) ? ` since ${new Date(d.since).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}` : ''}</small></div>`).join('')}</div>`;
}

export default {
  id: 'presence',
  title: "Who's home",
  size: 'sm',
  render(w) {
    const draw = async () => {
      const p = await get('/api/presence').catch(() => null);
      if (w.alive()) w.body.innerHTML = card(p);
    };
    draw();
    w.every(5000, draw);
  },
};
