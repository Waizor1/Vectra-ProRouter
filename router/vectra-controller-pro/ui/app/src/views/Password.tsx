// The router's password (contract README, "Router password"): root's, the one
// LuCI's login asks for. It is set with LuCI's own change, handed over by the
// host page (ctx.setPassword); vctl never sees it. The wizard's step and the
// main screen's dialog share the form below. Nothing here stores or logs a
// password: the fields are emptied as soon as LuCI has taken it.

import { useEffect, useRef, useState } from 'preact/hooks';
import { describeError } from '../api/errors';
import { useApp } from '../app/ctx';
import type { Key } from '../i18n';
import { Button, Field, Note } from '../ui/kit';
import { around, SLOT } from '../ui/words';

/** The shortest password the page takes. */
export const PW_MIN = 8;

export interface PwForm {
  pw: string;
  again: string;
  show: boolean;
  /** A field's mistake beside it; `call`: what went wrong with LuCI's change. */
  errs: { pw?: string; again?: string; call?: string };
  busy: boolean;
  setPw(v: string): void;
  setAgain(v: string): void;
  setShow(v: boolean): void;
  /** Checks the fields, then hands the password to LuCI; `onSaved` once LuCI has taken it. */
  save(): void;
}

/** A failed change in the owner's words: no ubus, no RPC. */
function failure(e: unknown): Key {
  const kind = describeError(e).kind;
  return kind === 'access' ? 'pw.e.access' : kind === 'network' || kind === 'timeout' ? 'pw.e.network' : 'pw.e.other';
}

/** `ids`: the two fields' ids, for the focus a mistake moves to. */
export function usePasswordForm(ids: readonly [string, string], onSaved: () => void): PwForm {
  const { t, store, setPassword, root } = useApp();
  const [pw, setPw] = useState('');
  const [again, setAgain] = useState('');
  const [show, setShow] = useState(false);
  const [errs, setErrs] = useState<PwForm['errs']>({});
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  useEffect(
    () => () => {
      alive.current = false;
    },
    [],
  );
  const focus = (id: string) => (root as ParentNode).querySelector<HTMLElement>('#' + id)?.focus();

  const save = async () => {
    if (busy || !setPassword) return;
    const e: PwForm['errs'] = {};
    if (!pw) e.pw = t('pw.e.empty');
    else if (Array.from(pw).length < PW_MIN) e.pw = t('pw.e.short');
    else if (!again) e.again = t('pw.e.again');
    else if (again !== pw) e.again = t('pw.e.mismatch');
    setErrs(e);
    if (e.pw || e.again) return focus(e.pw ? ids[0] : ids[1]);
    setBusy(true);
    let fail: Key | null = null;
    try {
      if ((await setPassword(pw)) !== true) fail = 'pw.e.refused';
    } catch (x) {
      fail = failure(x);
    }
    if (!alive.current) return;
    setBusy(false);
    if (fail) return setErrs({ call: t(fail) });
    setPw('');
    setAgain('');
    // `setup` says so at its next read: ask for it now.
    void store.fetch('setup', 0);
    onSaved();
  };

  return { pw, again, show, errs, busy, setPw, setAgain, setShow, save: () => void save() };
}

/** The password twice, a switch that shows both, and what went wrong with LuCI's change. */
export function PasswordFields({ f, ids }: { f: PwForm; ids: readonly [string, string] }) {
  const { t } = useApp();
  const type = f.show ? 'text' : 'password';
  return (
    <div class="pw-f">
      <div class="fld-g">
        <Field id={ids[0]} label={t('pw.new')} type={type} value={f.pw} onInput={f.setPw} error={f.errs.pw} hint={t('pw.hint')} auto="new-password" />
        <Field id={ids[1]} label={t('pw.again')} type={type} value={f.again} onInput={f.setAgain} error={f.errs.again} auto="new-password" />
      </div>
      <label class="chkbx">
        <input type="checkbox" checked={f.show} onChange={(e) => f.setShow((e.currentTarget as HTMLInputElement).checked)} />
        {t('pw.show')}
      </label>
      {f.errs.call ? (
        <div class="fld-err" role="alert">
          {f.errs.call}
        </div>
      ) : null}
    </div>
  );
}

/** The main screen, on a router without a password: calm, and the one thing to do about it. */
export function PasswordNote({ onSet }: { onSet: () => void }) {
  const { t } = useApp();
  return (
    <div class="sv-pw">
      <Note
        tone="warn"
        action={
          <Button small icon="lock" onClick={onSet}>
            {t('pw.set')}
          </Button>
        }
      >
        {around(t('pw.none.d', { b: SLOT }), <b>{t('pw.none.t')}</b>)}
      </Note>
    </div>
  );
}

const DLG_IDS = ['vx-pwd-1', 'vx-pwd-2'] as const;

/**
 * A small dialog to set the password (`first`: there is none) or change it.
 * Keyboard focus starts in the first field and stays inside; Esc and
 * "Cancel" leave the password as it was — not while LuCI is taking it — and
 * give the focus back to the control that opened it.
 */
export function PasswordDialog({ first, onClose }: { first: boolean; onClose: () => void }) {
  const { t, root } = useApp();
  const [done, setDone] = useState(false);
  const f = usePasswordForm(DLG_IDS, () => setDone(true));
  const ref = useRef<HTMLDivElement>(null);
  const shut = () => !f.busy && onClose();

  useEffect(() => {
    const dlg = ref.current!;
    const r = root as ShadowRoot;
    const back = (r.activeElement as HTMLElement | null) ?? null;
    // The dialog is fixed on screen: moving the focus into it must not scroll the page behind.
    const start = () => dlg.querySelector<HTMLElement>('input, button')?.focus({ preventScroll: true });
    start();
    // Browsers without `inert`, and a click on the page behind, could take the focus out.
    const trap = (e: Event) => void (dlg.contains(e.target as Node) || start());
    r.addEventListener('focusin', trap);
    return () => {
      r.removeEventListener('focusin', trap);
      // Back to the control that opened it once the dialog is gone — or, when it
      // went too (the note of a router that now has a password), to the panel.
      setTimeout(() =>
        back && back.isConnected ? back.focus({ preventScroll: true }) : (r.querySelector?.('#vx-panel') as HTMLElement | null)?.focus({ preventScroll: true }),
      );
    };
  }, []);

  // Saved: the fields are gone; the focus goes to the way out.
  useEffect(() => {
    if (done) ref.current?.querySelector<HTMLElement>('.row .bp')?.focus({ preventScroll: true });
  }, [done]);

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      return shut();
    }
    if (e.key !== 'Tab') return;
    const keys = Array.from(ref.current!.querySelectorAll<HTMLElement>('input, button:not([disabled])'));
    const at = keys.indexOf((root as ShadowRoot).activeElement as HTMLElement);
    const to = e.shiftKey ? (at > 0 ? -1 : keys.length - 1) : at < 0 || at === keys.length - 1 ? 0 : -1;
    if (to >= 0) {
      e.preventDefault();
      keys[to].focus();
    }
  };

  return (
    <div class="scrim">
      <div ref={ref} class="dlg dlg-pw" role="dialog" aria-modal="true" aria-labelledby="vx-pwd-t" aria-describedby="vx-pwd-d" onKeyDown={onKey}>
        <h2 id="vx-pwd-t">{t(done ? 'pw.saved.t' : first ? 'pw.t' : 'pw.change.t')}</h2>
        {done ? (
          <>
            <p id="vx-pwd-d" role="status">
              {t('pw.saved.d')}
            </p>
            <div class="row">
              <Button kind="p" onClick={onClose}>
                {t('close')}
              </Button>
            </div>
          </>
        ) : (
          <form
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              f.save();
            }}
          >
            <p id="vx-pwd-d">{t(first ? 'pw.d' : 'pw.change.d')}</p>
            <PasswordFields f={f} ids={DLG_IDS} />
            <div class="row">
              <Button disabled={f.busy} onClick={shut}>
                {t('cancel')}
              </Button>
              <Button kind="p" icon="lock" type="submit" busy={f.busy}>
                {t('pw.save')}
              </Button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
