import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { promisify } from "node:util";
import { describe, it } from "node:test";

const execFileAsync = promisify(execFile);
const packageRoot = new URL("../../", import.meta.url);

interface PackFile {
  path: string;
}

interface PackResult {
  files: PackFile[];
}

describe("published package", () => {
  it("contains only the manifest and bundled native entries", async () => {
    await execFileAsync(process.execPath, ["./scripts/build.mjs"], { cwd: packageRoot });
    const { stdout } = await execFileAsync("npm", ["pack", "--dry-run", "--json", "--ignore-scripts"], {
      cwd: packageRoot,
    });
    const results = JSON.parse(stdout) as PackResult[];
    assert.equal(results.length, 1);
    assert.deepEqual(
      results[0]?.files.map((file) => file.path).sort(),
      ["dist/omp.js", "dist/omp.js.map", "dist/pi.js", "dist/pi.js.map", "package.json"],
    );
  });

  it("declares independent native entry points that load without host runtime imports", async () => {
    const manifest = JSON.parse(await readFile(new URL("package.json", packageRoot), "utf8")) as {
      pi: { extensions: string[] };
      omp: { extensions: string[] };
    };
    assert.deepEqual(manifest.pi.extensions, ["./dist/pi.js"]);
    assert.deepEqual(manifest.omp.extensions, ["./dist/omp.js"]);

    process.env.GO_KIRO_GATEWAY_URL = "https://gateway.example";
    for (const entry of [manifest.pi.extensions[0], manifest.omp.extensions[0]]) {
      assert(entry);
      const module = await import(new URL(entry, packageRoot).href);
      assert.equal(typeof module.default, "function");
      const source = await readFile(new URL(entry, packageRoot), "utf8");
      assert.equal(source.includes("@earendil-works/pi-coding-agent"), false);
      assert.equal(source.includes("@oh-my-pi/pi-coding-agent"), false);
    }
  });
});
