// The router owner's switch for support access (contract: "Support shell"): the
// panel's support may run commands on the router, as root, only while it is on.
// Opening it is confirmed — it hands root to someone else; closing it is not.

import { useApp } from '../app/ctx';
import { Switch } from '../ui/kit';

/** `on`: what the router says now (status.remoteShell). */
export function SupportAccess({ on }: { on: boolean }) {
  const { t, run, toast, pending } = useApp();
  const change = async (next: boolean) => {
    const done = await run('set_remote_shell', { on: next }, {
      key: 'remote_shell',
      confirm: next ? { title: t('sa.onQ'), body: t('sa.onQ.d'), ok: t('sa.onBtn') } : undefined,
      // One code for both ways: the words say which way it went.
      quiet: true,
    });
    if (done) toast('ok', t(next ? 'sa.on.done' : 'sa.off.done'));
  };
  return (
    <div class="sv-sa">
      <Switch label={t('sa.t')} checked={on} disabled={!!pending} describedBy="vx-sa-d" onChange={(next) => void change(next)} />
      <p class="hint" id="vx-sa-d">
        {t('sa.d')}
      </p>
    </div>
  );
}
