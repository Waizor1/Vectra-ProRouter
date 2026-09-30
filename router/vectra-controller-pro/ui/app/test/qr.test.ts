import { createHash } from 'node:crypto';
import jsQR from 'jsqr';
import { describe, expect, it } from 'vitest';
import { qrMatrix, qrPath } from '../src/lib/qr';

type Level = 'L' | 'M' | 'Q' | 'H';
const LEVELS: Level[] = ['L', 'M', 'Q', 'H'];

// ISO/IEC 18004 Table 7: bytes a symbol holds in byte mode, versions 1–25.
const CAPACITY: Record<Level, number[]> = {
  L: [17, 32, 53, 78, 106, 134, 154, 192, 230, 271, 321, 367, 425, 458, 520, 586, 644, 718, 792, 858, 929, 1003, 1091, 1171, 1273],
  M: [14, 26, 42, 62, 84, 106, 122, 152, 180, 213, 251, 287, 331, 362, 412, 450, 504, 560, 624, 666, 711, 779, 857, 911, 997],
  Q: [11, 20, 32, 46, 60, 74, 86, 108, 130, 151, 177, 203, 241, 258, 292, 322, 364, 394, 442, 482, 509, 565, 611, 661, 715],
  H: [7, 14, 24, 34, 44, 58, 64, 84, 98, 119, 137, 155, 177, 194, 220, 250, 280, 310, 338, 382, 403, 439, 461, 511, 535],
};

const PRINTABLE = Array.from({ length: 95 }, (_, i) => String.fromCharCode(32 + i)).join('');
const B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
const DEEP_LINK = 'https://t.me/SomeBot?start=rt_7KQ4M9XD';
const side = (v: number) => 21 + 4 * (v - 1);

/** Deterministic text: `len` characters from `alphabet`. */
function filler(len: number, alphabet = PRINTABLE, seed = 1): string {
  let x = seed;
  let s = '';
  while (s.length < len) {
    x = (Math.imul(x, 1103515245) + 12345) >>> 0;
    s += alphabet[(x >>> 16) % alphabet.length];
  }
  return s;
}

/** Paints the symbol like a screen would (4 px a module, 4-module quiet zone) and reads it with jsQR. */
function scan(m: boolean[][], px = 4, quiet = 4) {
  const w = (m.length + 2 * quiet) * px;
  const rgba = new Uint8ClampedArray(w * w * 4).fill(255);
  m.forEach((row, r) =>
    row.forEach((dark, c) => {
      if (!dark) return;
      for (let y = 0; y < px; y++)
        for (let x = 0; x < px; x++) {
          const at = (((r + quiet) * px + y) * w + (c + quiet) * px + x) * 4;
          rgba[at] = rgba[at + 1] = rgba[at + 2] = 0;
        }
    }),
  );
  return jsQR(rgba, w, w, { inversionAttempts: 'dontInvert' });
}

/** Encode → pixels → jsQR must give back exactly the text and its UTF-8 bytes. */
function roundTrip(text: string, ecc: Level = 'M'): boolean[][] {
  const m = qrMatrix(text, ecc);
  expect(m.every((row) => row.length === m.length)).toBe(true);
  const code = scan(m);
  expect(code, 'jsQR could not read the symbol').not.toBeNull();
  expect(code?.data).toBe(text);
  expect(code?.binaryData).toEqual(Array.from(new TextEncoder().encode(text)));
  expect(code?.version).toBe((m.length - 17) / 4);
  return m;
}

/** Rows as hex, leftmost module in the high bit, padded to whole nibbles. */
const toHex = (m: boolean[][]) =>
  m.map((row) =>
    (row.map(Number).join('').padEnd(Math.ceil(row.length / 4) * 4, '0').match(/..../g) ?? [])
      .map((nibble) => parseInt(nibble, 2).toString(16))
      .join(''),
  );

/** Remainder of `x` divided by `poly` over GF(2). */
function gf2mod(x: number, poly: number): number {
  const deg = 31 - Math.clz32(poly);
  for (let i = 31 - Math.clz32(x); i >= deg; i--) if ((x >> i) & 1) x ^= poly << (i - deg);
  return x;
}

describe('qrMatrix → pixels → jsQR round trip', () => {
  it('a short ASCII string', () => {
    expect(roundTrip('Vectra').length).toBe(side(1));
  });

  it('a Telegram deep link', () => {
    roundTrip(DEEP_LINK);
  });

  it('a Wi-Fi join string', () => {
    roundTrip('WIFI:T:WPA;S:Vectra-1A2B;P:pa$$w0rd;;');
  });

  it('Cyrillic text, as UTF-8', () => {
    roundTrip('Привет! Роутер Vectra подключён, всё работает.');
  });

  it('3- and 4-byte UTF-8 (CJK, emoji)', () => {
    roundTrip('路由器 ✓ 🚀 Vectra');
  });

  it("the router's claim code: VECTRA:R1: and 330 base64url characters", () => {
    const claim = 'VECTRA:R1:' + filler(330, B64URL, 42);
    expect(claim).toHaveLength(340);
    expect(roundTrip(claim).length).toBe(side(14));
  });

  it('an empty string', () => {
    expect(roundTrip('').length).toBe(side(1));
  });

  it.each(LEVELS)('the deep link at level %s', (level) => {
    roundTrip(DEEP_LINK, level);
  });
});

// jsQR 1.4.0 has version 23's alignment centres as [6, 30, 54, 74, 102] (Annex E: 6, 30, 54, 78,
// 102), so it misreads every version-23 symbol. At M, Q and H the error correction absorbs its
// misread; at L it cannot. L-23 is checked against libqrencode below, and the v23 layout against Annex E.
const jsqrCannotRead = (level: Level, v: number) => level === 'L' && v === 23;

describe('version selection: every version 1–25 at every level', () => {
  const cases = LEVELS.flatMap((level) => CAPACITY[level].map((bytes, i) => [level, i + 1, bytes] as const));
  it.each(cases)('%s-%i holds %i bytes; one more needs the next version', (level, v, bytes) => {
    const text = filler(bytes, PRINTABLE, v);
    expect((jsqrCannotRead(level, v) ? qrMatrix(text, level) : roundTrip(text, level)).length).toBe(side(v));
    const over = filler(bytes + 1, PRINTABLE, v);
    if (v < 25) expect(qrMatrix(over, level).length).toBe(side(v + 1));
    else expect(() => qrMatrix(over, level)).toThrow(`QR: ${bytes + 1} bytes do not fit, version 25-${level} holds ${bytes}`);
  });

  it('counts UTF-8 bytes, not characters', () => {
    expect(qrMatrix('Ж'.repeat(7)).length).toBe(side(1)); // 14 bytes: M-1 is full
    expect(qrMatrix('Ж'.repeat(7) + '!').length).toBe(side(2));
  });

  it('defaults to level M', () => {
    expect(qrMatrix(DEEP_LINK)).toEqual(qrMatrix(DEEP_LINK, 'M'));
  });
});

describe('matches libqrencode 4.1.1 module for module, mask choice included', () => {
  // `qrencode -8 -m 0 -t ASCII -l <level> '<text>'`, rows as hex (see toHex).
  it('the deep link at M (version 3)', () => {
    expect(toHex(qrMatrix(DEEP_LINK, 'M'))).toEqual([
      'fef883f8', '827e9208', 'ba11d2e8', 'baf22ae8', 'ba9f22e8', '82fcb208', 'feaaabf8', '00f97800',
      '8ba747c8', '60428af8', 'c7e78008', '0cf54858', 'a714d410', 'f42705f8', 'aac96e68', '185bd898',
      'c326ec10', 'f8b0c3d8', '23a761a8', '14c4f098', 'dbb55fc8', '00e69888', 'fee10ae8', '82225890',
      'ba8edfd0', 'ba650c08', 'ba517578', '82204958', 'fe978d90',
    ]);
  });

  it('Cyrillic at H (version 2)', () => {
    expect(toHex(qrMatrix('Привет', 'H'))).toEqual([
      'fe723f8', '82f8a08', 'baeb2e8', 'bab92e8', 'ba9b2e8', '82b6208', 'feaabf8', '0058800', '27f85f0',
      'bdeb690', '8b0dba0', '6484c38', '1ebb0e0', '2434590', 'db4f770', '119a460', 'd237fb8', '008c890',
      'feefae0', '82938a0', 'ba1cf80', 'ba78ba0', 'bad2778', '822c060', 'fe1fda8',
    ]);
  });

  it('L-23, which jsQR cannot read (SHA-256 of the rows as 0/1 lines)', () => {
    const m = qrMatrix(filler(CAPACITY.L[22], PRINTABLE, 23), 'L');
    const rows = m.map((row) => row.map(Number).join('')).join('\n');
    expect(createHash('sha256').update(rows).digest('hex')).toBe('e39b011b3806e8c3ff6b497ad9ad40a0bb617e21ad9e350ab91ea434fd9130d0');
  });
});

describe('function patterns, format and version information', () => {
  // ISO/IEC 18004 Annex E: alignment pattern centres.
  const ALIGN: Record<number, number[]> = {
    2: [6, 18],
    7: [6, 22, 38],
    14: [6, 26, 46, 66],
    23: [6, 30, 54, 78, 102],
    25: [6, 32, 58, 84, 110],
  };
  // ISO/IEC 18004 Table D.1: version information bit sequences.
  const VERSION_INFO: Record<number, number> = { 7: 0x07c94, 10: 0x0a4d3, 14: 0x0e60d, 20: 0x149a6, 25: 0x191e1 };
  const FINDER = ['1111111', '1000001', '1011101', '1011101', '1011101', '1000001', '1111111'];
  const bitsOf = (m: boolean[][], r: number, c: number, len: number) => m[r].slice(c, c + len).map(Number).join('');

  it.each([1, 2, 7, 14, 23, 25])('version %i: finders, separators, timing, alignment, dark module', (v) => {
    const m = qrMatrix(filler(CAPACITY.M[v - 1], PRINTABLE, v));
    const n = m.length;
    expect(n).toBe(side(v));
    for (const [r0, c0] of [[0, 0], [0, n - 7], [n - 7, 0]]) {
      expect(FINDER.map((_, i) => bitsOf(m, r0 + i, c0, 7))).toEqual(FINDER);
    }
    // Light separators around the three finders.
    for (let i = 0; i < 8; i++) {
      for (const [r, c] of [[7, i], [i, 7], [7, n - 1 - i], [i, n - 8], [n - 8, i], [n - 1 - i, 7]]) expect(m[r][c]).toBe(false);
    }
    for (let i = 8; i < n - 8; i++) {
      expect(m[6][i]).toBe(i % 2 === 0);
      expect(m[i][6]).toBe(i % 2 === 0);
    }
    const centres = ALIGN[v] ?? [];
    for (const r of centres)
      for (const c of centres) {
        if ((r === 6 && (c === 6 || c === n - 7)) || (r === n - 7 && c === 6)) continue;
        expect([-2, -1, 0, 1, 2].map((i) => bitsOf(m, r + i, c - 2, 5))).toEqual(['11111', '10001', '10101', '10001', '11111']);
      }
    expect(m[n - 8][8]).toBe(true);
  });

  it.each(LEVELS.flatMap((level) => [1, 5, 13, 25].map((v) => [level, v] as const)))(
    '%s-%i: both format copies agree and are a valid BCH(15,5) codeword',
    (level, v) => {
      const m = qrMatrix(filler(CAPACITY[level][v - 1], PRINTABLE, 3 * v), level);
      const n = m.length;
      // Bit i of each copy (ISO/IEC 18004 Figure 25), bit 0 the least significant.
      const first: [number, number][] = [
        [0, 8], [1, 8], [2, 8], [3, 8], [4, 8], [5, 8], [7, 8], [8, 8],
        [8, 7], [8, 5], [8, 4], [8, 3], [8, 2], [8, 1], [8, 0],
      ];
      const second: [number, number][] = [
        ...Array.from({ length: 8 }, (_, i): [number, number] => [8, n - 1 - i]),
        ...Array.from({ length: 7 }, (_, i): [number, number] => [n - 7 + i, 8]),
      ];
      const word = (cells: [number, number][]) => cells.reduce((w, [r, c], i) => w | (Number(m[r][c]) << i), 0);
      expect(word(second)).toBe(word(first));
      const info = word(first) ^ 0x5412;
      expect(info >> 13).toBe({ L: 0b01, M: 0b00, Q: 0b11, H: 0b10 }[level]);
      expect(gf2mod(info, 0x537)).toBe(0);
    },
  );

  it.each([7, 10, 14, 20, 25])('version %i: both version-information blocks hold Table D.1', (v) => {
    const m = qrMatrix(filler(CAPACITY.Q[v - 1], PRINTABLE, v), 'Q');
    const n = m.length;
    let topRight = 0;
    let bottomLeft = 0;
    for (let i = 0; i < 18; i++) {
      const [a, b] = [Math.floor(i / 3), n - 11 + (i % 3)];
      topRight |= Number(m[a][b]) << i;
      bottomLeft |= Number(m[b][a]) << i;
    }
    expect(topRight).toBe(VERSION_INFO[v]);
    expect(bottomLeft).toBe(VERSION_INFO[v]);
  });

  it('versions below 7 carry no version information', () => {
    const m = qrMatrix(filler(CAPACITY.M[5], PRINTABLE, 6)); // version 6
    expect(m.length).toBe(side(6));
    expect(scan(m)?.version).toBe(6);
  });
});

describe('error correction', () => {
  it('reads back through a 6×6 patch of flipped modules at level H', () => {
    const m = qrMatrix(DEEP_LINK, 'H'); // version 5: 4 blocks, 22 check codewords each
    expect(m.length).toBe(side(5));
    const hurt = m.map((row) => row.slice());
    for (let r = 10; r < 16; r++) for (let c = 10; c < 16; c++) hurt[r][c] = !hurt[r][c];
    expect(scan(hurt)?.data).toBe(DEEP_LINK);
  });
});

describe('qrPath', () => {
  it('draws one rectangle per horizontal run of dark modules, inside the quiet zone', () => {
    const m = [
      [true, true, false],
      [false, true, true],
      [true, false, true],
    ];
    expect(qrPath(m, 1)).toEqual({ size: 5, d: 'M1 1h2v1h-2zM2 2h2v1h-2zM1 3h1v1h-1zM3 3h1v1h-1z' });
    expect(qrPath(m, 0).size).toBe(3);
  });

  it('covers exactly the dark modules of a real symbol', () => {
    const m = qrMatrix(DEEP_LINK);
    const { size, d } = qrPath(m);
    expect(size).toBe(m.length + 8);
    const run = /M(\d+) (\d+)h(\d+)v1h-\3z/g;
    expect(d.replace(run, '')).toBe('');
    const painted = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
    for (const [, x, y, w] of d.matchAll(run))
      for (let i = 0; i < Number(w); i++) {
        expect(painted[Number(y)][Number(x) + i]).toBe(false);
        painted[Number(y)][Number(x) + i] = true;
      }
    expect(painted.slice(4, -4).map((row) => row.slice(4, -4))).toEqual(m);
    expect(painted.flat().filter(Boolean)).toHaveLength(m.flat().filter(Boolean).length);
  });
});
