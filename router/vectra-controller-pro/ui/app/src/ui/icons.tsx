// Inline stroke icons on a 24px grid. No icon pack: each is one path.

const PATHS = {
  ok: 'M5 12.5l4.5 4.5L19 7.5',
  warn: 'M12 4.2 2.9 19.8h18.2zM12 10v4.2M12 16.9v.2',
  fail: 'M12 3.2a8.8 8.8 0 1 0 0 17.6 8.8 8.8 0 0 0 0-17.6zM9.2 9.2l5.6 5.6M14.8 9.2l-5.6 5.6',
  unknown: 'M12 3.2a8.8 8.8 0 1 0 0 17.6 8.8 8.8 0 0 0 0-17.6zM9.7 9.6a2.4 2.4 0 1 1 3.3 2.2c-.6.3-1 .8-1 1.4v.4M12 16.6v.2',
  info: 'M12 3.2a8.8 8.8 0 1 0 0 17.6 8.8 8.8 0 0 0 0-17.6zM12 11v5.2M12 7.8v.2',
  refresh: 'M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4.5v4h-4',
  sun: 'M12 8.3a3.7 3.7 0 1 0 0 7.4 3.7 3.7 0 0 0 0-7.4zM12 2.8v1.9M12 19.3v1.9M4.9 4.9l1.3 1.3M17.8 17.8l1.3 1.3M2.8 12h1.9M19.3 12h1.9M4.9 19.1l1.3-1.3M17.8 6.2l1.3-1.3',
  moon: 'M19.6 14.6A7.9 7.9 0 0 1 9.4 4.4a7.9 7.9 0 1 0 10.2 10.2z',
  system: 'M3.5 4.8h17v11.2h-17zM8.5 20h7M12 16v4',
  expand: 'M4.5 9.5v-5h5M19.5 9.5v-5h-5M4.5 14.5v5h5M19.5 14.5v5h-5',
  shrink: 'M9.5 4.5v5h-5M14.5 4.5v5h5M9.5 19.5v-5h-5M14.5 19.5v-5h5',
  pin: 'M9.2 3.6h5.6l-.9 5 3.3 3.4H6.8l3.3-3.4zM12 12v8.4',
  copy: 'M9 8.8h10.6v11H9zM15 8.8V4.2H4.4v11H9',
  arrow: 'M5 12h14M13.5 6.5 19 12l-5.5 5.5',
  globe: 'M12 3.2a8.8 8.8 0 1 0 0 17.6 8.8 8.8 0 0 0 0-17.6zM3.2 12h17.6M12 3.2c2.4 2.5 3.6 5.4 3.6 8.8s-1.2 6.3-3.6 8.8c-2.4-2.5-3.6-5.4-3.6-8.8s1.2-6.3 3.6-8.8z',
  close: 'M6.5 6.5l11 11M17.5 6.5l-11 11',
  plus: 'M12 5.5v13M5.5 12h13',
  gauge: 'M4.2 15.5a7.8 7.8 0 1 1 15.6 0M12 15.5l3.6-4.6M8 19.5h8',
  split: 'M4 6.5h5.5L14.5 12H20M14.5 12l-5 5.5H4M17 9l3 3-3 3',
  server: 'M4 4.5h16v6H4zM4 13.5h16v6H4zM7.5 7.5h.2M7.5 16.5h.2',
  list: 'M5 4.5h14v15H5zM8.5 8.5h7M8.5 12h7M8.5 15.5h4.2',
  filter: 'M4 5.5h16l-6.2 7.3v5.4l-3.6 1.8v-7.2z',
  wifi: 'M3.8 9.4a12 12 0 0 1 16.4 0M6.9 12.7a7.5 7.5 0 0 1 10.2 0M10 15.9a3 3 0 0 1 4 0M12 19.1v.1',
  lock: 'M6.2 10.8h11.6v8.9H6.2zM8.6 10.8V8a3.4 3.4 0 0 1 6.8 0v2.8M12 14.3v2.2',
  shield: 'M12 3.4l7.2 2.9v5.2c0 4.3-3 7.8-7.2 9.2-4.2-1.4-7.2-4.9-7.2-9.2V6.3zM9 12l2.2 2.2L15.2 10',
  power: 'M12 3.5v8M7.3 6.4a7.4 7.4 0 1 0 9.4 0',
  sliders: 'M4 7h8M16 7h4M4 17h4M12 17h8M14 4.5v5M10 14.5v5',
} as const;

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 16, class: cls }: { name: IconName; size?: number; class?: string }) {
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
      <path d={PATHS[name]} />
    </svg>
  );
}
