import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { logRows, type LogRow } from '../lib/logs';
import { own } from '../lib/own';
import { copyText, loadOneOf, save } from '../lib/storage';
import { Button, Empty, ErrorBox, Seg, Skeleton, Switch } from '../ui/kit';

// What an owner opens the journal for first: what went wrong. Then errors alone, then everything.
const FILTERS = ['important', 'error', 'all'] as const;
type Filter = (typeof FILTERS)[number];
const KEEP: Record<Filter, (level: string) => boolean> = {
  important: (l) => l === 'warn' || l === 'error',
  error: (l) => l === 'error',
  all: () => true,
};
const LABEL: Record<Filter, Key> = { important: 'lv.important', error: 'lv.error', all: 'lv.all' };
// Level → [badge text, tone]. Log lines are raw text and are never translated.
const TAG: Record<string, [string, string]> = { error: ['ERR', 'fail'], warn: ['WARN', 'warn'], info: ['INFO', 'info'], debug: ['DBG', 'mute'] };

const rowText = (r: LogRow) => [r.time || '-', r.level, r.source || '-', r.text].join(' ') + (r.count > 1 ? ' ×' + r.count : '');

export function Journal({ auto, setAuto }: { auto: boolean; setAuto: (v: boolean) => void }) {
  const { t, f, toast, refresh, root } = useApp();
  const res = useRes('logs');
  // Its own key: a choice stored under the old filter does not hide the new default.
  const [filter, setFilterState] = useState<Filter>(() => loadOneOf('logs.filter', FILTERS, 'important'));
  const box = useRef<HTMLOListElement>(null);
  const stick = useRef(true);

  const lines = res.data ? res.data.lines : [];
  const rows = logRows(lines, KEEP[filter]);
  const shown = rows.reduce((n, r) => n + r.count, 0);

  // Follow the tail like a terminal, unless the reader has scrolled up.
  useLayoutEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [res.data, filter]);

  if (!res.data) return res.error ? <ErrorBox t={t} err={res.error} onRetry={refresh} /> : <Skeleton />;

  const copy = async () => {
    const ok = await copyText(rows.map(rowText).join('\n'), root);
    toast(ok ? 'ok' : 'fail', ok ? t('j.copied', { lines: t.n('n.line', shown) }) : t('j.copyFail'));
  };

  return (
    <div class="stack">
      <div class="toolbar">
        <Seg
          label={t('lv.label')}
          value={filter}
          onChange={(v) => {
            stick.current = true;
            setFilterState(v);
            save('logs.filter', v);
          }}
          options={FILTERS.map((v) => ({ value: v, label: t(LABEL[v]) }))}
        />
        <Switch label={t('j.auto')} checked={auto} onChange={setAuto} />
        <span class="row push">
          <span class="hint">{t('x.of', { a: shown, b: lines.length })}</span>
          <Button small icon="copy" disabled={!rows.length} onClick={copy}>
            {t('j.copy')}
          </Button>
        </span>
      </div>
      {rows.length ? (
        <ol
          ref={box}
          class="log"
          tabIndex={0}
          aria-label={t('tab.journal')}
          onScroll={(e) => {
            const el = e.currentTarget as HTMLElement;
            stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          }}
        >
          {rows.map((r, i) => {
            const [tag, tone] = own(TAG, r.level) || ['···', 'mute'];
            return (
              <li key={i + (r.time || '') + r.text} class={'ll t-' + tone}>
                <time dateTime={r.time || undefined} title={r.time || undefined}>
                  {f.clock(r.time, '--:--:--')}
                </time>
                <b title={t.has('lv.' + r.level) ? t(('lv.' + r.level) as Key) : r.level}>{tag}</b>
                <span>{r.source || '—'}</span>
                <span>
                  {r.text}
                  {r.count > 1 ? (
                    <i class="rep" title={t('j.times', { n: r.count })}>
                      ×{r.count}
                    </i>
                  ) : null}
                </span>
              </li>
            );
          })}
        </ol>
      ) : (
        <Empty icon="list">{t(!lines.length ? 'j.empty' : filter === 'important' ? 'j.calm' : 'j.noMatch')}</Empty>
      )}
    </div>
  );
}
