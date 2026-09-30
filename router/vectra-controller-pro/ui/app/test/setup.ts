// Preact runs effects "after paint": on the next animation frame, or 35 ms
// later. happy-dom's frames tick in real time, so under fake timers a test
// could depend on how busy the machine was. Here effects wait for the timer
// queue alone — the fake clock when a test fakes it — as they would after a
// real frame.
import { options } from 'preact';

options.requestAnimationFrame = (flush: () => void) => {
  setTimeout(flush, 0);
};
