import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { deflateRawSync } from 'node:zlib';
import { describe, expect, it, vi } from 'vitest';
import { createStore } from '../src/api/store';
import { S } from '../src/i18n/strings';
import { inflate, pack91, unpack, unpack91 } from '../src/lib/inflate';

describe('store', () => {
  it('never runs two requests for the same method at once', async () => {
    let release!: (v: unknown) => void;
    const call = vi.fn((): Promise<unknown> => (call.mock.calls.length > 1 ? Promise.resolve({ version: '2' }) : new Promise((r) => (release = r))));
    const store = createStore(call);
    const a = store.fetch('status');
    const b = store.fetch('status');
    await Promise.resolve();
    expect(call).toHaveBeenCalledTimes(1);
    expect(store.busy()).toBe(true);
    release({ version: '1' });
    await Promise.all([a, b]);
    expect(store.busy()).toBe(false);
    expect(store.get('status').data?.version).toBe('1');
    await store.fetch('status');
    expect(call).toHaveBeenCalledTimes(2);
  });

  it('skips data younger than maxAge and asks for logs with lines=200', async () => {
    const call = vi.fn((m: string, p?: object) => Promise.resolve({ m, p }));
    const store = createStore(call);
    await store.fetch('logs');
    await store.fetch('logs', 60_000);
    expect(call).toHaveBeenCalledTimes(1);
    expect(call).toHaveBeenCalledWith('logs', { lines: 200 });
  });

  it('keeps the last good data when a refresh fails, and records the error', async () => {
    let fail = false;
    const store = createStore(() => (fail ? Promise.reject(new Error('RPC call to vectra/status failed with ubus code 7: Request timed out')) : Promise.resolve({ version: 'x' })));
    await store.fetch('status');
    fail = true;
    await store.fetch('status');
    const r = store.get('status');
    expect(r.data?.version).toBe('x');
    expect(r.error?.kind).toBe('timeout');
  });

  it('does not stay in flight when a subscriber throws', async () => {
    const call = vi.fn(() => Promise.resolve({ version: 'x' }));
    const store = createStore(call);
    store.on('status', () => {
      throw new Error('subscriber');
    });
    await store.fetch('status').catch(() => undefined);
    expect(store.busy()).toBe(false);
    await store.fetch('status').catch(() => undefined);
    expect(call).toHaveBeenCalledTimes(2);
  });

  it('notifies only the subscribers of the method that changed, and nobody after dispose', async () => {
    const store = createStore(() => Promise.resolve({}));
    const onStatus = vi.fn();
    const onNodes = vi.fn();
    store.on('status', onStatus);
    const off = store.on('nodes', onNodes);
    await store.fetch('status');
    expect(onStatus).toHaveBeenCalledTimes(1);
    expect(onNodes).not.toHaveBeenCalled();
    off();
    store.dispose();
    await store.fetch('nodes');
    expect(onNodes).not.toHaveBeenCalled();
  });
});

describe('inflate', () => {
  const css = readFileSync(resolve(__dirname, '../src/styles/app.css'), 'utf8');
  const table = JSON.stringify(Object.entries(S));
  const random = Buffer.from(Array.from({ length: 5000 }, (_, i) => (i * 7919 + (i >> 3)) & 255));

  it('round-trips what zlib produces at every level, including stored blocks', () => {
    for (const text of [css, table, 'a', '', 'ааааа · 中文 · 🇩🇪'.repeat(300)]) {
      for (const level of [0, 1, 6, 9]) {
        const packed = deflateRawSync(Buffer.from(text), { level });
        expect(Buffer.from(inflate(packed)).toString('utf8')).toBe(text);
      }
    }
    for (const level of [0, 9]) expect(Buffer.from(inflate(deflateRawSync(random, { level })))).toEqual(random);
  });

  it('round-trips fixed-Huffman blocks', () => {
    const packed = deflateRawSync(Buffer.from('fixed huffman, fixed huffman'), { strategy: 4 /* Z_FIXED */ });
    expect(Buffer.from(inflate(packed)).toString()).toBe('fixed huffman, fixed huffman');
  });

  it('unpacks text the way the build packs it', () => {
    expect(unpack(pack91(deflateRawSync(Buffer.from(table), { level: 9 })))).toBe(table);
    expect(unpack(pack91(deflateRawSync(Buffer.from(css), { level: 9 })))).toBe(css);
  });

  it('packs any bytes into quote-free printable ASCII and back, at every length', () => {
    for (let len = 0; len < 40; len++) {
      const bytes = random.subarray(len * 3, len * 4);
      const text = pack91(bytes);
      expect(text).toMatch(/^[ !#-&(-\[\]-_a-~]*$/);
      expect(Buffer.from(unpack91(text).subarray(0, bytes.length))).toEqual(Buffer.from(bytes));
      expect(unpack91(text).length - bytes.length).toBeLessThanOrEqual(1);
    }
    expect(Buffer.from(unpack91(pack91(random)).subarray(0, random.length))).toEqual(random);
  });

  it('fails loudly on truncated input', () => {
    const packed = deflateRawSync(Buffer.from(css), { level: 9 });
    expect(() => inflate(packed.subarray(0, 100))).toThrow();
  });
});
