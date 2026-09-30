// The router's password (contract README, "Router password"): root's, the one
// LuCI's login asks for. It is set with LuCI's own change, handed over by the
// host page (ctx.setPassword); vctl never sees it. The wizard's step and the
// main screen's dialog share the form below. Nothing here stores or logs a
// password: the fields are emptied as soon as LuCI has taken it.

import { useEffect, useRef, useState } from 'preact/hooks';
import { describeError } from '../api/errors';
import { useApp } from '../app/ctx';
import type { Key } from '../i18n';
import { Field } from '../ui/kit';

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
