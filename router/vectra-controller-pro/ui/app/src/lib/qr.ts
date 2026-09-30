// A QR Code encoder (ISO/IEC 18004) for the UI's join and claim codes: byte mode
// with UTF-8, versions 1–25, all four error-correction levels, no dependencies.
//
// text → UTF-8 bytes → bit stream (mode, byte count, bytes, terminator, padding)
// → data codewords split into blocks, each with Reed–Solomon check codewords →
// blocks interleaved → bits laid out around the function patterns → the mask
// (of 8) with the lowest penalty score is kept.

// ISO/IEC 18004 Table 9 for versions 1–25 (index v − 1), one row per level L, M, Q, H.
// Check (error-correction) codewords in each block:
const EC_PER_BLOCK = [
  [7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26],
  [10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28],
  [13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30],
  [17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30],
];
// Number of blocks. The version's codewords are shared out evenly; when they do not
// divide, the last (total mod blocks) blocks carry one data codeword more.
const BLOCKS = [
  [1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12],
  [1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21],
  [1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29],
  [1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35],
];

// Data masks (ISO/IEC 18004 Table 10), i = row, j = column: a data module is flipped where true.
const MASKS: ((i: number, j: number) => boolean)[] = [
  (i, j) => (i + j) % 2 === 0,
  (i) => i % 2 === 0,
  (_, j) => j % 3 === 0,
  (i, j) => (i + j) % 3 === 0,
  (i, j) => (Math.floor(i / 2) + Math.floor(j / 3)) % 2 === 0,
  (i, j) => ((i * j) % 2) + ((i * j) % 3) === 0,
  (i, j) => (((i * j) % 2) + ((i * j) % 3)) % 2 === 0,
  (i, j) => (((i + j) % 2) + ((i * j) % 3)) % 2 === 0,
];

// GF(256) over the QR field polynomial x⁸ + x⁴ + x³ + x² + 1 (0x11D), as exponent and
// log tables; EXP is stored twice over so a product needs no "mod 255".
const EXP = new Uint8Array(510);
const LOG = new Uint8Array(256);
for (let i = 0, x = 1; i < 255; i++) {
  EXP[i] = EXP[i + 255] = x;
  LOG[x] = i;
  x = x & 0x80 ? (x << 1) ^ 0x11d : x << 1;
}
const mul = (a: number, b: number) => (a && b ? EXP[LOG[a] + LOG[b]] : 0);

/** Reed–Solomon generator of degree `n`, highest power first: (x − α⁰)(x − α¹)…(x − αⁿ⁻¹). */
function rsGenerator(n: number): number[] {
  let g = [1];
  for (let i = 0; i < n; i++) g = [...g, 0].map((c, j) => c ^ (j ? mul(g[j - 1], EXP[i]) : 0));
  return g;
}

/** Check codewords: the remainder of data·xⁿ divided by the generator (shift-register division). */
function rsRemainder(data: number[], gen: number[]): number[] {
  const r: number[] = new Array(gen.length - 1).fill(0);
  for (const byte of data) {
    const f = byte ^ (r.shift() as number);
    r.push(0);
    for (let j = 0; j < r.length; j++) r[j] ^= mul(gen[j + 1], f);
  }
  return r;
}

/** BCH codeword: `data`, then the remainder of data·x^deg divided by `poly` over GF(2). */
function bch(data: number, poly: number, deg: number): number {
  let r = data << deg;
  for (let i = 17; i >= deg; i--) if ((r >> i) & 1) r ^= poly << (i - deg);
  return (data << deg) | r;
}

/** Codewords a version holds: the modules the function patterns leave free, ÷ 8 (the rest are remainder bits). */
function rawCodewords(v: number): number {
  const n = 17 + 4 * v;
  const k = v > 1 ? Math.floor(v / 7) + 2 : 0; // alignment centres per row/column
  const finders = 3 * 64; // with their separators
  const format = 2 * 15 + 1; // both copies, and the dark module
  const timing = 2 * (n - 16);
  const align = k && 25 * (k * k - 3) - 2 * 5 * (k - 2); // those on row/column 6 share 5 modules with timing
  const version = v > 6 ? 2 * 18 : 0;
  return (n * n - finders - format - timing - align - version) >> 3;
}

/** ISO/IEC 18004 §7.8.3 penalty of a masked symbol (N1 = 3, N2 = 3, N3 = 40, N4 = 10). */
function penalty(m: Uint8Array, n: number): number {
  let score = 0;
  let dark = 0;
  for (let i = 0; i < 2 * n; i++) {
    // Run lengths along row i (then column i − n), starting with a light run (0 when the line
    // starts dark), so even runs are light and odd runs dark.
    const runs = [0];
    for (let j = 0, prev = 0; j < n; j++) {
      const x = i < n ? m[i * n + j] : m[j * n + i - n];
      if (x === prev) runs[runs.length - 1]++;
      else {
        runs.push(1);
        prev = x;
      }
    }
    runs.forEach((run, k) => {
      // N1: a run of five or more modules of one colour scores 3, plus 1 per module past five.
      if (run >= 5) score += run - 2;
      // N3: dark:light:dark:light:dark runs in the ratio 1:1:3:1:1 (a finder look-alike, at any
      // scale u) with a light run of 4u before or after them score 40. Past the edge is quiet zone,
      // so a light run touching the edge (k = 3 before, the last run after) is long enough.
      const u = runs[k - 2];
      if (k % 2 && k > 2 && k + 2 < runs.length && run === 3 * u)
        if (runs[k - 1] === u && runs[k + 1] === u && runs[k + 2] === u)
          if (k === 3 || runs[k - 3] >= 4 * u || k + 4 >= runs.length || runs[k + 3] >= 4 * u) score += 40;
    });
  }
  for (let r = 0; r < n; r++)
    for (let c = 0; c < n; c++) {
      const x = m[r * n + c];
      dark += x;
      // N2: every 2×2 block of one colour scores 3.
      if (r && c && x === m[r * n + c - 1] && x === m[r * n + c - n] && x === m[r * n + c - n - 1]) score += 3;
    }
  // N4: 10 for every full 5 % by which the share of dark modules strays from 50 %. (libqrencode
  // rounds the share to a whole percent first, so near 45 % or 55 % dark it may pick another mask.)
  return score + 10 * Math.floor(Math.abs((dark * 20) / (n * n) - 10));
}

/**
 * The modules of a QR Code holding `text` (UTF-8, byte mode) at error-correction level `ecc`,
 * in the smallest version 1–25 it fits: `true` = dark, rows top to bottom, no quiet zone.
 * Throws when the text is too long for version 25 at that level.
 */
export function qrMatrix(text: string, ecc: 'L' | 'M' | 'Q' | 'H' = 'M'): boolean[][] {
  const e = 'LMQH'.indexOf(ecc);
  const bytes = new TextEncoder().encode(text);

  // The smallest version whose data codewords hold the 4-bit mode, the 8-bit (v1–9)
  // or 16-bit (v10+) byte count, and the bytes.
  let v = 0;
  let raw = 0; // all codewords of the version
  let cap = 0; // its data codewords; the rest are check codewords
  do {
    if (++v > 25) throw new Error(`QR: ${bytes.length} bytes do not fit, version 25-${ecc} holds ${cap - 3}`);
    raw = rawCodewords(v);
    cap = raw - BLOCKS[e][v - 1] * EC_PER_BLOCK[e][v - 1];
  } while (12 + (v > 9 ? 8 : 0) + 8 * bytes.length > 8 * cap);

  const bits: number[] = [];
  const put = (value: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((value >> i) & 1);
  };
  put(0b0100, 4); // byte mode
  put(bytes.length, v > 9 ? 16 : 8);
  bytes.forEach((b) => put(b, 8));
  put(0, Math.min(4, 8 * cap - bits.length)); // terminator, cut short when the symbol is full
  put(0, -bits.length & 7); // to a byte boundary
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) data.push(bits.slice(i, i + 8).reduce((a, b) => (a << 1) | b));
  for (let i = 0; data.length < cap; i++) data.push(i % 2 ? 0x11 : 0xec); // pad codewords

  // Blocks with their check codewords, then interleaved: the i-th data codeword of every
  // block in turn, then the i-th check codeword of every block.
  const nb = BLOCKS[e][v - 1];
  const ec = EC_PER_BLOCK[e][v - 1];
  const short = Math.floor(raw / nb) - ec; // data codewords in a short block
  const gen = rsGenerator(ec);
  const blocks: number[][] = [];
  for (let b = 0, at = 0; b < nb; b++) {
    const len = b < nb - (raw % nb) ? short : short + 1;
    blocks.push(data.slice(at, at + len));
    at += len;
  }
  const checks = blocks.map((block) => rsRemainder(block, gen));
  const stream: number[] = [];
  for (let i = 0; i <= short; i++) for (const block of blocks) if (i < block.length) stream.push(block[i]);
  for (let i = 0; i < ec; i++) for (const check of checks) stream.push(check[i]);

  // Function patterns go in first and are marked in `fn`: data and masks leave them alone.
  const n = 17 + 4 * v;
  const m = new Uint8Array(n * n); // 1 = dark
  const fn = new Uint8Array(n * n);
  const set = (r: number, c: number, dark: number | boolean) => {
    if (r >= 0 && r < n && c >= 0 && c < n) {
      m[r * n + c] = dark ? 1 : 0;
      fn[r * n + c] = 1;
    }
  };
  const ring = (r: number, c: number) => Math.max(Math.abs(r), Math.abs(c)); // square ring around a centre

  // Finders with their separators, as rings: dark core (0–1), light (2), dark (3), light separator (4).
  for (const [r, c] of [[3, 3], [3, n - 4], [n - 4, 3]])
    for (let i = -4; i <= 4; i++)
      for (let j = -4; j <= 4; j++) {
        const d = ring(i, j);
        set(r + i, c + j, d !== 2 && d !== 4);
      }

  // Alignment patterns (Annex E): centres at every pair of positions from 6 to n − 7, which are
  // spaced by an even step (only the first gap may be shorter), except the three finder corners.
  const k = v > 1 ? Math.floor(v / 7) + 2 : 0;
  const step = k && Math.ceil((n - 13) / (k - 1) / 2) * 2;
  const pos: number[] = [];
  for (let i = 0; i < k; i++) pos.push(i ? n - 7 - (k - 1 - i) * step : 6);
  for (const r of pos)
    for (const c of pos)
      if (!fn[r * n + c]) for (let i = -2; i <= 2; i++) for (let j = -2; j <= 2; j++) set(r + i, c + j, ring(i, j) !== 1);

  // Timing patterns along row and column 6 (they agree with the alignment patterns they cross).
  for (let i = 8; i < n - 8; i++) {
    set(6, i, ~i & 1);
    set(i, 6, ~i & 1);
  }

  // Format information: level (L 01, M 00, Q 11, H 10, i.e. the index XOR 1) and mask as a
  // BCH(15,5) codeword XOR 0x5412, in two copies. Bit 0 is the least significant.
  const format = (mask: number) => {
    const word = bch(((e ^ 1) << 3) | mask, 0x537, 10) ^ 0x5412;
    for (let i = 0; i < 15; i++) {
      const bit = (word >> i) & 1;
      // Around the top-left finder: bits 0–7 down column 8, 8–14 leftwards along row 8, hopping the timing lines.
      if (i < 8) set(i < 6 ? i : i + 1, 8, bit);
      else set(8, i < 9 ? 7 : 14 - i, bit);
      // Split copy: bits 0–7 along row 8 leftwards from the right edge, 8–14 down column 8 to the bottom.
      if (i < 8) set(8, n - 1 - i, bit);
      else set(n - 15 + i, 8, bit);
    }
  };
  format(0); // reserves the modules; each mask writes its own bits below
  set(n - 8, 8, 1); // the dark module

  // Version information from version 7: a BCH(18,6) codeword in two 6×3 blocks, left of the
  // top-right finder (bit i at row ⌊i/3⌋, column n − 11 + i mod 3) and, transposed, above the bottom-left one.
  if (v > 6) {
    const word = bch(v, 0x1f25, 12);
    for (let i = 0; i < 18; i++) {
      const a = Math.floor(i / 3);
      const b = n - 11 + (i % 3);
      set(a, b, (word >> i) & 1);
      set(b, a, (word >> i) & 1);
    }
  }

  // Codeword bits, most significant first, in 2-module-wide columns zig-zagging up and down from
  // the bottom-right corner (right module first), skipping function modules and the vertical
  // timing column. Modules left over at the end are the remainder bits and stay 0.
  for (let c = n - 1, up = true, i = 0; c > 0; c -= 2, up = !up) {
    if (c === 6) c = 5;
    for (let t = 0; t < n; t++) {
      const r = up ? n - 1 - t : t;
      for (const x of [c, c - 1]) {
        const p = r * n + x;
        if (!fn[p] && i < stream.length * 8) {
          m[p] = (stream[i >> 3] >> (7 - (i & 7))) & 1;
          i++;
        }
      }
    }
  }

  // Every mask, with its format information, is scored; the first lowest score wins.
  const flip = (mask: number) => {
    for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (!fn[r * n + c] && MASKS[mask](r, c)) m[r * n + c] ^= 1;
  };
  let best = m;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    flip(mask);
    format(mask);
    const score = penalty(m, n);
    if (score < bestScore) {
      bestScore = score;
      best = m.slice();
    }
    flip(mask); // a mask is an XOR: applying it again restores the data
  }
  return Array.from({ length: n }, (_, r) => Array.from(best.subarray(r * n, r * n + n), (x) => x === 1));
}

/**
 * The dark modules as one SVG path, a 1-module-high rectangle per horizontal run, offset by a
 * `quiet`-module light margin: render as `<svg viewBox="0 0 {size} {size}"><path d={d}/></svg>`
 * (with shape-rendering="crispEdges", so rows do not show hairline seams).
 */
export function qrPath(m: boolean[][], quiet = 4): { size: number; d: string } {
  let d = '';
  m.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) {
      if (!row[x]) continue;
      const start = x;
      while (row[x + 1]) x++;
      d += `M${start + quiet} ${y + quiet}h${x - start + 1}v1h-${x - start + 1}z`;
    }
  });
  return { size: m.length + 2 * quiet, d };
}
