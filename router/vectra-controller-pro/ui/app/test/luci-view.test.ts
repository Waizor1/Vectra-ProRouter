// The shipped LuCI view, evaluated the way LuCI's loader does it: the file is
// the body of `function(window, document, L, <required classes>)`.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { describeError } from '../src/api/errors';

const VIEW = readFileSync(resolve(__dirname, '../../../openwrt/files/www/luci-static/resources/view/vectra/app.js'), 'utf8');

interface FakeEl {
  tag: string;
  children: FakeEl[];
  style: Record<string, string>;
  className?: string;
  textContent?: string;
  src?: string;
  async?: boolean;
  onload?: () => void;
  onerror?: () => void;
  appendChild(c: FakeEl): FakeEl;
}

function setup(opts: { lang?: string; rpcImpl?: (opts: Record<string, unknown>, args: unknown[]) => unknown } = {}) {
  const el = (tag: string): FakeEl => ({ tag, children: [], style: {}, appendChild(c) { this.children.push(c); return c; } });
  const head = el('head');
  const doc = { createElement: el, head, documentElement: { lang: opts.lang ?? 'ru' } };
  const win: { VectraApp?: unknown } = {};
  const declared: Record<string, unknown>[] = [];
  const invoked: unknown[][] = [];
  const rpc = {
    declare(o: Record<string, unknown>) {
      declared.push(o);
      return (...args: unknown[]) => {
        invoked.push(args);
        return opts.rpcImpl ? opts.rpcImpl(o, args) : Promise.resolve({ method: o.method, args });
      };
    },
  };
  const view = { extend: <T>(o: T) => o };
  const cls = new Function('window', 'document', 'L', 'view', 'rpc', VIEW)(win, doc, {}, view, rpc) as {
    load(): Promise<{ app?: unknown; error?: unknown }>;
    render(loaded: unknown): FakeEl;
    handleSave: unknown;
    handleSaveApply: unknown;
    handleReset: unknown;
  };
  return { cls, win, head, declared, invoked };
}

/** Load a fake app and render; returns what mount() received. */
async function mounted(opts: Parameters<typeof setup>[0] = {}) {
  const env = setup(opts);
  const mount = vi.fn(() => () => undefined);
  const loading = env.cls.load();
  env.win.VectraApp = { mount };
  env.head.children[0].onload!();
  const host = env.cls.render(await loading);
  const [[mountHost, mountOpts]] = mount.mock.calls as unknown as [
    [FakeEl, { call: (m: string, p?: object) => Promise<unknown>; setPassword: (pw: string) => Promise<boolean>; lang: string }],
  ];
  return { ...env, host, mountHost, call: mountOpts.call, setPassword: mountOpts.setPassword, lang: mountOpts.lang };
}

describe('LuCI view', () => {
  it('hides the save/apply/reset footer', () => {
    const { cls } = setup();
    expect(cls.handleSave).toBeNull();
    expect(cls.handleSaveApply).toBeNull();
    expect(cls.handleReset).toBeNull();
  });

  it('loads the bundle once, with a version cache-buster, and waits for window.VectraApp', async () => {
    const { cls, head, win } = setup();
    const loading = cls.load();
    const script = head.children[0];
    const version = /var APP_VERSION = '([^']+)'/.exec(VIEW)![1];
    expect(script.src).toBe('/luci-static/vectra/vectra-app.js?v=' + encodeURIComponent(version));
    win.VectraApp = { mount: () => () => undefined };
    script.onload!();
    expect((await loading).app).toBe(win.VectraApp);
  });

  it('reports a bundle that fails to load instead of throwing', async () => {
    const { cls, head } = setup({ lang: 'en' });
    const loading = cls.load();
    head.children[0].onerror!();
    const loaded = await loading;
    expect(String(loaded.error)).toContain('vectra-app.js');
    const box = cls.render(loaded);
    expect(box.className).toContain('alert-message');
    expect(box.children[0].textContent).toBe('The router interface did not load');
  });

  it('shows the same alert when mount() throws', async () => {
    const { cls, head, win } = setup({ lang: 'zh-cn' });
    const loading = cls.load();
    win.VectraApp = {
      mount: () => {
        throw new Error('attachShadow is not a function');
      },
    };
    head.children[0].onload!();
    const box = cls.render(await loading);
    expect(box.className).toContain('alert-message');
    expect(box.children[0].textContent).toBe('路由器界面加载失败');
    expect(box.children[1].textContent).toBe('attachShadow is not a function');
  });

  it('mounts the app with the transport and LuCI’s language', async () => {
    const { host, mountHost, lang, call } = await mounted({ lang: 'zh-cn' });
    expect(mountHost).toBe(host);
    expect(host.className).toBe('vectra-app-host');
    expect(lang).toBe('zh-cn');
    expect(typeof call).toBe('function');
  });

  it('declares vectra.<method> with the param names and passes the values in order', async () => {
    const { call, declared, invoked } = await mounted();
    await call('status');
    await call('logs', { lines: 200 });
    await call('pin_balancer', { balancer: 'BL-MAIN', node: 'bridge-de5' });
    const opts = { object: 'vectra', expect: { '': {} }, reject: true, nobatch: true };
    expect(declared).toEqual([
      { ...opts, method: 'status', params: [] },
      { ...opts, method: 'logs', params: ['lines'] },
      { ...opts, method: 'pin_balancer', params: ['balancer', 'node'] },
    ]);
    expect(invoked).toEqual([[], [200], ['BL-MAIN', 'bridge-de5']]);
  });

  it('declares each method signature once', async () => {
    const { call, declared } = await mounted();
    await call('status');
    await call('status', {});
    await call('logs', { lines: 200 });
    await call('logs', { lines: 50 });
    expect(declared.map((d) => d.method)).toEqual(['status', 'logs']);
  });

  it('hands a LuCI rejection to the app as a rejection it can explain', async () => {
    const failure = new Error('RPC call to vectra/status failed with ubus code 4: Object not found');
    const { call } = await mounted({ rpcImpl: () => Promise.reject(failure) });
    const err = await call('status').catch((e: unknown) => e);
    expect(err).toBe(failure);
    expect(describeError(err).kind).toBe('not_found');
  });

  it('turns a synchronous throw inside rpc into a rejection', async () => {
    const { call } = await mounted({
      rpcImpl: () => {
        throw new Error('Access denied');
      },
    });
    await expect(call('status')).rejects.toThrow('Access denied');
  });

  // The router's password is LuCI's: the view hands the app LuCI's own
  // change (the call System → Administration makes), for root. vctl never
  // sees a password.
  it('hands the app LuCI’s password change: luci.setPassword for root, declared once, on first use', async () => {
    const { setPassword, declared, invoked } = await mounted({ rpcImpl: (o) => Promise.resolve(o.object === 'luci' ? true : {}) });
    expect(typeof setPassword).toBe('function');
    expect(declared).toEqual([]);
    expect(await setPassword('correct horse')).toBe(true);
    expect(await setPassword('battery staple')).toBe(true);
    expect(declared).toEqual([
      { object: 'luci', method: 'setPassword', params: ['username', 'password'], expect: { result: false }, reject: true, nobatch: true },
    ]);
    expect(invoked).toEqual([
      ['root', 'correct horse'],
      ['root', 'battery staple'],
    ]);
  });

  it('says the password took only when LuCI says so, and hands a failure over as a rejection', async () => {
    let answer: unknown = false;
    const { setPassword } = await mounted({ rpcImpl: () => (answer instanceof Error ? Promise.reject(answer) : Promise.resolve(answer)) });
    expect(await setPassword('correct horse')).toBe(false);
    answer = 'yes';
    expect(await setPassword('correct horse')).toBe(false);
    answer = new Error('RPC call to luci/setPassword failed with ubus code 6: Permission denied');
    const err = await setPassword('correct horse').catch((e: unknown) => e);
    expect(describeError(err).kind).toBe('access');
  });
});
