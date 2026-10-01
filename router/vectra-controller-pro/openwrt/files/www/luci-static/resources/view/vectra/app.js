'use strict';
'require view';
'require rpc';

/*
 * Vectra Controller Pro — router UI host view.
 *
 * The UI is one self-contained bundle, /luci-static/vectra/vectra-app.js,
 * built from router/vectra-controller-pro/ui/app. This view only loads it,
 * hands it a transport to the `vectra` ubus object and LuCI's own password
 * change, and stays out of the way: the app renders into its own shadow root,
 * speaks ru/en/zh itself and polls on its own.
 */

var APP_SRC = '/luci-static/vectra/vectra-app.js';
var APP_VERSION = '0.1.0-91bc22d5ad'; // stamped by `npm run build` in ui/app — do not edit by hand

var declared = {};

/*
 * call(method, params) -> Promise<object>.
 *
 * `reject: true` matters: without it LuCI resolves a failed ubus call (for
 * example "Object not found" when the rpcd plugin is missing) to the
 * `expect` default `{}`, and the app would render an empty router instead of
 * the error. With it, every ubus or JSON-RPC failure ("Object not found",
 * "Access denied", timeouts) reaches the app as a rejection.
 *
 * `nobatch: true` sends each call at once. Batched calls wait in LuCI's queue
 * for the next requestAnimationFrame, which browsers pause in background tabs,
 * so a page opened in the background would sit on its first load.
 */
function callVectra(method, params) {
	var args = (params && typeof params === 'object') ? params : {};
	var keys = Object.keys(args);
	var sig = method + '(' + keys.join(',') + ')';

	return Promise.resolve().then(function() {
		if (!declared[sig])
			declared[sig] = rpc.declare({
				object: 'vectra',
				method: method,
				params: keys,
				expect: { '': {} },
				reject: true,
				nobatch: true
			});

		return declared[sig].apply(null, keys.map(function(k) { return args[k]; }));
	});
}

var callSetPassword = null;

/*
 * setPassword(password) -> Promise<boolean>.
 *
 * The router's password is LuCI's: this is LuCI's own change for root
 * (`luci.setPassword`, luci-base's rpcd plugin — the call System →
 * Administration makes), granted by this package's ACL; vctl never sees a
 * password. True only when LuCI says the password was taken; a ubus or
 * JSON-RPC failure rejects, as a call to vectra does (`reject`, `nobatch`:
 * see above).
 */
function setPassword(password) {
	return Promise.resolve().then(function() {
		if (!callSetPassword)
			callSetPassword = rpc.declare({
				object: 'luci',
				method: 'setPassword',
				params: [ 'username', 'password' ],
				expect: { result: false },
				reject: true,
				nobatch: true
			});

		return callSetPassword('root', String(password));
	}).then(function(ok) {
		return ok === true;
	});
}

function loadApp() {
	if (window.VectraApp && typeof window.VectraApp.mount === 'function')
		return Promise.resolve(window.VectraApp);

	return new Promise(function(resolve, reject) {
		var script = document.createElement('script');

		script.src = APP_SRC + '?v=' + encodeURIComponent(APP_VERSION);
		script.async = true;
		script.onload = function() {
			if (window.VectraApp && typeof window.VectraApp.mount === 'function')
				resolve(window.VectraApp);
			else
				reject(new Error(APP_SRC + ' loaded but did not define window.VectraApp'));
		};
		script.onerror = function() {
			reject(new Error('Could not load ' + APP_SRC));
		};

		document.head.appendChild(script);
	});
}

function loadFailure(err) {
	var lang = String(document.documentElement.lang || '').toLowerCase();
	var title = lang.indexOf('ru') === 0 ? 'Интерфейс Vectra не загрузился'
		: lang.indexOf('zh') === 0 ? 'Vectra 界面加载失败'
		: 'The Vectra interface did not load';
	var box = document.createElement('div');
	var head = document.createElement('strong');
	var detail = document.createElement('code');

	box.className = 'alert-message warning';
	head.textContent = title;
	detail.textContent = String((err && err.message) || err);
	detail.style.display = 'block';
	detail.style.marginTop = '.5em';
	box.appendChild(head);
	box.appendChild(detail);

	return box;
}

return view.extend({
	load: function() {
		return loadApp().then(function(app) {
			return { app: app };
		}, function(err) {
			return { error: err };
		});
	},

	render: function(loaded) {
		if (!loaded || !loaded.app)
			return loadFailure(loaded && loaded.error);

		var host = document.createElement('div');

		host.className = 'vectra-app-host';
		try {
			this.unmount = loaded.app.mount(host, {
				call: callVectra,
				setPassword: setPassword,
				lang: document.documentElement.lang || ''
			});
		}
		catch (err) {
			return loadFailure(err);
		}

		return host;
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
