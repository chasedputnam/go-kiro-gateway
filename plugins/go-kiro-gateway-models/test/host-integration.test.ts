import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { after, before, describe, it } from "node:test";
import {
  createAgentSessionFromServices,
  createAgentSessionServices,
  SessionManager,
} from "@earendil-works/pi-coding-agent";

const execFileAsync = promisify(execFile);
const packageRoot = fileURLToPath(new URL("../../", import.meta.url));
const piEntry = fileURLToPath(new URL("../../dist/pi.js", import.meta.url));
const bunCli = fileURLToPath(new URL("../../node_modules/.bin/bun", import.meta.url));
const ompCli = fileURLToPath(new URL("../../node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js", import.meta.url));
const ompEntry = fileURLToPath(new URL("../../dist/omp.js", import.meta.url));

interface OmpModelJson {
  provider: string;
  id: string;
}

interface OmpModelsJson {
  models: OmpModelJson[];
}

let server: Server;
let gatewayUrl: string;
let catalog = ["host-model-a", "host-model-b"];
let discoveryStatus = 200;
let discoveryRequests = 0;
const inferenceModels: string[] = [];
const authorizations: string[] = [];
const temporaryDirectories: string[] = [];

before(async () => {
  server = createServer(async (request, response) => {
    authorizations.push(request.headers.authorization ?? "");
    if (request.url === "/v1/models") {
      discoveryRequests += 1;
      response.statusCode = discoveryStatus;
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({ data: catalog.map((id) => ({ id })) }));
      return;
    }
    assert.equal(request.url, "/v1/chat/completions");
    assert.equal(request.method, "POST");
    const body = await readJsonBody(request);
    if (typeof body.model !== "string") throw new TypeError("Inference request model must be a string");
    inferenceModels.push(body.model);
    writeCompletion(response, body.model);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert(address && typeof address === "object");
  gatewayUrl = `http://127.0.0.1:${address.port}`;
});

after(async () => {
  await new Promise<void>((resolve) => server.close(() => resolve()));
  await Promise.all(temporaryDirectories.map((directory) => rm(directory, { force: true, recursive: true })));
});

async function isolatedAgentDirectory(host: "pi" | "omp"): Promise<string> {
  const directory = await mkdtemp(`${tmpdir()}/go-kiro-gateway-${host}-`);
  temporaryDirectories.push(directory);
  return directory;
}

async function readJsonBody(request: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
  return JSON.parse(Buffer.concat(chunks).toString("utf8")) as Record<string, unknown>;
}

function writeCompletion(response: ServerResponse, model: string): void {
  const base = { id: "chatcmpl-host-test", object: "chat.completion.chunk", created: 1, model };
  response.setHeader("content-type", "text/event-stream");
  response.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { role: "assistant", content: "host-reply" }, finish_reason: null }] })}\n\n`);
  response.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: {}, finish_reason: "stop" }] })}\n\n`);
  response.end("data: [DONE]\n\n");
}

function hostEnvironment(agentDirectory: string): NodeJS.ProcessEnv {
  return {
    ...process.env,
    GO_KIRO_GATEWAY_URL: gatewayUrl,
    GO_KIRO_GATEWAY_API_KEY: "host-secret",
    PI_CODING_AGENT_DIR: agentDirectory,
    PI_TELEMETRY: "0",
    NO_COLOR: "1",
  };
}

async function runOmpModels(agentDirectory: string, args: string[]): Promise<OmpModelsJson> {
  const result = await execFileAsync(bunCli, [ompCli, "models", ...args], {
    cwd: packageRoot,
    env: hostEnvironment(agentDirectory),
    timeout: 30_000,
  });
  return JSON.parse(result.stdout) as OmpModelsJson;
}

async function runOmp(agentDirectory: string, args: string[]): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const child = spawn(bunCli, [ompCli, ...args], {
      cwd: packageRoot,
      env: hostEnvironment(agentDirectory),
      stdio: ["pipe", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    const timeout = setTimeout(() => child.kill("SIGTERM"), 30_000);
    child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk));
    child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
    child.on("error", reject);
    child.on("close", (code, signal) => {
      clearTimeout(timeout);
      if (code === 0) resolve(Buffer.concat(stdout).toString("utf8"));
      else reject(new Error(`OMP exited with ${code ?? signal}: ${Buffer.concat(stderr).toString("utf8")}`));
    });
    child.stdin.end();
  });
}

function ompGatewayModels(result: OmpModelsJson): string[] {
  return result.models
    .filter((model) => model.provider === "go-kiro-gateway")
    .map((model) => model.id)
    .sort();
}

describe("real host integration", () => {
  it("loads the built Pi entry through a real AgentSession and refreshes host-owned models", async () => {
    const agentDirectory = await isolatedAgentDirectory("pi");
    const previousUrl = process.env.GO_KIRO_GATEWAY_URL;
    const previousApiKey = process.env.GO_KIRO_GATEWAY_API_KEY;
    process.env.GO_KIRO_GATEWAY_URL = gatewayUrl;
    process.env.GO_KIRO_GATEWAY_API_KEY = "host-secret";
    catalog = ["host-model-a", "host-model-b"];
    discoveryStatus = 200;

    let session: Awaited<ReturnType<typeof createAgentSessionFromServices>>["session"] | undefined;
    try {
      const services = await createAgentSessionServices({
        cwd: packageRoot,
        agentDir: agentDirectory,
        resourceLoaderOptions: {
          additionalExtensionPaths: [piEntry],
          noSkills: true,
          noPromptTemplates: true,
          noThemes: true,
          noContextFiles: true,
        },
      });
      assert.deepEqual(services.diagnostics, []);
      assert(services.modelRuntime.getRegisteredProviderIds().includes("go-kiro-gateway"));

      await services.modelRuntime.refresh({
        providers: ["go-kiro-gateway"],
        allowNetwork: true,
        force: true,
        signal: AbortSignal.timeout(15_000),
      });
      assert.deepEqual(
        (await services.modelRuntime.getAvailable("go-kiro-gateway")).map((model) => model.id).sort(),
        ["host-model-a", "host-model-b"],
      );

      const initialModel = services.modelRuntime.getModel("go-kiro-gateway", "host-model-a");
      assert(initialModel);
      const created = await createAgentSessionFromServices({
        services,
        sessionManager: SessionManager.inMemory(packageRoot),
        model: initialModel,
        noTools: "all",
      });
      session = created.session;
      assert.equal(session.model?.id, "host-model-a");
      await session.prompt("Return the mock response");
      const assistant = [...session.messages].reverse().find((message) => message.role === "assistant");
      assert(assistant && "content" in assistant && Array.isArray(assistant.content));
      assert.equal(
        assistant.content.some((part) => part.type === "text" && part.text === "host-reply"),
        true,
      );
      assert.equal(inferenceModels.at(-1), "host-model-a");

      discoveryStatus = 503;
      const failedRefresh = await services.modelRuntime.refresh({
        providers: ["go-kiro-gateway"],
        allowNetwork: true,
        force: true,
        signal: AbortSignal.timeout(15_000),
      });
      assert(failedRefresh.errors.has("go-kiro-gateway"));
      assert.deepEqual(
        services.modelRuntime.getModels("go-kiro-gateway").map((model) => model.id).sort(),
        ["host-model-a", "host-model-b"],
      );

      discoveryStatus = 200;

      catalog = ["host-model-c"];
      await services.modelRuntime.refresh({
        providers: ["go-kiro-gateway"],
        allowNetwork: true,
        force: true,
        signal: AbortSignal.timeout(15_000),
      });
      assert.deepEqual(
        (await services.modelRuntime.getAvailable("go-kiro-gateway")).map((model) => model.id),
        ["host-model-c"],
      );
    } finally {
      session?.dispose();
      if (previousUrl === undefined) delete process.env.GO_KIRO_GATEWAY_URL;
      else process.env.GO_KIRO_GATEWAY_URL = previousUrl;
      if (previousApiKey === undefined) delete process.env.GO_KIRO_GATEWAY_API_KEY;
      else process.env.GO_KIRO_GATEWAY_API_KEY = previousApiKey;
    }
  });

  it("loads the built OMP entry and refreshes its host-owned catalog", async () => {
    const agentDirectory = await isolatedAgentDirectory("omp");
    discoveryStatus = 200;
    catalog = ["host-model-a", "host-model-b"];
    const initial = await runOmpModels(agentDirectory, [
      "refresh",
      "--json",
      "--no-extensions",
      "--extension",
      ompEntry,
    ]);
    assert.deepEqual(ompGatewayModels(initial), ["host-model-a", "host-model-b"]);

    const inference = await runOmp(agentDirectory, [
      "--print",
      "--no-session",
      "--no-tools",
      "--no-extensions",
      "--extension",
      ompEntry,
      "--model",
      "go-kiro-gateway/host-model-a",
      "Return the mock response",
    ]);
    assert.match(inference, /host-reply/u);
    assert.equal(inferenceModels.at(-1), "host-model-a");

    discoveryStatus = 503;
    const retained = await runOmpModels(agentDirectory, [
      "refresh",
      "--json",
      "--no-extensions",
      "--extension",
      ompEntry,
    ]);
    assert.deepEqual(ompGatewayModels(retained), ["host-model-a", "host-model-b"]);

    discoveryStatus = 200;
    catalog = ["host-model-c"];
    const replacement = await runOmpModels(agentDirectory, [
      "refresh",
      "--json",
      "--no-extensions",
      "--extension",
      ompEntry,
    ]);
    assert.deepEqual(ompGatewayModels(replacement), ["host-model-c"]);

    discoveryStatus = 503;
    const requestsBeforeRestore = discoveryRequests;
    const restored = await runOmpModels(agentDirectory, [
      "--json",
      "--no-extensions",
      "--extension",
      ompEntry,
    ]);
    assert.deepEqual(ompGatewayModels(restored), ["host-model-c"]);
    assert.equal(discoveryRequests, requestsBeforeRestore);
  });

  it("sends the resolved host credential on every gateway request", () => {
    assert(authorizations.length >= 8);
    assert.deepEqual(new Set(authorizations), new Set(["Bearer host-secret"]));
  });
});
