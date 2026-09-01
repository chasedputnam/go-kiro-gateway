import { mkdir, rm } from "node:fs/promises";
import { build } from "esbuild";

const root = new URL("../", import.meta.url);
const outdir = new URL("dist/", root);
await rm(outdir, { recursive: true, force: true });
await mkdir(outdir, { recursive: true });

await build({
  entryPoints: [new URL("src/pi.ts", root).pathname, new URL("src/omp.ts", root).pathname],
  outdir: outdir.pathname,
  bundle: true,
  format: "esm",
  platform: "node",
  target: "node22",
  sourcemap: true,
});
