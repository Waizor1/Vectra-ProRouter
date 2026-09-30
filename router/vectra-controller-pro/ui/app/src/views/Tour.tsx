// A short tour of the main screen, right after the setup: four cards, one
// sentence each, the card itself lit up. It shows once per browser (the person
// can replay it from the footer), skips what is not on screen, and gives the
// page back the moment the person says so (Esc, "Skip", "Got it") — with the
// keyboard's focus where it was.

import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { useApp } from '../app/ctx';
import type { Key } from '../i18n';
import { Icon } from '../ui/icons';
import { Button } from '../ui/kit';

type Step = { sel: string; title: Key; text: Key };

const STEPS: readonly Step[] = [
  { sel: '.sv-st', title: 'tour.1.t', text: 'tour.1.d' },
  { sel: '.sv-loc', title: 'tour.2.t', text: 'tour.2.d' },
  { sel: '.sv-sites', title: 'tour.3.t', text: 'tour.3.d' },
  { sel: '.sv-help', title: 'tour.4.t', text: 'tour.4.d' },
];

const SEEN = 'vectra.ui.tour';
/** Below this width the bubble docks at the bottom of the screen. */
const NARROW = 600;
const GAP = 12;
const PAD = 8;

/** Whether this browser has been shown around already. */
export function tourSeen(): boolean {
  try {
    return localStorage.getItem(SEEN) === 'done';
  } catch {
    return false;
  }
}

function markSeen() {
  try {
    localStorage.setItem(SEEN, 'done');
  } catch {
    /* private mode: it may show again, which is harmless */
  }
}

interface Box {
  top: number;
  left: number;
  width: number;
  height: number;
}

export function Tour({ onDone }: { onDone: () => void }) {
  const { t, root } = useApp();
  // Only the cards that are on screen (a router without settings has fewer),
  // looked up once the page is drawn: the tour may mount together with them.
  const [steps, setSteps] = useState<readonly Step[] | null>(null);
  const [i, setI] = useState(0);
  const [box, setBox] = useState<Box | null>(null);
  const [bubbleH, setBubbleH] = useState(180);
  const lastH = useRef(180);
  const primary = useRef<HTMLButtonElement>(null);
  const bubble = useRef<HTMLDivElement>(null);
  // Where the keyboard was: it goes back there when the tour ends.
  const before = useRef<HTMLElement | null>(null);
  const step = steps?.[i];

  const close = () => {
    markSeen();
    const back = before.current ?? (root as ParentNode).querySelector<HTMLElement>('#vx-panel');
    back?.focus?.({ preventScroll: true });
    onDone();
  };

  useLayoutEffect(() => {
    before.current = ((root as ShadowRoot).activeElement as HTMLElement | null) ?? null;
    setSteps(STEPS.filter((s) => (root as ParentNode).querySelector(s.sel)));
  }, []);

  useLayoutEffect(() => {
    if (!step) return;
    const el = (root as ParentNode).querySelector<HTMLElement>(step.sel);
    if (!el) return;
    const calm = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
    // The bubble goes under the card (on a phone it docks at the bottom): the
    // card is centred together with the room the bubble needs below it — when
    // both fit on the screen. A taller card is centred alone: its bubble goes
    // beside it, and the card's top never leaves the screen.
    const margin = el.style.scrollMarginBottom;
    const room = lastH.current + 2 * GAP;
    const vh = typeof innerHeight === 'number' ? innerHeight : 800;
    if (el.getBoundingClientRect().height + 2 * PAD + room <= vh) el.style.scrollMarginBottom = room + 'px';
    el.scrollIntoView?.({ block: 'center', behavior: calm ? 'auto' : 'smooth' });
    el.style.scrollMarginBottom = margin;
    const measure = () => {
      const r = el.getBoundingClientRect();
      setBox({ top: r.top, left: r.left, width: r.width, height: r.height });
    };
    measure();
    // The page scrolls (smoothly, or by hand) and resizes under the tour.
    const id = setInterval(measure, 200);
    addEventListener('resize', measure);
    addEventListener('scroll', measure, true);
    return () => {
      clearInterval(id);
      removeEventListener('resize', measure);
      removeEventListener('scroll', measure, true);
    };
  }, [i, steps]);

  // The bubble's own height decides whether it fits below the card or above it.
  useLayoutEffect(() => {
    if (bubble.current && bubble.current.offsetHeight) setBubbleH((lastH.current = bubble.current.offsetHeight));
  }, [i, box]);

  useEffect(() => {
    primary.current?.focus({ preventScroll: true });
  }, [i, steps]);

  useEffect(() => {
    // Esc gives the page back; Tab stays inside the bubble, as in any dialog.
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') return close();
      if (e.key !== 'Tab' || !bubble.current) return;
      const keys = Array.from(bubble.current.querySelectorAll<HTMLElement>('button:not([disabled])'));
      if (!keys.length) return;
      const at = keys.indexOf((root as ShadowRoot).activeElement as HTMLElement);
      e.preventDefault();
      keys[(at + (e.shiftKey ? keys.length - 1 : 1) + keys.length) % keys.length].focus();
    };
    addEventListener('keydown', onKey);
    return () => removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    // Nothing of the main screen to show: done, and nothing was shown.
    if (steps && !steps.length) onDone();
  }, [steps]);

  if (!steps || !step) return null;
  const last = i === steps.length - 1;
  const vw = typeof innerWidth === 'number' ? innerWidth : 1024;
  const vh = typeof innerHeight === 'number' ? innerHeight : 800;
  // A phone: the bubble docks at the bottom. Wider: below the card if it fits,
  // else above it, else beside a card too tall for either (the other column
  // has the room), else as low as the screen allows — never off the screen.
  let place: Record<string, string> | undefined;
  if (vw < NARROW) place = { left: GAP + 'px', right: GAP + 'px', bottom: GAP + 'px', width: 'auto' };
  else if (box) {
    const bw = Math.min(380, vw - 2 * GAP);
    const below = box.top + box.height + PAD + GAP;
    const above = box.top - PAD - GAP - bubbleH;
    const leftOf = box.left - PAD - GAP - bw;
    const rightOf = box.left + box.width + PAD + GAP;
    const beside = leftOf >= GAP ? leftOf : rightOf + bw <= vw - GAP ? rightOf : null;
    const low = Math.max(GAP, vh - GAP - bubbleH);
    const [top, left] =
      below + bubbleH <= vh - GAP
        ? [below, null]
        : above >= GAP
          ? [above, null]
          : beside !== null
            ? [Math.max(GAP, Math.min(box.top, low)), beside]
            : [low, null];
    place = { top: top + 'px', left: (left ?? Math.max(GAP, Math.min(box.left, vw - GAP - bw))) + 'px' };
  }
  return (
    <>
      {/* Room under the last card, so that it too can scroll up and leave the bubble its place. */}
      <div class="tour-room" aria-hidden="true" style={{ height: bubbleH + 2 * GAP + 'px' }} />
      <div class="tour" role="dialog" aria-modal="true" aria-labelledby="vx-tour-t" aria-describedby="vx-tour-d">
        {box ? (
          <div
            class="tour-spot"
            aria-hidden="true"
            style={{ top: box.top - PAD + 'px', left: box.left - PAD + 'px', width: box.width + PAD * 2 + 'px', height: box.height + PAD * 2 + 'px' }}
          />
        ) : null}
        <div ref={bubble} class="tour-bub card" style={place}>
          <p class="hint">{t('tour.of', { n: i + 1, total: steps.length })}</p>
          <h2 id="vx-tour-t" class="k">
            {t(step.title)}
          </h2>
          <p id="vx-tour-d">{t(step.text)}</p>
          <div class="row">
            {last ? null : (
              <Button kind="g" small onClick={close}>
                {t('tour.skip')}
              </Button>
            )}
            <button ref={primary} type="button" class="btn bp bs push" onClick={() => (last ? close() : setI(i + 1))}>
              <Icon name={last ? 'ok' : 'arrow'} size={14} />
              {t(last ? 'tour.done' : 'w.next')}
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
