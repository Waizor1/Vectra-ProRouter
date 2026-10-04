// Restart the VPN and turn Vectra off: one row of two real buttons, the same in
// both views — side by side on a wide screen, stacked full width on a phone.
// Turning off is the quiet one: an outline in grey, never a call to action.

import { useApp } from '../app/ctx';
import { Button } from '../ui/kit';

/** A missing handler leaves its button out; `primary`: the restart is the verdict's own fix. */
export function PowerActs({ restart, off, primary }: { restart?: () => void; off?: () => void; primary?: boolean }) {
  const { t, pending } = useApp();
  if (!restart && !off) return null;
  return (
    <div class="acts">
      {restart ? (
        <Button kind={primary ? 'p' : 'o'} icon="refresh" busy={pending === 'restart'} disabled={!!pending} onClick={restart}>
          {t('s.act.restart')}
        </Button>
      ) : null}
      {off ? (
        <Button kind="o" class="b-off" icon="power" busy={pending === 'power:off'} disabled={!!pending} onClick={off}>
          {t('s.pw.offBtn')}
        </Button>
      ) : null}
    </div>
  );
}
