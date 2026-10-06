// The icons' paths live in icons.txt (packed by the build): every name the
// type promises has a path, and nothing else is there.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('icons', () => {
  it('has one path for every icon name, and only those', () => {
    const src = readFileSync(resolve(__dirname, '../src/ui/icons.tsx'), 'utf8');
    const names = Array.from(/export type IconName =([^;]*);/.exec(src)![1].matchAll(/'([a-z]+)'/g), (m) => m[1]);
    const lines = readFileSync(resolve(__dirname, '../src/ui/icons.txt'), 'utf8').trim().split('\n');
    expect(lines.map((l) => l.split(' ')[0])).toEqual(names);
    for (const l of lines) expect(l, l).toMatch(/^[a-z]+ M[0-9MLHVCSQTAZmlhvcsqtaz .,-]+$/);
  });
});
