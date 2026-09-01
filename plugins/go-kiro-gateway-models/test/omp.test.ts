import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { afterEach, describe, it } from "node:test";

import type { ExtensionAPI, ProviderConfig } from "@oh-my-pi/pi-coding-agent";

import ompExtension, { registerOmpGateway } from "../src/omp.js";

const servers: Server[] = [];
const originalUrl = process.env.GO_KIRO_GATEWAY_URL;

afterEach(async () => {
  if (originalUrl === undefined) delete process.env.GO_KIRO_GATEWAY_URL;
  else process.env.GO_KIRO_GATEWAY_URL = originalUrl;
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

function captureRegistration(timeoutMs = 14_000): { name: string; config: ProviderConfig } {
  let registration: { name: string; config: ProviderConfig } | undefined;
  const api = {
    registerProvider(name: string, config: ProviderConfig) {
      registration = { name, config };
    },
  } as ExtensionAPI;
  registerOmpGateway(api, timeoutMs);
  assert(registration);
  return registration;
}
async function listen(handler: (request: IncomingMessage, response: ServerResponse) => void): Promise<string> {
  const server = createServer(handler);
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert(address && typeof address === "object");
  return `http://127.0.0.1:${address.port}`;
}

describe("OMP extension", () => {
  it("registers an empty dynamic OpenAI-compatible provider", () => {
    process.env.GO_KIRO_GATEWAY_URL = "https://gateway.example/prefix";
    const { name, config } = captureRegistration();
    assert.equal(name, "go-kiro-gateway");
    assert.equal(config.baseUrl, "https://gateway.example/prefix/v1");
    assert.equal(config.apiKey, "GO_KIRO_GATEWAY_API_KEY");
    assert.equal(config.authHeader, undefined);
    assert.equal(config.api, "openai-completions");
    assert.deepEqual(config.models, []);
    assert.equal(typeof config.fetchDynamicModels, "function");
  });

  it("exports the default host factory", () => {
    process.env.GO_KIRO_GATEWAY_URL = "https://gateway.example";
    let called = false;
    ompExtension({ registerProvider() { called = true; } } as unknown as ExtensionAPI);
    assert.equal(called, true);
  });

  it("uses the resolved key and returns authoritative replacements", async () => {
    let catalog: unknown = { data: [{ id: "model-b" }, { id: "model-a" }] };
    process.env.GO_KIRO_GATEWAY_URL = await listen((request, response) => {
      assert.equal(request.headers.authorization, "Bearer omp-secret");
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify(catalog));
    });
    const { config } = captureRegistration();
    assert(config.fetchDynamicModels);
    assert.deepEqual((await config.fetchDynamicModels("omp-secret")).map((model) => model.id), ["model-a", "model-b"]);
    catalog = { data: [{ id: "model-c" }] };
    assert.deepEqual((await config.fetchDynamicModels("omp-secret")).map((model) => model.id), ["model-c"]);
  });

  it("rejects missing credentials before network access", async () => {
    process.env.GO_KIRO_GATEWAY_URL = "http://127.0.0.1:1";
    const { config } = captureRegistration();
    assert(config.fetchDynamicModels);
    await assert.rejects(config.fetchDynamicModels(undefined), /GO_KIRO_GATEWAY_API_KEY/);
  });

  it("aborts the underlying request before the host timeout", async () => {
    let closed = false;
    process.env.GO_KIRO_GATEWAY_URL = await listen((request) => {
      request.on("close", () => { closed = true; });
    });
    const { config } = captureRegistration(20);
    assert(config.fetchDynamicModels);
    await assert.rejects(config.fetchDynamicModels("omp-secret"), /timed out/);
    await new Promise((resolve) => setTimeout(resolve, 10));
    assert.equal(closed, true);
  });

  it("does not convert failed refreshes into an empty catalog", async () => {
    let fail = true;
    process.env.GO_KIRO_GATEWAY_URL = await listen((_request, response) => {
      response.statusCode = fail ? 503 : 200;
      response.end(JSON.stringify({ data: [{ id: "recovered-model" }] }));
    });
    const { config } = captureRegistration();
    assert(config.fetchDynamicModels);
    await assert.rejects(config.fetchDynamicModels("omp-secret"));
    fail = false;
    assert.deepEqual((await config.fetchDynamicModels("omp-secret")).map((model) => model.id), ["recovered-model"]);
  });
});
