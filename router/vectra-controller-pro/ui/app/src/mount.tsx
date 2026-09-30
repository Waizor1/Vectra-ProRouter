// window.VectraApp.mount(host, { call, setPassword, lang }) → unmount.
// Everything renders into an open shadow root: LuCI's theme cannot leak in and
// our styles cannot leak out. Dialogs and toasts live in the same root.

import { render } from 'preact';
import type { CallFn, SetPasswordFn } from './api/types';
import { App } from './app/App';
import css from './styles/app.css?inline';

export interface MountOptions {
  /** Transport to the `vectra` ubus object: (method, params) → Promise<object>. */
  call: CallFn;
  /** LuCI's own change of the router's password; without it the app offers none. */
  setPassword?: SetPasswordFn;
  /** The host page's language (LuCI: document.documentElement.lang). */
  lang?: string;
}

const mounted = new WeakMap<HTMLElement, () => void>();

/** Where the package installs the brand files (see openwrt/Makefile). */
export const STATIC = '/luci-static/vectra/';

/**
 * Montserrat, Vectra's typeface, shipped with the package. @font-face does not
 * work from inside a shadow root in every browser, so the faces are declared
 * once on the page, under a name LuCI's own styles cannot collide with.
 */
function declareFonts(): void {
  if (document.getElementById('vectra-fonts')) return;
  const style = document.createElement('style');
  style.id = 'vectra-fonts';
  style.textContent = [500, 600, 700]
    .map((w) => `@font-face{font-family:"Vectra Montserrat";src:url("${STATIC}fonts/montserrat-${w}.woff2") format("woff2");font-weight:${w};font-style:normal;font-display:swap}`)
    .join('');
  document.head.appendChild(style);
}

export function mount(host: HTMLElement, opts: MountOptions): () => void {
  if (!host || typeof host.attachShadow !== 'function') throw new TypeError('VectraApp.mount: host must be an element');
  if (!opts || typeof opts.call !== 'function') throw new TypeError('VectraApp.mount: opts.call must be a function');

  const previous = mounted.get(host);
  if (previous) previous();
  declareFonts();

  const shadow = host.shadowRoot || host.attachShadow({ mode: 'open' });
  while (shadow.firstChild) shadow.removeChild(shadow.firstChild);
  const style = document.createElement('style');
  style.textContent = css;
  const container = document.createElement('div');
  container.className = 'vx-host';
  shadow.appendChild(style);
  shadow.appendChild(container);

  const setPassword = typeof opts.setPassword === 'function' ? opts.setPassword : null;
  render(<App call={opts.call} setPassword={setPassword} lang={opts.lang} root={shadow} />, container);

  let done = false;
  const unmount = () => {
    if (done) return;
    done = true;
    mounted.delete(host);
    render(null, container);
    while (shadow.firstChild) shadow.removeChild(shadow.firstChild);
  };
  mounted.set(host, unmount);
  return unmount;
}
