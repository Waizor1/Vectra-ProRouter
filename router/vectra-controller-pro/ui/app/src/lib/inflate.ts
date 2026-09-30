// A minimal raw-DEFLATE decoder (RFC 1951), after Mark Adler's puff.c.
//
// uhttpd serves LuCI's static files as they are on flash, uncompressed. The
// build therefore stores the two big text payloads of the bundle — the CSS and
// the ru/en/zh string table — deflated, and they are inflated once at startup
// (~50 KB, a few milliseconds). Dev mode and the tests use the plain sources.

type Huff = [counts: Uint16Array, symbols: Uint16Array];

function huff(lengths: ArrayLike<number>): Huff {
  const counts = new Uint16Array(16);
  const offs = new Uint16Array(16);
  const symbols = new Uint16Array(lengths.length);
  for (let i = 0; i < lengths.length; i++) counts[lengths[i]]++;
  counts[0] = 0;
  for (let i = 1; i < 16; i++) offs[i] = offs[i - 1] + counts[i - 1];
  for (let i = 0; i < lengths.length; i++) if (lengths[i]) symbols[offs[lengths[i]]++] = i;
  return [counts, symbols];
}

// Base values and extra bits for length codes 257..285 and distance codes 0..29.
const LBASE: number[] = [];
const LEXT: number[] = [];
const DBASE: number[] = [];
const DEXT: number[] = [];
for (let i = 0, l = 3, d = 1; i < 30; i++) {
  LEXT[i] = i < 8 ? 0 : (i >> 2) - 1;
  LBASE[i] = l;
  l += 1 << LEXT[i];
  DEXT[i] = i < 4 ? 0 : (i >> 1) - 1;
  DBASE[i] = d;
  d += 1 << DEXT[i];
}
LBASE[28] = 258;
LEXT[28] = 0;
const ORDER = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];

export function inflate(src: Uint8Array): Uint8Array {
  let pos = 0;
  let bitBuf = 0;
  let bitCnt = 0;
  let out = new Uint8Array(src.length * 4 + 1024);
  let n = 0;

  const put = (b: number) => {
    if (n === out.length) {
      const grown = new Uint8Array(out.length * 2);
      grown.set(out);
      out = grown;
    }
    out[n++] = b;
  };
  const bits = (need: number) => {
    while (bitCnt < need) {
      if (pos >= src.length) throw new Error('inflate: truncated input');
      bitBuf |= src[pos++] << bitCnt;
      bitCnt += 8;
    }
    const v = bitBuf & ((1 << need) - 1);
    bitBuf >>>= need;
    bitCnt -= need;
    return v;
  };
  const decode = ([counts, symbols]: Huff) => {
    let code = 0;
    let first = 0;
    let index = 0;
    for (let len = 1; len < 16; len++) {
      code |= bits(1);
      const count = counts[len];
      if (code - count < first) return symbols[index + (code - first)];
      index += count;
      first = (first + count) << 1;
      code <<= 1;
    }
    throw new Error('inflate: bad code');
  };

  let last = 0;
  while (!last) {
    last = bits(1);
    const type = bits(2);
    if (type === 0) {
      // Stored block: byte-aligned LEN, NLEN, then raw bytes.
      bitBuf = bitCnt = 0;
      const len = src[pos] | (src[pos + 1] << 8);
      pos += 4;
      for (let i = 0; i < len; i++) put(src[pos++]);
      continue;
    }
    if (type === 3) throw new Error('inflate: bad block type');
    let lit: Huff;
    let dist: Huff;
    if (type === 1) {
      const l = new Uint8Array(288);
      l.fill(8, 0, 144).fill(9, 144, 256).fill(7, 256, 280).fill(8, 280);
      lit = huff(l);
      dist = huff(new Uint8Array(30).fill(5));
    } else {
      const nlen = bits(5) + 257;
      const ndist = bits(5) + 1;
      const ncode = bits(4) + 4;
      const cl = new Uint8Array(19);
      for (let i = 0; i < ncode; i++) cl[ORDER[i]] = bits(3);
      const clHuff = huff(cl);
      const lengths = new Uint8Array(nlen + ndist);
      for (let i = 0; i < nlen + ndist; ) {
        const sym = decode(clHuff);
        if (sym < 16) {
          lengths[i++] = sym;
          continue;
        }
        const prev = sym === 16 ? lengths[i - 1] : 0;
        let rep = sym === 16 ? 3 + bits(2) : sym === 17 ? 3 + bits(3) : 11 + bits(7);
        while (rep--) lengths[i++] = prev;
      }
      lit = huff(lengths.subarray(0, nlen));
      dist = huff(lengths.subarray(nlen));
    }
    for (;;) {
      const sym = decode(lit);
      if (sym < 256) put(sym);
      else if (sym === 256) break;
      else {
        const li = sym - 257;
        const len = LBASE[li] + bits(LEXT[li]);
        const di = decode(dist);
        const back = DBASE[di] + bits(DEXT[di]);
        for (let i = 0; i < len; i++) put(out[n - back]);
      }
    }
  }
  return out.subarray(0, n);
}

/** Base64 of raw-deflated UTF-8 text → the text. */
export function unpack(b64: string): string {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder().decode(inflate(bytes));
}
