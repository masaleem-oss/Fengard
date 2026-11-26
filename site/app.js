// fengard website, plain js with no build step like the dashboard
(() => {
  'use strict';
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  const icon = (n) => `<svg class="ico"><use href="assets/icons.svg#i-${n}"/></svg>`;
  const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
  };
  const REPO = 'https://github.com/masaleem-oss/Fengard';

  // theme
  const isLight = () => {
    const t = document.documentElement.dataset.theme;
    return t ? t === 'light' : matchMedia('(prefers-color-scheme: light)').matches;
  };
  $('#theme').addEventListener('click', () => {
    const next = isLight() ? 'dark' : 'light';
    document.documentElement.dataset.theme = next;
    store.set('fg-site-theme', next);
    tourShow(tourCur);
  });

  // nav border once the page moves
  const nav = $('.nav');
  const onScroll = () => nav.classList.toggle('scrolled', scrollY > 8);
  addEventListener('scroll', onScroll, { passive: true });
  onScroll();

  // latest release, so the buttons always point at the real current download
  const setVersion = (v) => {
    if (!/^[0-9][0-9A-Za-z.-]{0,20}$/.test(v)) return;
    const zip = `${REPO}/releases/download/v${v}/fengard-${v}.zip`;
    for (const [a, l] of [['#dl', '#dl-label'], ['#dl2', '#dl2-label']]) {
      $(a).href = zip;
      $(l).textContent = `Download ${v}`;
    }
    $('#ver-line').textContent = `Fengard ${v} · Open source · Apache 2.0`;
    $('#zip-name').textContent = `fengard-${v}.zip`;
    $('#foot-ver').textContent = v;
    version = v;
    instShow(instCur);
  };
  let version = '';

  // ---------- try a lookup ----------
  // a small copy of what fengard's policy engine checks, in the same order
  const CATS = {
    ads: 'Ads & trackers', malware: 'Malware & phishing', adult: 'Adult content', gambling: 'Gambling', social: 'Social media',
    gaming: 'Gaming', streaming: 'Video streaming', ai: 'AI chatbots', bypass: 'VPN, proxy & DNS bypass', tor: 'Tor',
  };
  const DOMAINS = {
    'tiktok.com': { cat: 'social', app: 'TikTok' }, 'instagram.com': { cat: 'social', app: 'Instagram' }, 'snapchat.com': { cat: 'social', app: 'Snapchat' },
    'doubleclick.net': { cat: 'ads' }, 'googlesyndication.com': { cat: 'ads' }, 'app-measurement.com': { cat: 'ads' }, 'device-metrics-us.amazon.com': { cat: 'ads' },
    'roblox.com': { cat: 'gaming', app: 'Roblox' }, 'fortnite.com': { cat: 'gaming', app: 'Fortnite' }, 'discord.com': { cat: 'social', app: 'Discord' },
    'secure-login-paypa1.com': { cat: 'malware' }, 'chatgpt.com': { cat: 'ai', app: 'ChatGPT' }, 'character.ai': { cat: 'ai', app: 'Character.AI' },
    'nordvpn.com': { cat: 'bypass' }, 'torproject.org': { cat: 'tor' }, 'netflix.com': { cat: 'streaming', app: 'Netflix' },
    'youtube.com': { cat: 'streaming', app: 'YouTube', safe: 'restrict.youtube.com' }, 'google.com': { safe: 'forcesafesearch.google.com' },
    'bing.com': { safe: 'strict.bing.com' },
  };
  const PROFILES = {
    everyone: { name: 'Everyone', device: 'Work laptop', cats: ['malware', 'ads'] },
    kids: { name: 'Kids', device: "Leo's phone", cats: ['malware', 'ads', 'adult', 'gambling', 'social', 'bypass', 'tor'], safe: true, used: { Roblox: '1 h a day' } },
    iot: { name: 'Smart home', device: 'Kitchen Echo', cats: ['malware', 'ads', 'social', 'gaming', 'streaming', 'ai'] },
  };
  const VIA = {
    router: { to: '192.168.8.1:53', what: 'the router' },
    google: { to: '8.8.8.8:53', what: '8.8.8.8' },
    doh: { to: 'dns.google:443', what: 'DNS-over-HTTPS' },
  };
  let profile = 'kids';

  const lookupOf = (raw) => {
    const d = raw.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/[/:?#].*$/, '').replace(/\.$/, '');
    for (let p = d; p; p = p.includes('.') ? p.slice(p.indexOf('.') + 1) : '') {
      if (DOMAINS[p]) return { domain: d, ...DOMAINS[p], matched: p };
    }
    return { domain: d };
  };

  const decide = (prof, l) => {
    const order = ['approved', 'paused', 'screen time', 'app limit', 'schedule', 'allow rules', 'block rules', 'categories', 'apps', 'SafeSearch'];
    const checks = (hit) => order.map((c) => ({ c, hit: c === hit }));
    if (l.app && prof.used && prof.used[l.app]) {
      return { kind: 'blocked', why: `${l.app} time is used up for today`, sub: `limit ${prof.used[l.app]}`, at: 'app limit', checks: checks('app limit'), page: true };
    }
    if (l.cat && prof.cats.includes(l.cat)) {
      const page = l.cat !== 'ads';
      return { kind: 'blocked', why: CATS[l.cat], sub: l.matched === l.domain ? l.domain : `${l.domain} is part of ${l.matched}`, at: 'categories', checks: checks('categories'), page };
    }
    if (prof.safe && l.safe) {
      return { kind: 'safe', why: 'SafeSearch enforced', sub: `CNAME ${l.safe}`, at: 'SafeSearch', checks: checks('SafeSearch') };
    }
    return { kind: 'allowed', why: 'Allowed', sub: '', at: '', checks: checks('') };
  };

  const hash = (s) => [...s].reduce((h, c) => (h * 31 + c.charCodeAt(0)) >>> 0, 7);

  const run = () => {
    const prof = PROFILES[profile];
    const l = lookupOf($('#trace-domain').value);
    const via = VIA[$('#trace-via').value];
    const steps = $('#trace-steps');
    const verdict = $('#trace-verdict');
    verdict.hidden = true;
    if (!l.domain || !/^[a-z0-9.-]+\.[a-z]{2,}$/.test(l.domain)) {
      steps.innerHTML = `<li class="step on warn"><span class="pip">${icon('info')}</span><div><b>That doesn't look like a domain</b><p>Try something like example.com</p></div><span class="us"></span></li>`;
      return;
    }
    const d = decide(prof, l);
    const cached = hash(l.domain) % 3 === 0;
    const policyUs = 9 + (hash(l.domain + profile) % 14);
    const s = [];
    s.push({ cls: 'on', ic: 'smartphone', t: `${prof.device} asks ${via.what}`, p: `${l.domain} A?  →  ${via.to}`, us: '' });
    if (via === VIA.doh) {
      s.push({ cls: 'stop', ic: 'brick-wall', t: 'Kernel refuses DNS-over-HTTPS', p: 'dns.google is in @doh4, port 443 rejected. The browser falls back to the router.', us: '0 µs' });
    } else if (via === VIA.google) {
      s.push({ cls: 'warn', ic: 'brick-wall', t: 'Kernel redirects it to Fengard', p: 'dnat 8.8.8.8:53 → 192.168.8.1:53, the phone never notices', us: '0 µs' });
    } else {
      s.push({ cls: 'ok', ic: 'brick-wall', t: 'Arrives at Fengard', p: 'port 53, under the per-device rate limit', us: '0 µs' });
    }
    const at = d.checks.findIndex((x) => x.hit);
    const chk = d.checks.map((c, i) => `<span class="${c.hit ? 'hit' : at < 0 || i < at ? 'pass' : ''}">${esc(c.c)}</span>`).join('');
    s.push({
      cls: d.kind === 'blocked' ? 'stop' : 'ok', ic: 'shield', t: `${prof.name} profile: ${d.kind === 'blocked' ? `blocked at ${d.at}` : d.kind === 'safe' ? 'SafeSearch' : 'nothing matches'}`,
      p: `<span class="checks">${chk}</span>`, us: `${policyUs} µs`, raw: true,
    });
    let answer;
    if (d.kind === 'blocked') {
      answer = d.page
        ? { cls: 'stop', ic: 'ban', t: 'Answered with the block page', p: '192.168.8.2, with the reason and a Request access button', us: '' }
        : { cls: 'stop', ic: 'ban', t: 'Answered 0.0.0.0', p: 'nothing connects, so apps never ask for Local Network access', us: '' };
    } else if (d.kind === 'safe') {
      answer = { cls: 'ok', ic: 'search', t: 'Answered with the safe version', p: `${l.domain} CNAME ${l.matched === 'youtube.com' ? 'restrict.youtube.com' : l.safe}`, us: '' };
    } else if (cached) {
      answer = { cls: 'ok', ic: 'database', t: 'Answered from the cache', p: `${l.domain} A 93.184.${hash(l.domain) % 200}.${hash(l.domain + 'x') % 250 + 1}`, us: '0.3 ms' };
    } else {
      answer = { cls: 'ok', ic: 'globe-lock', t: 'Asked upstream over HTTP/3', p: 'cloudflare-dns.com, then cached for everyone', us: `${12 + (hash(l.domain) % 19)} ms` };
    }
    s.push(answer);

    steps.innerHTML = s.map((x) => `<li class="step ${still ? x.cls : ''}" data-cls="${x.cls}"><span class="pip">${icon(x.ic)}</span>
      <div><b>${esc(x.t)}</b>${x.raw ? x.p : `<p>${esc(x.p)}</p>`}</div><span class="us">${esc(x.us)}</span></li>`).join('');

    const show = () => {
      verdict.className = `verdict ${d.kind === 'blocked' ? 'blocked' : 'allowed'}`;
      verdict.innerHTML = d.kind === 'blocked'
        ? `${icon('shield-ban')}<b>Blocked: ${esc(d.why)}</b><span>${esc(d.sub)} · logged for ${esc(prof.device)}</span>`
        : d.kind === 'safe'
          ? `${icon('shield-check')}<b>${esc(d.why)}</b><span>${esc(d.sub)}</span>`
          : `${icon('circle-check')}<b>Allowed</b><span>${esc(l.domain)} isn't in anything ${esc(prof.name)} blocks</span>`;
      verdict.hidden = false;
    };
    if (still) { show(); return; }
    const items = $$('.step', steps);
    items.forEach((li, i) => setTimeout(() => {
      li.classList.add('on', li.dataset.cls);
      if (i === items.length - 1) setTimeout(show, 260);
    }, 120 + i * 380));
  };

  $$('#trace-profile button').forEach((b) => b.addEventListener('click', () => {
    profile = b.dataset.p;
    $$('#trace-profile button').forEach((x) => { x.classList.toggle('on', x === b); x.setAttribute('aria-checked', String(x === b)); });
    $('#trace-dev').textContent = PROFILES[profile].device;
    run();
  }));
  $$('#trace-hint button').forEach((b) => b.addEventListener('click', () => { $('#trace-domain').value = b.dataset.d; run(); }));
  $('#trace-via').addEventListener('change', run);
  $('#trace-form').addEventListener('submit', (e) => { e.preventDefault(); run(); });
  run();

  // ---------- ways around a filter, with the real rules fengard writes ----------
  const BYPASS = {
    dns: {
      say: 'A phone set to 8.8.8.8, or an app with its own DNS server, still gets Fengard. The router rewrites the destination before the lookup leaves the house.',
      code: '<span class="c"># nftables, table inet fengard, chain dstnat</span>\niifname { "br-lan" } meta nfproto ipv4 \\\n  meta l4proto { tcp, udp } th dport 53 \\\n  ip daddr != 192.168.8.1 <span class="k">dnat ip to 192.168.8.1:53</span>',
      note: 'Same on the iptables backend for OpenWrt 21 and GL.iNet 4.x. Fengard picks whichever the router uses.',
    },
    doh: {
      say: "Browsers and apps that bring their own encrypted DNS can't reach it. A daily list of DoH server addresses goes into a firewall set, and port 443 to those addresses is refused, over TCP and QUIC.",
      code: '<span class="c"># chain forward</span>\niifname { "br-lan" } oifname { "wan" } \\\n  ip daddr @doh4 meta l4proto { tcp, udp } th dport 443 \\\n  counter <span class="k">reject with icmpx admin-prohibited</span>',
      note: 'Rejected, not dropped, so the browser gives up straight away and falls back to the router instead of hanging.',
    },
    dot: {
      say: "DNS-over-TLS and DNS-over-QUIC use port 853, which nothing in a home needs. It's closed on the way out.",
      code: '<span class="c"># chain forward</span>\niifname { "br-lan" } oifname { "wan" } \\\n  meta l4proto { tcp, udp } th dport 853 \\\n  counter <span class="k">reject with icmpx admin-prohibited</span>',
      note: "The router's own upstream lookups aren't affected, they don't pass through the forward chain.",
    },
    tor: {
      say: 'Tor is blocked by name, so Tor Browser and its bridges fail to start, and by address: the relay list refreshes daily into a firewall set, applied only to the profiles that block Tor.',
      code: '<span class="c"># chain forward</span>\niifname { "br-lan" } oifname { "wan" } ether saddr @tor_devs \\\n  ip daddr @tor4 counter <span class="k">reject with icmpx admin-prohibited</span>',
      note: 'Devices are matched by MAC address on the LAN, and by tunnel address for phones on the VPN.',
    },
    canary: {
      say: "Firefox checks a special name before turning its own DoH on, and Apple devices do the same before iCloud Private Relay. Fengard answers both with \"doesn't exist\", so they quietly use the router.",
      code: '$ dig use-application-dns.net\n<span class="k">;; status: NXDOMAIN</span>\n\n$ dig mask.icloud.com\n<span class="k">;; status: NXDOMAIN</span>',
      note: 'Turned off together with the rest under DNS → Stop filter bypass, if you ever need them back.',
    },
    flood: {
      say: 'One device hammering DNS is cut off in the kernel before its packets fill a socket. In the lab a single device sending 1.4 million lookups a second left everyone else resolving with no failures.',
      code: '<span class="c"># chain input, 2× the per-device limit set on the Settings page</span>\niifname { "br-lan" } meta nfproto ipv4 udp dport 53 \\\n  update @dns4 { ip saddr limit rate over 200/second burst 800 packets } <span class="k">counter drop</span>',
      note: 'Normal devices make under 20 lookups a second.',
    },
  };
  const bypassShow = (k) => {
    const b = BYPASS[k];
    $('#bypass-say').textContent = b.say;
    $('#bypass-code').innerHTML = b.code;
    $('#bypass-note').textContent = b.note;
    $$('.bypass-list button').forEach((x) => { x.classList.toggle('on', x.dataset.b === k); x.setAttribute('aria-selected', String(x.dataset.b === k)); });
  };
  $$('.bypass-list button').forEach((b) => b.addEventListener('click', () => bypassShow(b.dataset.b)));
  bypassShow('dns');

  // ---------- dashboard tour ----------
  const TOUR = {
    dashboard: ['Lookups, what got blocked and why, devices online and response time. Cards can be moved, resized or removed.', 'Fengard overview: lookups, blocked share, devices online and the DNS traffic chart'],
    devices: ['Every device by name and maker, with its profile. Pause one, block it, or wake it up.', 'The Devices page listing each device with its profile and status'],
    profiles: ['Categories, single apps, SafeSearch, schedules, screen time and their own DNS servers, per profile.', 'A profile with blocked categories and apps'],
    activity: ['Every lookup, live or searched, and live speed and data per device.', 'The Activity page with recent lookups'],
    filtering: ['Built-in categories and custom lists, with how many domains each holds and what it costs in memory.', 'The Filtering page with blocklists'],
    'check-site': ['What every profile would do with a domain, and why. Also from the command palette.', 'Check a site, showing the decision for each profile'],
  };
  let tourCur = 'dashboard';
  const tourShow = (k) => {
    tourCur = k;
    $$('#tour-tabs button').forEach((x) => { x.classList.toggle('on', x.dataset.s === k); x.setAttribute('aria-selected', String(x.dataset.s === k)); });
    const phones = k === 'phones';
    $('#tour-phones').hidden = !phones;
    $('.shot-frame').hidden = phones;
    if (phones) {
      $('#tour-cap').textContent = 'The block page with Request access, and My time, where kids see what is left today and ask for more.';
      return;
    }
    const img = $('#tour-img');
    const src = `assets/shots/${k === 'dashboard' && isLight() ? 'dashboard-light' : k}.png`;
    $('#tour-cap').textContent = TOUR[k][0];
    if (img.getAttribute('src') === src) return;
    img.classList.add('fade');
    const next = new Image();
    next.onload = () => { img.src = src; img.alt = TOUR[k][1]; img.classList.remove('fade'); };
    next.src = src;
  };
  $$('#tour-tabs button').forEach((b) => b.addEventListener('click', () => tourShow(b.dataset.s)));
  tourShow('dashboard');

  // ---------- will it run on mine ----------
  const ROUTERS = [
    { n: 'GL.iNet GL-MT3000 (Beryl AX)', k: 'beryl ax mt3000 filogic mt7981', cpu: 'arm64', fw: 'GL.iNet 4.7, OpenWrt 21.02', mem: '33 MB', s: 'hw', note: 'In daily use since 1.0.0, updated in place every release.' },
    { n: 'GL.iNet GL-MT6000 (Flint 2)', k: 'flint 2 mt6000 filogic mt7986', cpu: 'arm64', fw: 'GL.iNet 4.x', mem: '~26 MB', s: 'user', note: 'Runs for users, including its WireGuard and OpenVPN servers from 1.3.4.' },
    { n: 'GL.iNet GL-MT3600BE (Beryl 7)', k: 'beryl 7 mt3600be wifi 7', cpu: 'arm64', fw: 'GL.iNet 4.9', mem: '~25 MB', s: 'user', note: 'Its firmware has an nft that can\'t read rules, so Fengard uses iptables there by itself.' },
    { n: 'Linksys E8450', k: 'e8450 belkin rt3200 mt7622', cpu: 'arm64', fw: 'OpenWrt 22.03 to 25.12', mem: '25 MB', s: 'lab', note: 'ARM64 lab router, every release.' },
    { n: 'GL.iNet GL-B1300 (Convexa-B)', k: 'b1300 convexa ipq40xx', cpu: 'arm', fw: 'OpenWrt 21.02 to 24.10', mem: '22 MB', s: 'lab', note: 'ARMv7 lab router, every release.' },
    { n: 'Linksys WRT1900AC, WRT3200ACM', k: 'linksys wrt mvebu wrt32x wrt1200ac', cpu: 'arm', fw: 'OpenWrt 21.02 to 24.10', mem: '24 MB', s: 'lab', note: 'ARMv7 lab router, every release.' },
    { n: 'TP-Link Archer C7', k: 'archer c7 tp-link tplink ath79 qca9558', cpu: 'mips', fw: 'OpenWrt 22.03', mem: '25 MB', s: 'lab', note: 'MIPS big-endian lab router. 128 MB of RAM is enough for full blocklists.' },
    { n: 'GL.iNet GL-AR750S (Slate)', k: 'ar750s slate ath79', cpu: 'mips', fw: 'OpenWrt 22.03', mem: '25 MB', s: 'lab', note: 'MIPS big-endian lab router.' },
    { n: 'Xiaomi Mi Router 4A Gigabit', k: 'xiaomi 4a mi router mt7621', cpu: 'mipsle', fw: 'OpenWrt 24.10', mem: '26 MB', s: 'lab', note: 'MT7621, MIPS little-endian lab router.' },
    { n: 'Netgear R6220', k: 'netgear r6220 mt7621', cpu: 'mipsle', fw: 'OpenWrt 24.10', mem: '26 MB', s: 'lab', note: 'MT7621, MIPS little-endian lab router.' },
    { n: 'GL.iNet GL-MT1300 (Beryl)', k: 'mt1300 beryl mt7621', cpu: 'mipsle', fw: 'OpenWrt 24.10', mem: '26 MB', s: 'lab', note: 'MT7621, MIPS little-endian lab router.' },
    { n: 'Ubiquiti EdgeRouter Lite', k: 'edgerouter lite erlite ubiquiti octeon', cpu: 'mips64', fw: 'OpenWrt 24.10', mem: '27 MB', s: 'lab', note: 'Octeon, MIPS64 lab router.' },
    { n: 'x86-64 mini PC', k: 'x86 x86_64 amd64 mini pc protectli n100 intel', cpu: 'amd64', fw: 'OpenWrt 19.07 to 24.10', mem: '26 to 30 MB', s: 'lab', note: 'Rebooted in the lab too, ext4 and squashfs images.' },
    { n: 'x86 32-bit', k: 'x86 i386 386 32-bit', cpu: '386', fw: 'OpenWrt 23.05', mem: '23 MB', s: 'lab', note: 'Old PCs and thin clients.' },
  ];
  const BADGE = { hw: ['On real hardware', 'hw'], lab: ['Lab tested', 'lab'], user: ['Users report', 'user'] };
  const rtShow = () => {
    const q = $('#rt-q').value.trim().toLowerCase();
    const terms = q.split(/\s+/).filter(Boolean);
    const hits = ROUTERS.filter((r) => terms.every((t) => (r.n + ' ' + r.k + ' ' + r.cpu).toLowerCase().includes(t)));
    const list = q ? hits : ROUTERS.slice(0, 6);
    $('#rt-results').innerHTML = list.map((r) => `<article class="rt-card"><h3>${esc(r.n)}<span class="badge ${BADGE[r.s][1]}">${BADGE[r.s][0]}</span></h3>
      <div class="meta"><span>linux-${esc(r.cpu)}</span><span>${esc(r.fw)}</span><span>${esc(r.mem)}</span></div><p>${esc(r.note)}</p></article>`).join('')
      + (q && !hits.length ? `<div class="rt-empty"><b>Not in our list yet.</b> If it runs OpenWrt 19.07 or newer and has 128 MB of memory, it almost certainly works: the installer checks first and stops cleanly if it doesn't fit. <a href="${REPO}/issues/new/choose">Tell us how it goes</a> and it'll be added.</div>` : '')
      + (!q ? `<div class="rt-empty">${ROUTERS.length - 6} more, plus any router on OpenWrt 19.07 or newer. Search by model, chip like MT7621, or CPU like mipsle.</div>` : '');
  };
  $('#rt-q').addEventListener('input', rtShow);
  rtShow();

  // ---------- install ----------
  const INST = {
    win: ['Unzip it and double-click Install Fengard.cmd. If Windows warns about a downloaded file, choose More info, then Run anyway.', () => 'Install Fengard.cmd'],
    nix: ['Unzip it, open a terminal in the folder and run:', () => 'sh install.sh'],
    ssh: ['No computer needed. A router with internet installs itself from the latest official release:', () => `wget -O- ${REPO}/releases/latest/download/router-install.sh | sh`],
    pkg: ['On OpenWrt up to 24.10 and GL.iNet, upload the .ipk in LuCI under System → Software. On 25.12 and later, copy the .apk over and run:', () => `apk add --allow-untrusted /tmp/fengard_${version || '<version>'}_<cpu>.apk`],
  };
  let instCur = 'win';
  function instShow(k) {
    instCur = k;
    $$('#inst-tabs button').forEach((x) => { x.classList.toggle('on', x.dataset.i === k); x.setAttribute('aria-selected', String(x.dataset.i === k)); });
    $('#inst-say').textContent = INST[k][0];
    $('#inst-code').textContent = INST[k][1]();
    $('#inst-copy').classList.remove('done');
  }
  $$('#inst-tabs button').forEach((b) => b.addEventListener('click', () => instShow(b.dataset.i)));
  $('#inst-copy').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    try {
      await navigator.clipboard.writeText($('#inst-code').textContent);
      btn.classList.add('done');
      btn.innerHTML = icon('check');
      setTimeout(() => { btn.classList.remove('done'); btn.innerHTML = icon('copy'); }, 1600);
    } catch { /* clipboard blocked, the text is still selectable */ }
  });
  instShow('win');

  // ---------- numbers count up, memory bars fill, sections ease in ----------
  const countUp = (el) => {
    const to = parseFloat(el.dataset.count), dec = Number(el.dataset.dec || 0), suf = el.dataset.suffix || '';
    if (still) return;
    const t0 = performance.now(), dur = 1100;
    const tick = (t) => {
      const p = Math.min(1, (t - t0) / dur), e = 1 - Math.pow(1 - p, 3);
      el.textContent = (to * e).toFixed(dec) + suf;
      if (p < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  };
  if ('IntersectionObserver' in window) {
    const io = new IntersectionObserver((entries) => entries.forEach((en) => {
      if (!en.isIntersecting) return;
      const el = en.target;
      el.classList.add('in');
      if (el.dataset.count) countUp(el);
      io.unobserve(el);
    }), { threshold: 0.25 });
    $$('[data-count]').forEach((el) => io.observe(el));
    $('#mem') && io.observe($('#mem'));
    if (!still) {
      $$('.sec-head, .flow, .bypass, .feat-grid, .table-wrap, .rt, .tour, .faq, .off-list').forEach((el) => { el.classList.add('reveal'); io.observe(el); });
    }
  } else {
    $('#mem')?.classList.add('in');
  }
  // last, once everything it updates exists
  (async () => {
    let v = null;
    try { v = sessionStorage.getItem('fg-site-ver'); } catch { /* fine */ }
    if (!v) {
      try {
        const r = await fetch('https://api.github.com/repos/masaleem-oss/Fengard/releases/latest', { headers: { Accept: 'application/vnd.github+json' } });
        if (r.ok) v = String((await r.json()).tag_name || '').replace(/^v/, '');
        try { if (v) sessionStorage.setItem('fg-site-ver', v); } catch { /* fine */ }
      } catch { /* offline or rate limited, the links still go to the releases page */ }
    }
    if (v) setVersion(v);
  })();

})();
