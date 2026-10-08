// Preact runs effects "after paint": on the next animation frame, or 35 ms
// later. happy-dom's frames tick in real time, so under fake timers a test
// could depend on how busy the machine was. Here effects wait for the timer
// queue alone — the fake clock when a test fakes it — as they would after a
// real frame.
//
// A flush queued on the real clock (a test without fake timers) must not
// outlive its test: one still pending when the next test fakes the clock
// runs on the real clock in the middle of it, after that test's first
// tick(), and its effects miss the fake time they were waiting for (the
// "my sites" flake under load). So every test ends by waiting, on the real
// clock, for the real-clock flushes it left behind.
import { options } from 'preact';
import { afterEach, vi } from 'vitest';

const realSetTimeout = globalThis.setTimeout;
let realFlushes = 0;

options.requestAnimationFrame = (flush: () => void) => {
  if (vi.isFakeTimers()) {
    setTimeout(flush, 0);
    return;
  }
  realFlushes++;
  setTimeout(() => {
    realFlushes--;
    flush();
  }, 0);
};

afterEach(async () => {
  for (let i = 0; realFlushes > 0 && i < 100; i++) await new Promise((r) => realSetTimeout(r, 1));
});
