// Dialogs and toasts render inside the shadow root: above the app (and above
// LuCI's header in full-screen mode), below LuCI's own modal layer.

import { useEffect, useRef } from 'preact/hooks';
import type { ToastTone } from '../app/ctx';
import type { T } from '../i18n';
import { Icon } from './icons';
import { Button } from './kit';

export interface ToastItem {
  id: number;
  tone: ToastTone;
  text: string;
  detail?: string | null;
}

export function Dialog(p: { t: T; title: string; body: string; ok: string; back: Element | null; onClose: (ok: boolean) => void }) {
  const { t, title, body, ok, onClose } = p;
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const dlg = ref.current!;
    const root = dlg.getRootNode() as ShadowRoot;
    const back = p.back as HTMLButtonElement | null;
    const ours = !!back && back.getRootNode() === root;
    const first = () => dlg.querySelector('button')!.focus();
    // Start on the safe choice: Enter on an impactful dialog must not restart anything.
    first();
    // Browsers without `inert` could still move focus behind the dialog.
    const trap = (e: Event) => void (dlg.contains(e.target as Node) || first());
    root.addEventListener('focusin', trap);
    return () => {
      root.removeEventListener('focusin', trap);
      // Back to the control that opened it (recorded before the app behind went
      // inert), once this render has lifted `inert` again: Preact unmounts the
      // dialog before it patches its siblings.
      setTimeout(() =>
        back && !(ours && (!back.isConnected || back.disabled))
          ? back.focus()
          : (root.getElementById?.('vx-panel') as HTMLElement | null)?.focus({ preventScroll: true }),
      );
    };
  }, []);

  // Esc closes; Tab cycles through the dialog's buttons (the app behind it is inert).
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      onClose(false);
    }
    if (e.key !== 'Tab') return;
    const btns = [...ref.current!.querySelectorAll('button')];
    const at = btns.indexOf((ref.current!.getRootNode() as ShadowRoot).activeElement as HTMLButtonElement);
    const to = e.shiftKey ? (at > 0 ? -1 : btns.length - 1) : at < 0 || at === btns.length - 1 ? 0 : -1;
    if (to >= 0) {
      e.preventDefault();
      btns[to].focus();
    }
  };

  return (
    <div class="scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose(false)}>
      <div
        ref={ref}
        class="dlg"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="vx-dt"
        aria-describedby="vx-db"
        tabIndex={-1}
        onKeyDown={onKey}
      >
        <h2 id="vx-dt">{title}</h2>
        <p id="vx-db">{body}</p>
        <div class="row">
          <Button onClick={() => onClose(false)}>{t('cancel')}</Button>
          <Button kind="p" onClick={() => onClose(true)}>
            {ok}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** A stable polite live region; failures inside it are alerts and stay until closed. */
export const Toasts = ({ t, items, onClose }: { t: T; items: ToastItem[]; onClose: (id: number) => void }) => (
  <div class="toasts" aria-live="polite">
    {items.map((it) => (
      <div key={it.id} class={'toast t-' + it.tone} role={it.tone === 'fail' ? 'alert' : undefined}>
        <Icon name={it.tone} size={18} />
        <div>
          <p>{it.text}</p>
          {it.detail ? <small>{it.detail}</small> : null}
        </div>
        <button type="button" class="btn bi bs bg" aria-label={t('close')} title={t('close')} onClick={() => onClose(it.id)}>
          <Icon name="close" size={14} />
        </button>
      </div>
    ))}
  </div>
);
