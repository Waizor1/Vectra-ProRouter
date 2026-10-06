// The contract fixtures, imported verbatim. Assigning them to the contract
// types makes `npm run typecheck` fail when a fixture and ../api/types.ts drift.

import action from '../../../contract/action.json';
import balancers from '../../../contract/balancers.json';
import diagnostics from '../../../contract/diagnostics.json';
import entries from '../../../contract/entries.json';
import logs from '../../../contract/logs.json';
import nodes from '../../../contract/nodes.json';
import portForwardsContract from '../../../contract/port_forwards.json';
import rulesContract from '../../../contract/rules.json';
import servicesContract from '../../../contract/services.json';
import setupContract from '../../../contract/setup.json';
import status from '../../../contract/status.json';
import wanCheckContract from '../../../contract/wan_check.json';
import wifiScanContract from '../../../contract/wifi_scan.json';
import type { Action, PortForwards, ReadData, Rules, Services, Setup, WanCheck, WifiRadio, WifiScan } from '../api/types';

// A router that is set up and linked: what every fleet router answers.
/** A radio of an AX3000T as the router reports it; `over` changes what the case needs. */
export const radio = (band: '2g' | '5g', over: Partial<WifiRadio> = {}): WifiRadio => ({
  device: band === '2g' ? 'radio0' : 'radio1',
  band,
  channel: band === '2g' ? 11 : 149,
  auto: false,
  htmode: band === '2g' ? 'HE20' : 'HE80',
  country: 'PA',
  txpower: null,
  maxPower: true,
  ssid: 'Vectra-4E2A',
  secured: true,
  enabled: true,
  width: band === '2g' ? 20 : 80,
  ap: true,
  mesh: false,
  up: true,
  ...over,
});

const setup: Setup = {
  done: true,
  passwordSet: true,
  wan: { proto: 'dhcp', link: true, ipv4: '100.64.12.7', gateway: '100.64.12.1', dns: ['100.64.12.1'] },
  lan: { ipv4: '192.168.1.1' },
  wifi: {
    radios: [radio('2g'), radio('5g')],
    tuned: true,
    tunable: true,
    verdict: 'fine',
    suggested: 'Vectra-4E2A',
    apply: { state: 'ok', at: diagnostics.checkedAt, detail: null, radios: { radio0: true, radio1: true } },
  },
  vectra: { linked: true, botUsername: 'VectraConnectBot', owner: { label: '@vectra_user' }, claim: null },
};
const wanCheck: WanCheck = { link: true, ipv4: '100.64.12.7', dns: true, internet: true, panel: true, checkedAt: diagnostics.checkedAt };
const rules: Rules = rulesContract;
const services: Services = servicesContract;
const portForwards: PortForwards = portForwardsContract;
// A block of flats: channel 1 and 6 crowded, 11 quieter; on 5 GHz the low block is busier.
// What the router heard at its last boost; the mock's air when it listens again.
const wifiScan: WifiScan = {
  radios: [
    {
      device: 'radio0',
      band: '2g',
      current: 11,
      recommended: 11,
      networks: 14,
      channels: [
        { channel: 1, networks: 6, strongest: -48 },
        { channel: 6, networks: 5, strongest: -57 },
        { channel: 11, networks: 3, strongest: -71 },
      ],
      error: null,
    },
    {
      device: 'radio1',
      band: '5g',
      current: 149,
      recommended: 149,
      networks: 4,
      channels: [
        { channel: 36, networks: 3, strongest: -63 },
        { channel: 149, networks: 1, strongest: -80 },
      ],
      error: null,
    },
  ],
  scannedAt: diagnostics.checkedAt,
};

export const FIXTURES: ReadData = { status, balancers, nodes, entries, diagnostics, logs, setup, wan_check: wanCheck, rules, wifi_scan: wifiScan, services, port_forwards: portForwards };

/** The router's own answers for a fresh, unlinked router (contract/), typed: a drift fails the typecheck. */
export const CONTRACT_SETUP: Setup = setupContract;
export const CONTRACT_WAN_CHECK: WanCheck = wanCheckContract;
export const CONTRACT_WIFI_SCAN: WifiScan = wifiScanContract;
export const ACTION_FIXTURE: Action = action;

/** The instant the fixtures describe ("now" on the fixture router). */
export const FIXTURE_NOW = Date.parse(diagnostics.checkedAt);
