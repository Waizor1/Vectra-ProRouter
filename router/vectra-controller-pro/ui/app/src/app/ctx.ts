import { createContext } from 'preact';
import { useContext, useEffect, useReducer, useRef } from 'preact/hooks';
import type { Res, Store } from '../api/store';
import type { ActionMethod, ReadData, ReadMethod, SetPasswordFn, Status } from '../api/types';
import type { Key, Lang, T } from '../i18n';
import type { Fmt } from '../lib/format';

export type TabId = 'overview' | 'balancing' | 'nodes' | 'locations' | 'sites' | 'journal';
// The servers come second: switching one is what a person does most, after a look at the overview.
export const TABS: readonly TabId[] = ['overview', 'locations', 'balancing', 'nodes', 'sites', 'journal'];

/** simple: one screen for the router's owner. pro: the tabs, for whoever runs it. */
export type Mode = 'simple' | 'pro';
export const MODES: readonly Mode[] = ['simple', 'pro'];

export type ToastTone = 'ok' | 'info' | 'warn' | 'fail';

export interface ConfirmOpts {
  title: string;
  body: string;
  ok: string;
}

export interface RunOpts {
  /** Identifies the control that started the action, to show its spinner. */
  key: string;
  confirm?: ConfirmOpts;
  /** For a `pending` answer: does this status prove the change landed? `before` is the one seen when it was sent. */
  landed?: (now: Status, before: Status | null) => boolean;
  /** No toast when it works: the screen itself shows what happened. Failures still say so. */
  quiet?: boolean;
  /** A failure this action explains better than the generic sentence for its code. */
  fail?: Partial<Record<string, Key>>;
  /** For a `pending` answer that shows in another read than `status`: which one to poll, and when it has landed. */
  settled?: { read: ReadMethod; ok: (data: unknown) => boolean };
  /** How long a `pending` answer is watched for landing, ms (30 s when not said). */
  waitMs?: number;
  /** The code a landed `pending` change is told with, when it is not the method's own. */
  done?: string;
}

export interface AppCtx {
  t: T;
  f: Fmt;
  lang: Lang;
  /** The view on screen: the stored choice, or `simple` whenever the operator locked the router. */
  mode: Mode;
  /** The operator allows only the simple view on this router (status.ui.locked). */
  locked: boolean;
  store: Store;
  /** LuCI's own change of the router's password, from the host page; null: none to offer. */
  setPassword: SetPasswordFn | null;
  /** Key of the action in flight, or null. Every mutating control is disabled while set. */
  pending: string | null;
  /** Resolves true once the change is in effect; false when cancelled, refused or not seen landing. */
  run(method: ActionMethod, params: Record<string, unknown>, opts: RunOpts): Promise<boolean>;
  confirm(opts: ConfirmOpts): Promise<boolean>;
  toast(tone: ToastTone, text: string, detail?: string | null): void;
  goTab(tab: TabId): void;
  refresh(): void;
  /** Where overlays may attach helper nodes (the shadow root). */
  root: ShadowRoot | HTMLElement;
}

export const Ctx = createContext<AppCtx>(null as unknown as AppCtx);
export const useApp = () => useContext(Ctx);

/**
 * Subscribe to one store key and return its current value. The value read
 * during render is re-checked once the subscription exists, so an answer that
 * lands between render and effect is not missed.
 */
function useSub<V>(store: Store, key: ReadMethod | 'fetching', read: () => V): V {
  const [, bump] = useReducer((n: number, _: void) => n + 1, 0);
  const value = read();
  const seen = useRef(value);
  seen.current = value;
  useEffect(() => {
    const off = store.on(key, () => bump());
    if (read() !== seen.current) bump();
    return off;
  }, [store, key]); // `read` closes over store and key only
  return value;
}

/** One read method's data/error; re-renders only when that method changes. */
export function useRes<M extends ReadMethod>(m: M): Res<ReadData[M]> {
  const { store } = useApp();
  return useSub(store, m, () => store.get(m));
}

/** True while any read request is on the wire (header spinner). */
export function useBusy(): boolean {
  const { store } = useApp();
  return useSub(store, 'fetching', () => store.busy());
}
