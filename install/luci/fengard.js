'use strict';
'require view';
'require fs';
'require ui';
'require poll';

// services > fengard, the dashboard does the real work this just says if its up and links to it

function call(cmd) {
	return fs.exec_direct('/usr/libexec/fengard-luci', [ cmd ], 'json').catch(function() { return {}; });
}

var css = '\
.fg { --fg-accent: #6a4ff0; --fg-soft: rgba(106, 79, 240, .1); --fg-line: rgba(127, 127, 127, .22); --fg-muted: #777; max-width: 720px; }\
:root[data-darkmode="true"] .fg { --fg-accent: #8f78ff; --fg-soft: rgba(143, 120, 255, .14); --fg-muted: #9a9a9a; }\
@media (prefers-color-scheme: dark) { :root:not([data-darkmode="false"]) .fg { --fg-accent: #8f78ff; --fg-soft: rgba(143, 120, 255, .14); --fg-muted: #9a9a9a; } }\
.fg-head { display: flex; align-items: center; gap: 14px; margin: 4px 0 20px; }\
.fg-logo { width: 44px; height: 44px; flex: none; border-radius: 11px; background: var(--fg-soft); color: var(--fg-accent); display: grid; place-items: center; }\
.fg-logo svg { width: 30px; height: 30px; }\
.fg-head h2 { margin: 0; padding: 0; border: 0; font-size: 1.4em; line-height: 1.2; }\
.fg-sub { color: var(--fg-muted); font-size: .92em; }\
.fg-card { border: 1px solid var(--fg-line); border-radius: 10px; padding: 18px 20px; }\
.fg-state { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: 1.05em; margin-bottom: 14px; }\
.fg-dot { width: 9px; height: 9px; border-radius: 50%; background: var(--fg-muted); flex: none; }\
.fg-on .fg-dot { background: var(--fg-accent); box-shadow: 0 0 0 4px var(--fg-soft); }\
.fg-rows { display: grid; grid-template-columns: max-content 1fr; gap: 8px 22px; margin: 0 0 18px; }\
.fg-rows dt { color: var(--fg-muted); }\
.fg-rows dd { margin: 0; word-break: break-word; }\
.fg-rows a { color: var(--fg-accent); }\
.fg-actions { display: flex; flex-wrap: wrap; gap: 8px; }\
.fg-actions .fg-primary, .fg-actions .fg-primary:hover { background: var(--fg-accent); border-color: var(--fg-accent); color: #fff; text-decoration: none; }\
.fg-log { margin: 14px 0 0; padding: 10px 12px; border-radius: 8px; background: rgba(127, 127, 127, .1); font-size: .85em; white-space: pre-wrap; max-height: 260px; overflow: auto; }\
.fg-foot { color: var(--fg-muted); font-size: .92em; margin-top: 14px; }\
';

return view.extend({
	load: function() {
		var logo = fetch(L.resource('fengard/logo.svg')).then(function(r) { return r.ok ? r.text() : ''; }).catch(function() { return ''; });
		return Promise.all([ call('status'), logo ]);
	},

	act: function(cmd, btn) {
		btn.disabled = true;
		return fs.exec_direct('/usr/libexec/fengard-luci', [ cmd ]).catch(function(e) {
			ui.addNotification(null, E('p', _('Could not %s Fengard: %s').format(cmd, e.message)));
		}).then(L.bind(this.refresh, this));
	},

	stateText: function(s) {
		if (s.installing)
			return _('Setting up, this takes about a minute');
		if (!s.installed)
			return _("Setup didn't finish");
		if (!s.running && s.stopped)
			return _('Stopped, dnsmasq is answering DNS for now');
		if (s.standin)
			return _('Not answering DNS, plain dnsmasq is standing in until it recovers');
		if (s.running && s.answering)
			return _('Filtering DNS for the network');
		if (s.running)
			return _('Starting up');
		return _('Stopped, dnsmasq is answering DNS for now');
	},

	body: function(s) {
		var on = s.installed && s.running && s.answering && !s.installing;
		var url = s.ip ? 'http://' + s.ip + '/' : null;
		var rows = [], actions = [];

		if (s.installed && url) {
			rows.push(E('dt', _('Dashboard')), E('dd', [ E('a', { href: url, target: '_blank', rel: 'noopener' }, url), ' ', _('or'), ' ',
				E('a', { href: 'http://fengard.lan/', target: '_blank', rel: 'noopener' }, 'fengard.lan') ]));
			rows.push(E('dt', _('At boot')), E('dd', s.enabled ? _('Starts by itself') : _('Stays off until started here')));
		}

		if (s.installed && !s.installing) {
			if (url)
				actions.push(E('a', { 'class': 'btn cbi-button fg-primary', href: url, target: '_blank', rel: 'noopener' }, _('Open dashboard')));
			if (s.running) {
				actions.push(E('button', { 'class': 'btn cbi-button', click: ui.createHandlerFn(this, 'act', 'restart') }, _('Restart')));
				actions.push(E('button', { 'class': 'btn cbi-button', click: ui.createHandlerFn(this, 'act', 'stop') }, _('Stop')));
			} else {
				actions.push(E('button', { 'class': 'btn cbi-button', click: ui.createHandlerFn(this, 'act', 'start') }, _('Start')));
			}
		}

		var card = [
			E('div', { 'class': 'fg-state' + (on ? ' fg-on' : '') }, [ E('span', { 'class': 'fg-dot' }), this.stateText(s) ])
		];
		if (rows.length)
			card.push(E('dl', { 'class': 'fg-rows' }, rows));
		if (actions.length)
			card.push(E('div', { 'class': 'fg-actions' }, actions));
		if ((s.installing || !s.installed) && s.log) {
			card.push(E('pre', { 'class': 'fg-log' }, s.log));
			if (!s.installing)
				card.push(E('p', { 'class': 'fg-foot' }, _('Reinstalling the package tries again. Running sh /usr/share/fengard/router-install.sh over SSH shows the whole log.')));
		}
		return E('div', { 'class': 'fg-card' }, card);
	},

	refresh: function() {
		var self = this;
		return call('status').then(function(s) {
			var old = document.querySelector('.fg-card');
			if (old)
				old.replaceWith(self.body(s));
		});
	},

	render: function(data) {
		var s = data[0], logo = E('span', { 'class': 'fg-logo' });
		logo.innerHTML = data[1];

		poll.add(L.bind(this.refresh, this), 5);

		return E('div', { 'class': 'fg' }, [
			E('style', {}, css),
			E('div', { 'class': 'fg-head' }, [
				logo,
				E('div', {}, [
					E('h2', {}, 'Fengard'),
					E('div', { 'class': 'fg-sub' }, [ _('DNS filtering and firewall for the whole network'), s.version ? ' · ' + s.version : '' ])
				])
			]),
			this.body(s),
			E('p', { 'class': 'fg-foot' }, _('Blocking, devices, schedules and the firewall are all set up in the dashboard.'))
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
