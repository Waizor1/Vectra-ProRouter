// The router's password: on a router out of the box anyone on the LAN can open
// its settings (OpenWrt ships without one). The wizard asks for one first and
// cannot be walked past without it; the main screen says so calmly and offers
// to set one, and always to change it. The password goes to LuCI's own change
// (luci.setPassword, handed over by the LuCI view) — never to vctl.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import type { Setup } from '../src/api/types';
import { createMock } from '../src/mock/transport';

let cleanup: (() => void)[] = [];
beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup.forEach((fn) => fn());
  cleanup = [];
  document.body.innerHTML = '';
});

describe('the mock router’s password', () => {
  it('takes LuCI’s change like a router: setup then says a password is set; failures as LuCI reports them', async () => {
    const mock = (fails?: 'refused' | 'denied' | 'offline') => {
      const m = createMock({ scenario: 'unboxed', latencyMs: 0, live: false, passwordFails: fails });
      cleanup.push(m.dispose);
      return m;
    };
    const ok = mock();
    expect(((await ok.call('setup')) as Setup).passwordSet).toBe(false);
    expect(await ok.setPassword('correct horse')).toBe(true);
    expect(((await ok.call('setup')) as Setup).passwordSet).toBe(true);
    expect(await mock('refused').setPassword('correct horse')).toBe(false);
    await expect(mock('denied').setPassword('correct horse')).rejects.toThrow(/luci\/setPassword failed with ubus code 6/);
    await expect(mock('offline').setPassword('correct horse')).rejects.toThrow(/aborted/);
    // A router with Vectra's plugin missing still has LuCI: the change works.
    const down = createMock({ scenario: 'down', latencyMs: 0, live: false });
    cleanup.push(down.dispose);
    expect(await down.setPassword('correct horse')).toBe(true);
  });
});
