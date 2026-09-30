// Answers captured from a real vctl behind real rpcd + LuCI (the UI stand in
// test/dataplane, OpenWrt 24.10.8). The app must load them — including in a tab
// that is hidden when it mounts — and never sit on its loading skeleton.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createStore, CALL_TIMEOUT_MS } from '../src/api/store';
import { normalize } from '../src/api/normalize';
import type { CallFn, ReadMethod } from '../src/api/types';
import { TABS } from '../src/app/ctx';
import { mount } from '../src/mount';
import balancers from './real/balancers.json';
import diagnostics from './real/diagnostics.json';
import entries from './real/entries.json';
import logs from './real/logs.json';
import nodes from './real/nodes.json';
import status from './real/status.json';

// Captured from vctl 0.3, before the setup wizard and "My sites": that router
// answers those methods the way LuCI reports an unknown ubus method.
const REAL: Partial<Record<ReadMethod, unknown>> = { status, balancers, nodes, entries, diagnostics, logs };
const NEWER: string[] = ['setup', 'wan_check', 'rules'];
const realCall: CallFn = (m) =>
  NEWER.indexOf(m) >= 0
    ? Promise.reject(new Error(`RPC call to vectra/${m} failed with ubus code 3: Method not found`))
    : m in REAL
      ? Promise.resolve(JSON.parse(JSON.stringify(REAL[m as ReadMethod])))
      : Promise.resolve({ ok: true, code: 'balancer_pinned', detail: null });

let cleanup: (() => void)[] = [];
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  vi.useRealTimers();
  // @ts-expect-error drop the per-test override, back to the prototype getter
  delete document.hidden;
  localStorage.clear();
});

async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await new Promise((r) => setTimeout(r, 0));
}

function start(call: CallFn = realCall, mode: 'simple' | 'pro' = 'pro') {
  localStorage.setItem('vectra.ui.mode', mode);
  const host = document.createElement('div');
  document.body.appendChild(host);
  cleanup.push(mount(host, { call, lang: 'en' }), () => host.remove());
  return host.shadowRoot!;
}

describe('real router answers', () => {
  it('normalize without losing a field', () => {
    for (const m of Object.keys(REAL) as ReadMethod[]) expect(normalize(m, REAL[m]), m).toMatchObject(REAL[m] as object);
    // An older router without the wizard: no setup data, no wizard, no crash.
    expect(NEWER.length).toBe(3);
    // Captured from vctl 0.3, before the operator's lock: an older router means "not locked".
    expect(normalize('status', REAL.status).ui).toEqual({ locked: null });
  });

  it('render every tab with data, not the skeleton', async () => {
    const root = start();
    await settle();
    expect(root.querySelector('.verdict')?.textContent).toBe('Works, with warnings');
    expect(root.textContent).toContain('Stand A');
    for (const tab of TABS) {
      root.querySelector<HTMLElement>('#vx-tab-' + tab)!.click();
      await settle();
      expect(root.querySelector('.skel'), tab).toBeNull();
      expect(root.textContent, tab).not.toMatch(/undefined|NaN|\bnull\b/);
    }
    expect(root.textContent).toContain('node-dead');
  });

  it('render the simple view: works, the location, no technical words', async () => {
    const root = start(realCall, 'simple');
    await settle();
    expect(root.querySelector('.verdict')?.textContent).toBe('Everything works');
    expect(root.querySelector('.sv-loc b')?.textContent).toBe('Stand A');
    const text = (root.textContent ?? '').replace(/Vectra \S+/g, '');
    expect(text).not.toMatch(/xray|node|balancer|undefined|NaN|\bnull\b/i);
  });

  it('load in a tab that is hidden when the app mounts', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
    const root = start();
    await settle();
    expect(root.querySelector('.skel')).toBeNull();
    expect(root.querySelector('.verdict')?.textContent).toBe('Works, with warnings');
  });
});

describe('a call that never answers', () => {
  it('ends, in the simple view, in a plain sentence and a retry, then recovers', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
    let hang = true;
    const root = start((m, p) => (hang ? new Promise(() => undefined) : realCall(m, p)), 'simple');
    await vi.advanceTimersByTimeAsync(CALL_TIMEOUT_MS + 10);
    expect(root.querySelector('.skel')).toBeNull();
    expect(root.querySelector('.verdict')?.textContent).toBe('The router did not answer');
    expect(root.querySelector('.sv-raw code')?.textContent).toContain('request timed out');
    hang = false;
    await vi.advanceTimersByTimeAsync(CALL_TIMEOUT_MS);
    expect(root.querySelector('.verdict')?.textContent).toBe('Everything works');
  });

  // What LuCI's batched RPC did in a background tab: the request is queued and never sent.
  it('ends in an explained error instead of an endless skeleton, then recovers', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
    let hang = true;
    const root = start((m, p) => (hang ? new Promise(() => undefined) : realCall(m, p)));
    await vi.advanceTimersByTimeAsync(20_000);
    expect(root.querySelector('.skel')).not.toBeNull();
    await vi.advanceTimersByTimeAsync(CALL_TIMEOUT_MS - 20_000 + 10);
    expect(root.querySelector('.skel')).toBeNull();
    expect(root.querySelector('.err')?.textContent).toContain('request timed out');
    hang = false;
    // The retry already on the wire times out as well; the poll after it gets through.
    await vi.advanceTimersByTimeAsync(CALL_TIMEOUT_MS);
    expect(root.querySelector('.err')).toBeNull();
    expect(root.querySelector('.verdict')?.textContent).toBe('Works, with warnings');
  });

  it('times out, shows the error and lets the next poll ask again', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    let hang = true;
    const call = vi.fn((m: string) => (hang ? new Promise(() => undefined) : Promise.resolve(REAL[m as ReadMethod])));
    const store = createStore(call);
    const first = store.fetch('status');
    await vi.advanceTimersByTimeAsync(CALL_TIMEOUT_MS + 10);
    await first;
    expect(store.get('status').error?.kind).toBe('timeout');
    expect(store.busy()).toBe(false);
    hang = false;
    await store.fetch('status');
    expect(store.get('status').data?.version).toBe('dataplane-stand');
    expect(store.get('status').error).toBeNull();
    store.dispose();
  });
});
