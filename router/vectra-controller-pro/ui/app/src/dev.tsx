// `npm run dev`: the app mounted into a LuCI-like page with the mock transport.
//   ?scenario=healthy|reserve|degraded|down|empty   ?lang=ru|en|zh   ?frame=0 (no LuCI chrome)
//   ?locked=1 (the operator's lock: simple view only)
//   ?wifiDown=radio1 (a radio that does not come back after a Wi-Fi change)   ?wifiEnd=unverified|failed
//   ?scenario=off&holder=passwall2|agent|direct (Vectra switched off: who carries the traffic)
//   ?pwfail=refused|denied|offline (LuCI's password change fails so)
//   ?wifi=manual|overlap|down (the Wi-Fi as an owner may have it: the wizard's verdicts)
//   ?scenario=cgnat (port forwarding behind the provider's CGNAT)   ?pfPending=1   ?pffail=port_conflict|dest_not_lan|…
import { createMock } from './mock/transport';
import { SCENARIOS, type Scenario } from './mock/scenarios';
import { mount } from './mount';

const q = new URLSearchParams(location.search);
const scenario = (SCENARIOS as readonly string[]).indexOf(q.get('scenario') ?? '') >= 0 ? (q.get('scenario') as Scenario) : 'healthy';
const lang = q.get('lang');
if (lang) document.documentElement.lang = lang;
if (q.get('frame') === '0') document.body.classList.add('bare');

const bar = document.getElementById('dev-scenarios');
if (bar) {
  for (const s of SCENARIOS) {
    const a = document.createElement('a');
    const next = new URLSearchParams(q);
    next.set('scenario', s);
    a.href = '?' + next.toString();
    a.textContent = s;
    if (s === scenario) a.setAttribute('aria-current', 'page');
    bar.appendChild(a);
  }
}

const wifiEnd = q.get('wifiEnd');
const holder = q.get('holder');
const pw = q.get('pwfail');
const wifi = q.get('wifi');
const mock = createMock({
  scenario,
  latencyMs: Number(q.get('latency') ?? 300),
  locked: q.get('locked') === '1',
  wifiDown: q.get('wifiDown')?.split(',').filter(Boolean),
  wifiEnd: wifiEnd === 'unverified' || wifiEnd === 'failed' ? wifiEnd : undefined,
  holder: holder === 'agent' || holder === 'direct' ? holder : undefined,
  passwordFails: pw === 'refused' || pw === 'denied' || pw === 'offline' ? pw : undefined,
  wifi: wifi === 'manual' || wifi === 'overlap' || wifi === 'down' ? wifi : undefined,
  pfPending: q.get('pfPending') === '1',
  pfFail: q.get('pffail') || undefined,
});
const host = document.getElementById('vectra-host') as HTMLElement;
const unmount = mount(host, { call: mock.call, setPassword: mock.setPassword, lang: document.documentElement.lang });

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    unmount();
    mock.dispose();
  });
}
