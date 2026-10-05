// Bundles src/worker/main.ts into dist/worker/main.mjs for plain `node`.
//
// Application code and the workspace packages (@vectra/db, @vectra/contracts,
// shipped as TypeScript source) are bundled; every third-party package stays
// external and is loaded from apps/web/node_modules at runtime, exactly the
// copies the web process uses. `server-only` is replaced by an empty module:
// it exists to keep server code out of client bundles, and the worker is
// server code by definition.
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { build } from "esbuild";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8"));
const external = [
  ...Object.keys(pkg.dependencies ?? {}),
  ...Object.keys(pkg.devDependencies ?? {}),
].filter((name) => !name.startsWith("@vectra/") && name !== "server-only");

await build({
  absWorkingDir: root,
  entryPoints: ["src/worker/main.ts"],
  outfile: "dist/worker/main.mjs",
  bundle: true,
  platform: "node",
  format: "esm",
  target: "node22",
  sourcemap: true,
  external,
  logLevel: "info",
  plugins: [
    {
      name: "empty-server-only",
      setup(b) {
        b.onResolve({ filter: /^server-only$/ }, () => ({
          path: "server-only",
          namespace: "empty",
        }));
        b.onLoad({ filter: /.*/, namespace: "empty" }, () => ({
          contents: "",
          loader: "js",
        }));
      },
    },
  ],
});
