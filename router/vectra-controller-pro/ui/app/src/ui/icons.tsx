// Inline stroke icons on a 24px grid. No icon pack: each is one path, a line
// of icons.txt ("name path"; ports: a way in from outside to a box at home).
// The build packs that file with the CSS and the strings.

import DATA from './icons.txt?raw';

export type IconName =
  | 'ok' | 'warn' | 'fail' | 'unknown' | 'info' | 'refresh' | 'sun' | 'moon' | 'system' | 'expand'
  | 'shrink' | 'pin' | 'copy' | 'arrow' | 'globe' | 'close' | 'plus' | 'gauge' | 'split'
  | 'server' | 'list' | 'filter' | 'wifi' | 'lock' | 'shield' | 'power' | 'sliders' | 'ports'
  | 'cube' | 'pad' | 'play' | 'down' | 'home' | 'cam' | 'mic' | 'search' | 'games' | 'steam' | 'epic'
  | 'riot' | 'battlenet' | 'starcitizen';

// No prototype: an icon the catalog names `constructor` is just one we lack.
const PATHS: Record<string, string> = Object.create(null);
for (const line of DATA.trim().split('\n')) PATHS[line.slice(0, line.indexOf(' ')) as IconName] = line.slice(line.indexOf(' ') + 1);

/** `name` may be any key of the catalog's icon set: one this UI lacks draws the globe. */
export function Icon({ name, size = 16, class: cls }: { name: IconName | (string & {}); size?: number; class?: string }) {
  return (
    <svg
      class={cls ? 'ic ' + cls : 'ic'}
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={PATHS[name] || PATHS.globe} />
    </svg>
  );
}
