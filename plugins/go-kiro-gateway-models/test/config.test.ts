import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { GatewayDiscoveryError, loadGatewayConfig, normalizeGatewayUrl } from "../src/config.js";

describe("normalizeGatewayUrl", () => {
  const cases = [
    ["http://127.0.0.1:8000", "http://127.0.0.1:8000/v1"],
    ["http://127.0.0.1:8000/", "http://127.0.0.1:8000/v1"],
    ["https://gateway.example/v1", "https://gateway.example/v1"],
    ["https://gateway.example/v1/", "https://gateway.example/v1"],
    ["https://gateway.example/team/kiro", "https://gateway.example/team/kiro/v1"],
  ] as const;

  for (const [input, expected] of cases) {
    it(`normalizes ${input}`, () => {
      assert.equal(normalizeGatewayUrl(input).apiBaseUrl, expected);
      assert.equal(normalizeGatewayUrl(input).modelsUrl, `${expected}/models`);
    });
  }

  for (const input of [
    "",
    "gateway.example",
    "ftp://gateway.example",
    "https://user:password@gateway.example",
    "https://gateway.example/v1?token=secret",
    "https://gateway.example/v1#secret",
  ]) {
    it(`rejects unsafe URL ${JSON.stringify(input)}`, () => {
      assert.throws(
        () => normalizeGatewayUrl(input),
        (error: unknown) => error instanceof GatewayDiscoveryError && error.kind === "configuration",
      );
    });
  }
});

describe("loadGatewayConfig", () => {
  it("requires GO_KIRO_GATEWAY_URL", () => {
    assert.throws(
      () => loadGatewayConfig({}),
      (error: unknown) =>
        error instanceof GatewayDiscoveryError &&
        error.kind === "configuration" &&
        error.message.includes("GO_KIRO_GATEWAY_URL"),
    );
  });

  it("does not include embedded credentials in errors", () => {
    const secret = "do-not-print";
    assert.throws(
      () => loadGatewayConfig({ GO_KIRO_GATEWAY_URL: `https://user:${secret}@gateway.example` }),
      (error: unknown) => error instanceof Error && !error.message.includes(secret),
    );
  });
});
