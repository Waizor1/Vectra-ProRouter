import type { ButtonHTMLAttributes, ComponentChildren } from 'preact';
import { useRef } from 'preact/hooks';
import type { ErrInfo } from '../api/errors';
import type { Key, T } from '../i18n';
import { Icon, type IconName } from './icons';

export type Tone = 'ok' | 'warn' | 'fail' | 'info' | 'warm' | 'mute';

// ── buttons ─────────────────────────────────────────────────────────────────

type BtnProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'icon'> & {
  /** p = primary, g = ghost */
  kind?: 'p' | 'g';
  small?: boolean;
  icon?: IconName;
  busy?: boolean;
};

export function Button({ kind, small, icon, busy, children, class: cls, disabled, onClick, ...rest }: BtnProps) {
  return (
    <button
      type="button"
      {...rest}
      class={'btn' + (kind ? ' b' + kind : '') + (small ? ' bs' : '') + (cls ? ' ' + cls : '')}
      // A busy button keeps keyboard focus (it is where the dialog returns it) but does nothing.
      disabled={!busy && disabled}
      aria-disabled={busy || undefined}
      aria-busy={busy || undefined}
      onClick={busy ? undefined : onClick}
    >
      {busy ? <Spinner /> : icon ? <Icon name={icon} size={small ? 14 : 16} /> : null}
      {children}
    </button>
  );
}

export const IconButton = ({ icon, label, pressed, onClick, class: cls }: { icon: IconName; label: string; pressed?: boolean; onClick: () => void; class?: string }) => (
  <button type="button" class={'btn bi' + (cls ? ' ' + cls : '')} aria-label={label} title={label} aria-pressed={pressed} onClick={onClick}>
    <Icon name={icon} size={17} />
  </button>
);

// ── status ──────────────────────────────────────────────────────────────────

export const Spinner = () => <span class="spin" aria-hidden="true" />;

export const Badge = ({ tone = 'mute', icon, children }: { tone?: Tone; icon?: IconName; children: ComponentChildren }) => (
  <span class={'bd t-' + tone}>
    {icon ? <Icon name={icon} size={12} /> : null}
    {children}
  </span>
);

export const checkTone = (status: string): Tone => (status === 'ok' || status === 'warn' || status === 'fail' ? status : 'mute');

/** Status never by colour alone: each status has its own shape. */
export const StatusIcon = ({ status }: { status: string }) => (
  <span class={'sicon t-' + checkTone(status)}>
    <Icon name={checkTone(status) === 'mute' ? 'unknown' : (status as IconName)} />
  </span>
);

/** Node health: filled dot = alive, cross = dead, ring = not probed yet. */
export const NodeDot = ({ alive, label }: { alive: boolean | null; label: string }) => (
  <span class={'ndot nd-' + (alive ? 'ok' : alive === false ? 'dead' : 'none')} title={label}>
    <span class="sr">{label}</span>
  </span>
);

export const Bars = ({ tone }: { tone: 'good' | 'ok' | 'slow' | null }) => {
  const lit = tone === 'good' ? 3 : tone === 'ok' ? 2 : tone ? 1 : 0;
  return (
    <span class="bars" aria-hidden="true">
      {[1, 2, 3].map((i) => (
        <i key={i} class={i <= lit ? 'on' : undefined} />
      ))}
    </span>
  );
};

export function Meter({ value, max, label, tone }: { value: number | null; max: number | null; label: string; tone?: Tone }) {
  if (value === null || max === null || max <= 0) return null;
  const pct = Math.max(0, Math.min(100, (value / max) * 100));
  return (
    <span class="meter" role="img" aria-label={label}>
      <span class={'t-' + (tone ?? (pct >= 90 ? 'fail' : pct >= 75 ? 'warn' : 'ok'))} style={{ width: pct.toFixed(1) + '%' }} />
    </span>
  );
}

// ── layout ──────────────────────────────────────────────────────────────────

export const Card = ({ title, aside, children, class: cls, id }: { title: ComponentChildren; aside?: ComponentChildren; children: ComponentChildren; class?: string; id: string }) => (
  <section class={'card' + (cls ? ' ' + cls : '')} aria-labelledby={id}>
    <header class="card-h">
      <h3 class="k" id={id}>
        {title}
      </h3>
      {aside}
    </header>
    {children}
  </section>
);

export type Row = [label: ComponentChildren, value: ComponentChildren, extra?: ComponentChildren];

export const KV = ({ rows }: { rows: Row[] }) => (
  <dl class="kv">
    {rows.map(([k, v, extra], i) => (
      <div key={i}>
        <dt>{k}</dt>
        <dd>
          {v}
          {extra}
        </dd>
      </div>
    ))}
  </dl>
);

/** A callout. `action`: the one thing to do about it, at its end (it wraps under the text when narrow). */
export const Note = ({ tone, children, action }: { tone: Tone; children: ComponentChildren; action?: ComponentChildren }) => (
  <div class={'note t-' + tone + (action ? ' note-act' : '')}>
    <Icon name={tone === 'fail' || tone === 'warn' ? tone : 'info'} />
    <span>{children}</span>
    {action}
  </div>
);

export const Empty = ({ children, icon = 'info' }: { children: ComponentChildren; icon?: IconName }) => (
  <div class="empty">
    <Icon name={icon} size={20} />
    <p>{children}</p>
  </div>
);

export const Skeleton = ({ rows = 3 }: { rows?: number }) => (
  <div class="skel" aria-hidden="true">
    {Array.from({ length: rows }, (_, i) => (
      <span key={i} />
    ))}
  </div>
);

/** A transport error explained in the user's language, the raw message kept verbatim. */
export const ErrorBox = ({ t, err, onRetry, big, note }: { t: T; err: ErrInfo; onRetry?: () => void; big?: boolean; note?: string }) => (
  <div class={big ? 'err big' : 'err'} role="alert">
    <Icon name="fail" size={big ? 28 : 20} />
    <div>
      <b>{t(('err.' + err.kind) as Key)}</b>
      <p>
        {t(('err.' + err.kind + '.hint') as Key)}
        {note ? ' ' + note : ''}
      </p>
      <code>{err.raw}</code>
    </div>
    {onRetry ? (
      <Button icon="refresh" onClick={onRetry}>
        {t('retry')}
      </Button>
    ) : null}
  </div>
);

// ── inputs ──────────────────────────────────────────────────────────────────

export interface Opt<V> {
  value: V;
  label: ComponentChildren;
  lang?: string;
}

/** Radio group with roving focus: Tab enters at the checked option, arrows move and select. */
export function Seg<V extends string | number>(p: { label: string; value: V | null; options: Opt<V>[]; onChange: (v: V) => void; disabled?: boolean; small?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const n = p.options.length;
  const at = Math.max(0, p.options.findIndex((o) => o.value === p.value));
  const onKey = (e: KeyboardEvent) => {
    const k = e.key;
    const to = k === 'Home' ? 0 : k === 'End' ? n - 1 : k === 'ArrowRight' || k === 'ArrowDown' ? (at + 1) % n : k === 'ArrowLeft' || k === 'ArrowUp' ? (at + n - 1) % n : -1;
    if (to < 0) return;
    e.preventDefault();
    p.onChange(p.options[to].value);
    (ref.current?.children[to] as HTMLElement | undefined)?.focus();
  };
  return (
    <div ref={ref} class={p.small ? 'seg seg-s' : 'seg'} role="radiogroup" aria-label={p.label} onKeyDown={onKey}>
      {p.options.map((o, i) => (
        <button
          type="button"
          role="radio"
          key={String(o.value)}
          aria-checked={o.value === p.value}
          tabIndex={i === at ? 0 : -1}
          lang={o.lang}
          disabled={p.disabled}
          onClick={() => p.onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export const Switch = ({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) => (
  <button type="button" role="switch" class="sw" aria-checked={checked} onClick={() => onChange(!checked)}>
    <i aria-hidden="true" />
    {label}
  </button>
);
