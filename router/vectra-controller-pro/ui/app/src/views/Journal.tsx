import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import type { LogLine } from '../api/types';
import { useApp, useRes } from '../app/ctx';
import type { Key } from '../i18n';
import { own } from '../lib/own';
import { copyText, loadOneOf, save } from '../lib/storage';
import { Button, Empty, ErrorBox, Seg, Skeleton, Switch } from '../ui/kit';

const LEVELS = ['all', 'error', 'warn', 'info', 'debug'] as const;
type Level = (typeof LEVELS)[number];
// Level → [badge text, tone]. Log lines are raw text and are never translated.
const TAG: Record<string, [string, string]> = { error: ['ERR', 'fail'], warn: ['WARN', 'warn'], info: ['INFO', 'info'], debug: ['DBG', 'mute'] };

const lineText = (l: LogLine) => [l.time || '-', l.level, l.source || '-', l.message].join(' ');

export function Journal({ auto, setAuto }: { auto: boolean; setAuto: (v: boolean) => void }) {
  const { t, f, toast, refresh, root } = useApp();
  const res = useRes('logs');
  const [level, setLevelState] = useState<Level>(() => loadOneOf('logs.level', LEVELS, 'all'));
  const box = useRef<HTMLOListElement>(null);
  const stick = useRef(true);

  const lines = res.data ? res.data.lines : [];
  const shown = level === 'all' ? lines : lines.filter((l) => l.level === level);

  // Follow the tail like a terminal, unless the reader has scrolled up.
  useLayoutEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [res.data, level]);

  if (!res.data) return res.error ? <ErrorBox t={t} err={res.error} onRetry={refresh} /> : <Skeleton />;

  const copy = async () => {
    const ok = await copyText(shown.map(lineText).join('\n'), root);
    toast(ok ? 'ok' : 'fail', ok ? t('j.copied', { lines: t.n('n.line', shown.length) }) : t('j.copyFail'));
  };

  return (
    <div class="stack">
      <div class="toolbar">
        <Seg
          label={t('lv.label')}
          value={level}
          onChange={(v) => {
            stick.current = true;
            setLevelState(v);
            save('logs.level', v);
          }}
          options={LEVELS.map((v) => ({ value: v, label: t((v === 'all' ? 'f.all' : 'lv.' + v) as Key) }))}
        />
        <Switch label={t('j.auto')} checked={auto} onChange={setAuto} />
        <span class="row push">
          <span class="hint">{t('x.of', { a: shown.length, b: lines.length })}</span>
          <Button small icon="copy" disabled={!shown.length} onClick={copy}>
            {t('j.copy')}
          </Button>
        </span>
      </div>
      {shown.length ? (
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
          {shown.map((l, i) => {
            const [tag, tone] = own(TAG, l.level) || ['···', 'mute'];
            return (
              <li key={i + (l.time || '') + l.message} class={'ll t-' + tone}>
                <time dateTime={l.time || undefined} title={l.time || undefined}>
                  {f.clock(l.time, '--:--:--')}
                </time>
                <b title={t.has('lv.' + l.level) ? t(('lv.' + l.level) as Key) : l.level}>{tag}</b>
                <span>{l.source || '—'}</span>
                <span>{l.message}</span>
              </li>
            );
          })}
        </ol>
      ) : (
        <Empty icon="list">{t(lines.length ? 'j.noMatch' : 'j.empty')}</Empty>
      )}
    </div>
  );
}
