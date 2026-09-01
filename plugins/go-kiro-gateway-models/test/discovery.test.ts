import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { afterEach, describe, it } from "node:test";

import { GatewayDiscoveryError } from "../src/config.js";
import { discoverModels } from "../src/discovery.js";
import { DEFAULT_CONTEXT_WINDOW, DEFAULT_MAX_TOKENS } from "../src/model.js";

const servers: Array<ReturnType<typeof createServer>> = [];

afterEach(async () => {
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

async function serve(handler: (request: IncomingMessage, response: ServerResponse) => void): Promise<string> {
  const server = createServer(handler);
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert(address && typeof address === "object");
  return `http://127.0.0.1:${address.port}/v1`;
}

function assertDiscoveryError(kind: GatewayDiscoveryError["kind"], secret?: string) {
  return (error: unknown): boolean => {
    assert(error instanceof GatewayDiscoveryError);
    assert.equal(error.kind, kind);
    if (secret) assert.equal(error.message.includes(secret), false);
    return true;
  };
}

describe("discoverModels", () => {
  it("sends bearer auth and normalizes a forward-compatible catalog", async () => {
    const token = "test-secret-token";
    const baseUrl = await serve((request, response) => {
      assert.equal(request.method, "GET");
      assert.equal(request.url, "/v1/models");
      assert.equal(request.headers.authorization, `Bearer ${token}`);
      assert.equal(request.headers.accept, "application/json");
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({
        object: "list",
        ignored: true,
        data: [
          { id: "z-model", unknown: "ignored" },
          {
            id: "a-model",
            name: "A Model",
            context_window: 100_000,
            max_output_tokens: 8_192,
            capabilities: { reasoning: true, input: ["image", "text", "audio"] },
          },
          { id: "z-model", name: "duplicate loses" },
          { id: "bad metadata", name: "", context_window: -1, max_output_tokens: 1.5, capabilities: { input: [] } },
          { id: " leading" },
          { object: "model" },
        ],
      }));
    });

    const models = await discoverModels({ apiBaseUrl: baseUrl, bearerToken: token, signal: AbortSignal.timeout(1_000) });
    assert.deepEqual(models.map((model) => model.id), ["a-model", "bad metadata", "z-model"]);
    assert.deepEqual(models[0], {
      id: "a-model",
      name: "A Model",
      reasoning: true,
      input: ["image", "text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 100_000,
      maxTokens: 8_192,
    });
    assert.equal(models[1]?.name, "bad metadata");
    assert.equal(models[1]?.contextWindow, DEFAULT_CONTEXT_WINDOW);
    assert.equal(models[1]?.maxTokens, DEFAULT_MAX_TOKENS);
    assert.equal(models[1]?.reasoning, false);
    assert.deepEqual(models[1]?.input, ["text"]);
  });

  it("requires a resolved credential before network access", async () => {
    await assert.rejects(
      discoverModels({ apiBaseUrl: "http://127.0.0.1:1/v1", bearerToken: "", signal: AbortSignal.timeout(100) }),
      assertDiscoveryError("configuration"),
    );
  });

  for (const [status, kind] of [[401, "authentication"], [403, "authentication"], [404, "not-found"], [503, "http"]] as const) {
    it(`classifies HTTP ${status} as ${kind}`, async () => {
      const secret = "never-in-error";
      const baseUrl = await serve((_request, response) => {
        response.statusCode = status;
        response.end(`echo ${secret}`);
      });
      await assert.rejects(
        discoverModels({ apiBaseUrl: baseUrl, bearerToken: secret, signal: AbortSignal.timeout(1_000) }),
        assertDiscoveryError(kind, secret),
      );
    });
  }

  it("rejects malformed JSON without exposing the response body", async () => {
    const secret = "body-secret";
    const baseUrl = await serve((_request, response) => response.end(`{${secret}`));
    await assert.rejects(
      discoverModels({ apiBaseUrl: baseUrl, bearerToken: "token", signal: AbortSignal.timeout(1_000) }),
      assertDiscoveryError("malformed-response", secret),
    );
  });

  for (const payload of [{ object: "list" }, { data: "not-an-array" }, { data: [] }, { data: [{ id: "\n" }, null] }]) {
    it(`rejects unusable payload ${JSON.stringify(payload)}`, async () => {
      const baseUrl = await serve((_request, response) => response.end(JSON.stringify(payload)));
      await assert.rejects(
        discoverModels({ apiBaseUrl: baseUrl, bearerToken: "token", signal: AbortSignal.timeout(1_000) }),
        assertDiscoveryError(Array.isArray((payload as { data?: unknown }).data) ? "empty-catalog" : "malformed-response"),
      );
    });
  }

  it("classifies caller deadline cancellation as timeout and aborts the request", async () => {
    let closed = false;
    const baseUrl = await serve((request) => {
      request.on("close", () => { closed = true; });
    });
    await assert.rejects(
      discoverModels({ apiBaseUrl: baseUrl, bearerToken: "token", signal: AbortSignal.timeout(20) }),
      assertDiscoveryError("timeout"),
    );
    await new Promise((resolve) => setTimeout(resolve, 10));
    assert.equal(closed, true);
  });

  it("classifies connection failure as network", async () => {
    await assert.rejects(
      discoverModels({ apiBaseUrl: "http://127.0.0.1:1/v1", bearerToken: "token", signal: AbortSignal.timeout(1_000) }),
      assertDiscoveryError("network"),
    );
  });
});
