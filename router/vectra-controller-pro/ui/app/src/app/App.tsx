import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { describeError, type ErrInfo } from '../api/errors';
import { normAction } from '../api/normalize';
import { createStore } from '../api/store';
import type { ActionMethod, CallFn, ReadMethod, SetPasswordFn } from '../api/types';
import { LANG_LABEL, LANGS, makeT, pickLang, type Key, type Lang } from '../i18n';
import { makeFmt } from '../lib/format';
import { hasSubscription, health, type Level } from '../lib/health';
import { actionText } from '../lib/labels';
import { load, loadOneOf, save } from '../lib/storage';
import { Icon, type IconName } from '../ui/icons';
import { ErrorBox, IconButton, Seg, Skeleton, Spinner } from '../ui/kit';
import { Dialog, Toasts, type ToastItem } from '../ui/overlay';
import { Balancing } from '../views/Balancing';
import { Journal } from '../views/Journal';
import { Locations } from '../views/Locations';
import { MySites } from '../views/MySites';
import { Overview } from '../views/Overview';
import { Settings } from '../views/Settings';
import { Simple } from '../views/Simple';
import { Ctx, MODES, TABS, useApp, useBusy, useRes, type AppCtx, type ConfirmOpts, type Mode, type RunOpts, type TabId, type ToastTone } from './ctx';

const POLL_MS = 10_000;
// Outside the Overview the header verdict still needs diagnostics, just less often.
const DIAG_BACKGROUND_MS = 60_000;
const PENDING_MAX_MS = 30_000;
const PENDING_STEP_MS = 2_000;

// The simple view is one screen: its verdict needs the diagnostics; the
// location list is fetched when it is opened.
const SIMPLE_METHODS: ReadMethod[] = ['diagnostics', 'setup'];
/** How often a set-up, linked router's `setup` is read (a release or a new owner shows within it). */
const SETUP_CALM_MS = 60_000;
const TAB_METHODS: Record<TabId, ReadMethod[]> = {
  overview: ['diagnostics'],
  balancing: ['balancers', 'nodes'],
  locations: ['entries'],
  sites: ['rules'],
  settings: ['setup'],
  journal: ['logs'],
};
const TAB_ICON: Record<TabId, IconName> = { overview: 'gauge', balancing: 'split', locations: 'globe', sites: 'filter', settings: 'sliders', journal: 'list' };
const DONE_CODE: Record<ActionMethod, string> = {
  select_entry: 'entry_selected',
  reset_entry: 'entry_reset',
  pin_balancer: 'balancer_pinned',
  unpin_balancer: 'balancer_unpinned',
  set_probe_interval: 'probe_interval_set',
  restart_xray: 'xray_restarted',
  set_wifi: 'wifi_set',
  optimize_wifi: 'wifi_optimized',
  finish_setup: 'setup_finished',
  set_rules: 'rules_set',
  set_service: 'service_set',
  set_power: 'power_on',
  set_remote_shell: 'remote_shell_set',
};
const LEVEL_TONE: Record<Level, string> = { ok: 'ok', warn: 'warn', fail: 'fail', setup: 'info', unknown: 'mute', off: 'mute' };

type ThemePref = 'system' | 'light' | 'dark';
const THEMES: readonly ThemePref[] = ['system', 'light', 'dark'];
const THEME_ICON: Record<ThemePref, IconName> = { system: 'system', light: 'sun', dark: 'moon' };
const DARK_Q = '(prefers-color-scheme: dark)';

/** LuCI's bootstrap theme publishes its choice as <html data-darkmode>; otherwise follow the OS. */
function systemDark(): boolean {
  const attr = document.documentElement.getAttribute('data-darkmode');
  if (attr) return attr === 'true';
  return !window.matchMedia || window.matchMedia(DARK_Q).matches;
}

function useSystemDark(): boolean {
  const [dark, setDark] = useState(systemDark);
  useEffect(() => {
    const update = () => setDark(systemDark());
    const mql = window.matchMedia ? window.matchMedia(DARK_Q) : null;
    mql?.addEventListener?.('change', update);
    const mo = new MutationObserver(update);
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-darkmode'] });
    return () => {
      mql?.removeEventListener?.('change', update);
      mo.disconnect();
    };
  }, []);
  return dark;
}

function Header(p: {
  themePref: ThemePref;
  onTheme: () => void;
  full: boolean;
  onFull: () => void;
  setLang: (l: Lang) => void;
  setMode: (m: Mode) => void;
}) {
  const { t, f, lang, mode, locked, refresh } = useApp();
  const st = useRes('status');
  const dg = useRes('diagnostics');
  const busy = useBusy();
  const s = st.data;
  const pro = mode === 'pro';
  const level: Level | null = st.error ? 'unknown' : s ? health(s, dg.data).level : null;
  const clock = st.at === null ? null : f.clock(st.at);
  const updated = clock ? t('hdr.updated', { time: clock }) : st.error ? '—' : t('hdr.loading');
  return (
    <header class="hdr">
      <div class="brand">
        {/* The official wordmark (a mask filled with the brand gradient); the word is its text. */}
        <span class="wm" aria-hidden="true" />
        <span class="word">VECTRA</span>
        {pro && s?.router.hostname ? (
          <span class="host clip" title={s.router.hostname}>
            {s.router.hostname}
          </span>
        ) : null}
      </div>
      {pro ? (
        <span class={'pill t-' + (level ? LEVEL_TONE[level] : 'mute')} role="status">
          {level ? <i aria-hidden="true" /> : <Spinner />}
          {level ? t(('h.' + level) as Key) : null}
        </span>
      ) : null}
      <div class="tools">
        {pro ? (
          <button type="button" class="upd" onClick={refresh} title={updated} aria-label={t('hdr.refresh') + '. ' + updated}>
            {busy ? <Spinner /> : <Icon name="refresh" size={14} />}
            {clock || (st.error ? '—' : t('hdr.loading'))}
          </button>
        ) : null}
        {/* The operator's lock is the router's answer, so the switch waits for it. */}
        {s && !locked ? (
          <Seg small label={t('mode.label')} value={mode} onChange={p.setMode} options={MODES.map((m) => ({ value: m, label: t(('mode.' + m) as Key) }))} />
        ) : null}
        <Seg small label={t('hdr.lang')} value={lang} onChange={p.setLang} options={LANGS.map((l) => ({ value: l, label: LANG_LABEL[l], lang: l }))} />
        <IconButton icon={THEME_ICON[p.themePref]} label={t(('theme.' + p.themePref) as Key)} onClick={p.onTheme} />
        {pro ? <IconButton class="to-full" icon={p.full ? 'shrink' : 'expand'} label={t(p.full ? 'full.off' : 'full.on')} pressed={p.full} onClick={p.onFull} /> : null}
      </div>
    </header>
  );
}

function Tabs({ tab }: { tab: TabId }) {
  const { t, goTab } = useApp();
  const ref = useRef<HTMLDivElement>(null);
  const onKey = (e: KeyboardEvent) => {
    const i = TABS.indexOf(tab);
    const n = TABS.length;
    const k = e.key;
    const to = k === 'ArrowRight' ? (i + 1) % n : k === 'ArrowLeft' ? (i + n - 1) % n : k === 'Home' ? 0 : k === 'End' ? n - 1 : -1;
    if (to < 0) return;
    e.preventDefault();
    goTab(TABS[to]);
    (ref.current?.children[to] as HTMLElement | undefined)?.focus();
  };
  return (
    <div ref={ref} class="tabs" role="tablist" aria-label={t('app.tabs')} onKeyDown={onKey}>
      {TABS.map((id) => (
        <button
          type="button"
          role="tab"
          key={id}
          id={'vx-tab-' + id}
          class="tab"
          aria-selected={id === tab}
          aria-controls="vx-panel"
          tabIndex={id === tab ? 0 : -1}
          onClick={() => goTab(id)}
        >
          <Icon name={TAB_ICON[id]} />
          <span>{t(('tab.' + id) as Key)}</span>
        </button>
      ))}
    </div>
  );
}

const VIEWS = { overview: Overview, balancing: Balancing, locations: Locations, sites: MySites, settings: Settings };

/** Old data whose refresh failed stays on screen, marked as such. */
function Stale({ err }: { err: ErrInfo | null | undefined }) {
  const { t, refresh } = useApp();
  return err ? (
    <div class="banner">
      <ErrorBox t={t} err={err} onRetry={refresh} note={t('err.stale')} />
    </div>
  ) : null;
}

/** The visible tab's own methods; re-checked whenever a request settles. */
function TabStale({ tab }: { tab: TabId }) {
  const { store } = useApp();
  useBusy();
  return <Stale err={TAB_METHODS[tab].map((m) => store.get(m)).find((r) => r.data && r.error)?.error} />;
}

function Main({ tab, logsAuto, setLogsAuto }: { tab: TabId; logsAuto: boolean; setLogsAuto: (v: boolean) => void }) {
  const { t, refresh } = useApp();
  const st = useRes('status');
  // Never reached the controller at all: one clear error instead of five empty tabs.
  if (!st.data && st.error) return <ErrorBox big t={t} err={st.error} onRetry={refresh} />;
  const View = tab === 'journal' ? null : VIEWS[tab];
  return (
    <>
      <Tabs tab={tab} />
      <main>
        {st.data && st.error ? <Stale err={st.error} /> : <TabStale tab={tab} />}
        <div role="tabpanel" id="vx-panel" aria-labelledby={'vx-tab-' + tab} tabIndex={0}>
          {!st.data ? <Skeleton rows={4} /> : View ? <View /> : <Journal auto={logsAuto} setAuto={setLogsAuto} />}
        </div>
      </main>
    </>
  );
}

export function App({ call, setPassword = null, lang: hostLang, root }: { call: CallFn; setPassword?: SetPasswordFn | null; lang?: string; root: ShadowRoot | HTMLElement }) {
  // mount() renders once: the transport, and so the store, never change.
  const [store] = useState(() => createStore(call));
  const [lang, setLangState] = useState<Lang>(() => pickLang(load('lang'), hostLang, navigator.language));
  const t = useMemo(() => makeT(lang), [lang]);
  const f = useMemo(() => makeFmt(t), [t]);
  const [tab, setTab] = useState<TabId>(() => loadOneOf('tab', TABS, 'overview'));
  const [modePref, setModePref] = useState<Mode>(() => loadOneOf('mode', MODES, 'simple'));
  const [locked, setLocked] = useState(false);
  // An operator's lock wins over the stored choice; the choice is kept for when it lifts.
  const mode: Mode = locked ? 'simple' : modePref;
  // Vectra's own apps are dark; the owner can still pick light or follow the system.
  const [themePref, setThemePref] = useState<ThemePref>(() => loadOneOf('theme', THEMES, 'dark'));
  const sysDark = useSystemDark();
  const [full, setFull] = useState(() => load('full') === '1');
  const [logsAuto, setLogsAuto] = useState(() => load('logs.auto') !== '0');
  const [pending, setPending] = useState<string | null>(null);
  const [dialog, setDialog] = useState<(ConfirmOpts & { resolve: (ok: boolean) => void; back: Element | null }) | null>(null);
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const rootRef = useRef<HTMLDivElement>(null);
  // Mutable state the timers and async actions read; never triggers a render.
  const L = useRef({ alive: true, tab, mode, auto: logsAuto, t, pending: '', seq: 0, timers: new Set<ReturnType<typeof setTimeout>>() }).current;
  const tick = useRef<(force?: boolean) => Promise<unknown> | void>(() => undefined);
  L.tab = tab;
  L.mode = mode;
  L.auto = logsAuto;
  L.t = t;

  useEffect(() => store.on('status', () => setLocked(store.get('status').data?.ui.locked === true)), [store]);

  const later = (ms: number, fn: () => void) => {
    const id = setTimeout(() => {
      L.timers.delete(id);
      fn();
    }, ms);
    L.timers.add(id);
  };

  // ── polling: status + the visible tab every 10 s, paused while the page is hidden ──
  useEffect(() => {
    let id: ReturnType<typeof setTimeout> | undefined;
    // The first load happens even in a hidden tab (opened in the background, or
    // driven by automation); only the periodic refresh waits for visibility.
    let first = true;
    const run = (force?: boolean) => {
      clearTimeout(id);
      if (!L.alive || (document.hidden && !first)) return;
      first = false;
      const age = force ? 0 : 2000;
      const status = store.fetch('status', age);
      const rest = () => {
        // The operator's lock arrives with the status: until it is known, and
        // while it is on, ask only for what the simple view reads.
        const simple = L.mode === 'simple' || store.get('status').data?.ui.locked === true;
        const all: Promise<void>[] = [];
        if (!simple && L.tab !== 'overview') all.push(store.fetch('diagnostics', force ? 0 : DIAG_BACKGROUND_MS));
        for (const m of simple ? SIMPLE_METHODS : TAB_METHODS[L.tab]) {
          // A paused journal keeps its lines until the user asks for fresh ones.
          if (m === 'logs' && !L.auto && !force && store.get('logs').at !== null) continue;
          // Every answer is a process on the router: a router that is set up,
          // linked and running its subscription has little in `setup` to
          // watch — once a minute will do. The moment the subscription is
          // gone (a release, a new owner) it is read again at once.
          const sd = m === 'setup' ? store.get('setup').data : null;
          const st = store.get('status').data;
          const calm = !force && sd?.done === true && sd.vectra.linked === true && !!st && hasSubscription(st);
          all.push(store.fetch(m, calm ? SETUP_CALM_MS : age));
        }
        return Promise.all(all);
      };
      id = setTimeout(run, POLL_MS);
      return Promise.all([status, store.get('status').data ? rest() : status.then(rest)]);
    };
    const onVis = () => void (document.hidden ? clearTimeout(id) : run());
    tick.current = run;
    document.addEventListener('visibilitychange', onVis);
    run();
    return () => {
      clearTimeout(id);
      document.removeEventListener('visibilitychange', onVis);
    };
  }, [store, tab, logsAuto, mode]);

  useEffect(
    () => () => {
      L.alive = false;
      L.timers.forEach(clearTimeout);
      store.dispose();
    },
    [store],
  );

  // Width buckets drive the layout: the app lives in LuCI's column, not the viewport.
  useEffect(() => {
    const el = rootRef.current!;
    const set = (w: number) => w > 0 && el.setAttribute('data-w', w < 600 ? 's' : w < 880 ? 'm' : w < 1240 ? 'l' : 'x');
    set(el.getBoundingClientRect().width);
    if (!window.ResizeObserver) return;
    const ro = new ResizeObserver((e) => set(e[0].contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Full screen is a Pro tool (wide tables, the journal); the simple view is one narrow column.
  const isFull = full && mode === 'pro';
  // Full screen is a fixed overlay above LuCI's chrome; the page behind must not scroll.
  useEffect(() => {
    if (!isFull) return;
    const s = document.documentElement.style;
    const prev = s.overflow;
    s.overflow = 'hidden';
    return () => {
      s.overflow = prev;
    };
  }, [isFull]);

  // ── actions ──────────────────────────────────────────────────────────────
  const dismiss = (id: number) => setToasts((list) => list.filter((x) => x.id !== id));
  const toast = (tone: ToastTone, text: string, detail?: string | null) => {
    const id = ++L.seq;
    setToasts((list) => list.slice(-3).concat({ id, tone, text, detail }));
    // Failures stay until closed; the rest step aside on their own.
    if (tone !== 'fail') later(tone === 'warn' ? 9000 : 5000, () => dismiss(id));
    return id;
  };
  const confirm = (o: ConfirmOpts) =>
    new Promise<boolean>((resolve) => setDialog({ ...o, resolve, back: (root as ShadowRoot).activeElement || document.activeElement }));
  const refresh = () => void tick.current(true);

  /** Keyboard focus that fell out of the app with a removed control goes to the panel, not to <body>. */
  const keepFocus = () => {
    const r = root as ShadowRoot;
    const a = document.activeElement;
    if (!r.activeElement && (!a || a === document.body)) (r.getElementById?.('vx-panel') as HTMLElement | null)?.focus({ preventScroll: true });
  };

  const run = async (method: ActionMethod, params: Record<string, unknown>, o: RunOpts): Promise<boolean> => {
    const hadFocus = !!(root as ShadowRoot).activeElement;
    if (L.pending || (o.confirm && !(await confirm(o.confirm))) || !L.alive || L.pending) return false;
    L.pending = o.key;
    setPending(o.key);
    let done = false;
    let again: RunOpts['retry'] = undefined;
    try {
      const before = store.get('status').data;
      const res = normAction(await call(method, params));
      if (!L.alive) return false;
      if (!res.ok && o.retry && res.code === o.retry.code) {
        // Asked again below, once this one has let go of the controls.
        again = o.retry;
      } else {
        // The simple view speaks to the router's owner: its own words, no raw detail.
        const simple = L.mode === 'simple';
        const first =
          res.ok && o.quiet && res.code !== 'pending'
            ? 0
            : toast(
                res.ok ? (res.code === 'pending' ? 'info' : 'ok') : 'fail',
                !res.ok && o.fail?.[res.code] ? L.t(o.fail[res.code]!) : actionText(L.t, res.code, simple, res.ok),
                simple ? null : res.detail,
              );
        done = res.ok && res.code !== 'pending';
        if (res.ok && res.code === 'pending' && (o.landed || o.settled)) {
          // Accepted but still applying: keep asking until the router shows it.
          let landed = false;
          for (const deadline = Date.now() + (o.waitMs ?? PENDING_MAX_MS); !landed && L.alive && Date.now() < deadline; ) {
            await new Promise<void>((r) => later(PENDING_STEP_MS, r));
            if (o.settled) {
              await store.fetch(o.settled.read);
              const d = store.get(o.settled.read).data;
              landed = d != null && o.settled.ok(d);
            } else {
              await store.fetch('status');
              const s = store.get('status').data;
              landed = !!s && o.landed!(s, before);
            }
          }
          if (!L.alive) return false;
          dismiss(first);
          // A `pending` change is kept only if it succeeds: not seen in 30 s means not applied.
          toast(landed ? 'ok' : 'info', landed ? actionText(L.t, o.done ?? DONE_CODE[method], simple) : L.t('a.late'));
          done = landed;
        }
      }
    } catch (e) {
      if (L.alive) L.mode === 'simple' ? toast('fail', L.t('s.a.fail')) : toast('fail', L.t('err.other'), describeError(e).raw);
    } finally {
      L.pending = '';
      if (L.alive) {
        setPending(null);
        // Fresh data may replace the control that had focus (a pin becomes a badge).
        Promise.resolve(tick.current(true)).then(() => L.alive && hadFocus && later(50, keepFocus));
      }
    }
    if (again && L.alive) return run(method, again.params, again.opts);
    return done;
  };

  const goTab = useCallback((id: TabId) => {
    setTab(id);
    save('tab', id);
  }, []);

  // Stable identities for the context: the functions above only read `L` and refs.
  const fns = useRef({ run, confirm, toast, refresh }).current;
  const ctx = useMemo<AppCtx>(
    () => ({ t, f, lang, mode, locked, store, setPassword, pending, root, goTab, run: fns.run, confirm: fns.confirm, refresh: fns.refresh, toast: (a, b, c) => void fns.toast(a, b, c) }),
    [t, f, lang, mode, locked, store, setPassword, pending, root, goTab, fns],
  );

  const setLang = (l: Lang) => {
    setLangState(l);
    save('lang', l);
  };
  const setMode = (m: Mode) => {
    setModePref(m);
    save('mode', m);
  };
  const cycleTheme = () => {
    const next = THEMES[(THEMES.indexOf(themePref) + 1) % THEMES.length];
    setThemePref(next);
    save('theme', next);
  };
  const toggleFull = () => {
    setFull(!full);
    save('full', full ? '0' : '1');
  };
  const setAuto = (v: boolean) => {
    setLogsAuto(v);
    save('logs.auto', v ? '1' : '0');
  };

  return (
    <Ctx.Provider value={ctx}>
      <div
        ref={rootRef}
        class={'vx' + (isFull ? ' full' : '') + ' m-' + mode}
        data-theme={(themePref === 'system' ? sysDark : themePref === 'dark') ? 'dark' : 'light'}
        role="region"
        aria-label={t('app.region')}
        lang={lang}
      >
        <div class="vx-in" inert={dialog ? true : undefined}>
          <Header themePref={themePref} onTheme={cycleTheme} full={isFull} onFull={toggleFull} setLang={setLang} setMode={setMode} />
          {mode === 'simple' ? <Simple /> : <Main tab={tab} logsAuto={logsAuto} setLogsAuto={setAuto} />}
        </div>
        {dialog ? (
          <Dialog
            t={t}
            title={dialog.title}
            body={dialog.body}
            ok={dialog.ok}
            back={dialog.back}
            onClose={(ok) => {
              dialog.resolve(ok);
              setDialog(null);
            }}
          />
        ) : null}
        <Toasts t={t} items={toasts} onClose={dismiss} />
      </div>
    </Ctx.Provider>
  );
}
