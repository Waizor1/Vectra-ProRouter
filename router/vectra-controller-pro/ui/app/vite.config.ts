/// <reference types="vitest/config" />
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { deflateRawSync, gzipSync } from 'node:zlib';
import preact from '@preact/preset-vite';
import { defineConfig, type Plugin } from 'vite';

const here = dirname(fileURLToPath(import.meta.url));
const pkg = JSON.parse(readFileSync(resolve(here, 'package.json'), 'utf8')) as { version: string };

// Where the router package picks the files up (router/vectra-controller-pro/openwrt/files/www).
const WWW = resolve(here, '../../openwrt/files/www');
const OUT_DIR = resolve(WWW, 'luci-static/vectra');
const BUNDLE = 'vectra-app.js';
const VIEW = resolve(WWW, 'luci-static/resources/view/vectra/app.js');
// Two views ship in one bundle (simple + Pro), with a sentence in ru/en/zh for
// every router state the owner can meet; since 0.5.0 also the setup wizard,
// "My sites" and a QR encoder; since then the Wi-Fi step for both bands, the
// server step, the tour and Pro in plain words (~165 KB, ~80 KB gzip). Still
// under 0.7 % of the xray binary it sits next to on flash; the budget is here
// to catch creep — a dependency pulled in by accident — not to cut copy.
const BUDGET = 180 * 1024;

/**
 * After the bundle is written: report its size, fail over budget, and stamp the
 * LuCI view's cache-buster with the package version + a digest of the bundle,
 * so a browser never runs a stale app against a new controller.
 */
function vectraBundle(): Plugin {
  // Where this build writes: `--outDir` (a size check in a scratch folder) is measured too.
  let outDir = OUT_DIR;
  return {
    name: 'vectra-bundle',
    apply: 'build',
    configResolved(c) {
      outDir = resolve(c.root, c.build.outDir);
    },
    closeBundle() {
      const code = readFileSync(resolve(outDir, BUNDLE));
      const gz = gzipSync(code, { level: 9 }).length;
      const kb = (n: number) => (n / 1024).toFixed(1) + ' KB';
      console.log(`\n  ${BUNDLE}: ${kb(code.length)} min · ${kb(gz)} gzip (budget ${kb(BUDGET)})`);
      if (code.length > BUDGET) throw new Error(`${BUNDLE} is ${kb(code.length)}, over the ${kb(BUDGET)} budget`);
      // Only the package's own bundle stamps the view: a bundle built elsewhere is not the one that ships.
      if (outDir !== OUT_DIR) return;

      const version = pkg.version + '-' + createHash('sha256').update(code).digest('hex').slice(0, 10);
      const view = readFileSync(VIEW, 'utf8');
      const stamped = view.replace(/(var APP_VERSION = ')[^']*(';)/, `$1${version}$2`);
      if (stamped === view && !view.includes(`'${version}'`)) throw new Error('APP_VERSION marker not found in ' + VIEW);
      if (stamped !== view) writeFileSync(VIEW, stamped);
      console.log(`  view stamped: ?v=${version}\n`);
    },
  };
}

/** `ANALYZE=1 npm run build`: approximate minified bytes each module contributes. */
function analyze(): Plugin {
  return {
    name: 'vectra-analyze',
    apply: 'build',
    async generateBundle(_, bundle) {
      const { minifySync } = await import('rolldown/utils');
      for (const file of Object.values(bundle)) {
        if (file.type !== 'chunk') continue;
        const rows = Object.entries(file.modules).map(([id, m]) => {
          const code = m.code ?? '';
          const min = code ? minifySync('m.js', code, { compress: true, mangle: true }).code : '';
          return [Buffer.byteLength(min), id.replace(/^.*\/ui\/app\//, '')] as const;
        });
        rows.sort((a, b) => b[0] - a[0]).forEach(([n, id]) => console.log(String(n).padStart(8), id));
      }
    },
  };
}

/**
 * The bundle's two big text payloads — the CSS and the ru/en/zh string table —
 * ship raw-deflated and are inflated once at startup by src/lib/inflate.ts.
 * uhttpd serves LuCI's files uncompressed, so this is ~24 KB less on flash and
 * on the wire. Dev mode and the tests keep using the plain sources.
 */
function packText(): Plugin {
  const strings = resolve(here, 'src/i18n/strings.ts');
  const unpack = `import { unpack } from ${JSON.stringify(resolve(here, 'src/lib/inflate.ts'))};`;
  const pack = (text: string) => JSON.stringify(deflateRawSync(Buffer.from(text), { level: 9 }).toString('base64'));
  return {
    name: 'vectra-pack-text',
    apply: 'build',
    enforce: 'post',
    async load(id) {
      if (id !== strings) return null;
      const { S } = (await import(pathToFileURL(strings).href)) as { S: Record<string, readonly string[]> };
      const keys = Object.keys(S);
      const table = JSON.stringify([keys, ...[0, 1, 2].map((i) => keys.map((k) => S[k][i]))]);
      return `${unpack}const [K,...C]=JSON.parse(unpack(${pack(table)}));export const S={};K.forEach((k,i)=>{S[k]=C.map((c)=>c[i])});`;
    },
    transform(code, id) {
      if (!id.endsWith('/src/styles/app.css?inline')) return null;
      const m = /export default\s*("(?:[^"\\]|\\.)*")/.exec(code);
      if (!m) this.error('app.css?inline has an unexpected shape');
      return `${unpack}export default unpack(${pack(JSON.parse(m[1]))});`;
    },
  };
}

/**
 * Classic `h()` JSX instead of the automatic runtime: `h('p', null, a, b)` is
 * shorter than `jsx('p', { children: [a, b] })`, and bytes matter on a router.
 * Type checking still uses tsconfig's react-jsx / preact settings.
 */
function classicJsx(): Plugin {
  return {
    name: 'vectra-classic-jsx',
    config: () => ({
      oxc: {
        // Private names, so a local variable called `h` can never shadow the factory.
        jsx: { runtime: 'classic', pragma: '__vxh', pragmaFrag: '__vxFragment' },
        jsxInject: "import { h as __vxh, Fragment as __vxFragment } from 'preact'",
      },
    }),
  };
}

export default defineConfig(({ command }) => ({
  plugins: [
    preact({ devtoolsInProd: false, prerender: { enabled: false } }),
    classicJsx(),
    packText(),
    vectraBundle(),
    ...(process.env.ANALYZE ? [analyze()] : []),
  ],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
    ...(command === 'build' ? { 'process.env.NODE_ENV': JSON.stringify('production') } : {}),
  },
  server: { port: 5178, host: '127.0.0.1' },
  // Dev serves the package's web root, so /luci-static/vectra/{fonts,wordmark}
  // resolve exactly as on the router. The build never copies it (copyPublicDir).
  publicDir: command === 'build' ? false : WWW,
  build: {
    target: 'es2019',
    cssTarget: ['chrome80', 'firefox78', 'safari13'],
    outDir: OUT_DIR,
    emptyOutDir: false,
    copyPublicDir: false,
    reportCompressedSize: false,
    sourcemap: false,
    minify: true,
    cssMinify: true,
    lib: {
      entry: resolve(here, 'src/main.tsx'),
      formats: ['iife'],
      name: 'VectraAppBundle',
      fileName: () => BUNDLE,
    },
  },
  test: {
    environment: 'happy-dom',
    include: ['test/**/*.test.{ts,tsx}'],
    setupFiles: ['test/setup.ts'],
    restoreMocks: true,
  },
}));
