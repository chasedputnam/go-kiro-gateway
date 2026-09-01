import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { afterEach, describe, it } from "node:test";

import type { ExtensionAPI, ProviderConfig } from "@earendil-works/pi-coding-agent";

import piExtension from "../src/pi.js";

const servers: Server[] = [];
const originalUrl = process.env.GO_KIRO_GATEWAY_URL;

afterEach(async () => {
  if (originalUrl === undefined) delete process.env.GO_KIRO_GATEWAY_URL;
  else process.env.GO_KIRO_GATEWAY_URL = originalUrl;
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

async function catalogServer(catalog: () => unknown, status: () => number = () => 200): Promise<string> {
  const server = createServer((request, response) => {
    assert.equal(request.headers.authorization, "Bearer pi-secret");
    response.statusCode = status();
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify(catalog()));
  });
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert(address && typeof address === "object");
  return `http://127.0.0.1:${address.port}`;
}

function captureRegistration(): { name: string; config: ProviderConfig } {
  let registration: { name: string; config: ProviderConfig } | undefined;
  const api = {
    registerProvider(name: string, config: ProviderConfig) {
      registration = { name, config };
    },
  } as ExtensionAPI;
  piExtension(api);
  assert(registration);
  return registration;
}

describe("Pi extension", () => {
  it("registers an empty dynamic OpenAI-compatible provider", () => {
    process.env.GO_KIRO_GATEWAY_URL = "https://gateway.example/prefix/";
    const { name, config } = captureRegistration();
    assert.equal(name, "go-kiro-gateway");
    assert.equal(config.name, "go-kiro-gateway");
    assert.equal(config.baseUrl, "https://gateway.example/prefix/v1");
    assert.equal(config.apiKey, "$GO_KIRO_GATEWAY_API_KEY");
    assert.equal(config.authHeader, true);
    assert.equal(config.api, "openai-completions");
    assert.deepEqual(config.models, []);
    assert.equal(typeof config.refreshModels, "function");
  });

  it("discovers with the host-resolved credential and supplied signal", async () => {
    let catalog: unknown = { data: [{ id: "model-b" }, { id: "model-a" }] };
    process.env.GO_KIRO_GATEWAY_URL = await catalogServer(() => catalog);
    const { config } = captureRegistration();
    assert(config.refreshModels);
    const controller = new AbortController();
    const models = await config.refreshModels({
      credential: { type: "api_key", key: "pi-secret" },
      allowNetwork: true,
      signal: controller.signal,
      publish: async () => true,
    });
    assert.deepEqual(models.map((model) => model.id), ["model-a", "model-b"]);

    catalog = { data: [{ id: "model-c" }] };
    const replacement = await config.refreshModels({
      credential: { type: "api_key", key: "pi-secret" },
      allowNetwork: true,
      signal: controller.signal,
      publish: async () => true,
    });
    assert.deepEqual(replacement.map((model) => model.id), ["model-c"]);
  });

  it("returns the host-stored catalog without network access when offline", async () => {
    process.env.GO_KIRO_GATEWAY_URL = "http://127.0.0.1:1";
    const { config } = captureRegistration();
    assert(config.refreshModels);
    const models = await config.refreshModels({
      credential: { type: "api_key", key: "pi-secret" },
      stored: {
        models: [{
          id: "stored-model",
          name: "Stored Model",
          provider: "go-kiro-gateway",
          api: "openai-completions",
          baseUrl: "http://127.0.0.1:1/v1",
          reasoning: false,
          input: ["text"],
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
          contextWindow: 1000,
          maxTokens: 100,
        }],
      },
      allowNetwork: false,
      signal: new AbortController().signal,
      publish: async () => true,
    });
    assert.deepEqual(models.map((model) => model.id), ["stored-model"]);
  });

  it("rejects failed refreshes rather than publishing an empty replacement", async () => {
    let status = 503;
    process.env.GO_KIRO_GATEWAY_URL = await catalogServer(() => ({ data: [{ id: "ignored" }] }), () => status);
    const { config } = captureRegistration();
    assert(config.refreshModels);
    let publishCalls = 0;
    await assert.rejects(config.refreshModels({
      credential: { type: "api_key", key: "pi-secret" },
      allowNetwork: true,
      signal: new AbortController().signal,
      publish: async () => { publishCalls += 1; return true; },
    }));
    assert.equal(publishCalls, 0);
    status = 200;
    const recovered = await config.refreshModels({
      credential: { type: "api_key", key: "pi-secret" },
      allowNetwork: true,
      signal: new AbortController().signal,
      publish: async () => true,
    });
    assert.deepEqual(recovered.map((model) => model.id), ["ignored"]);
  });
});
