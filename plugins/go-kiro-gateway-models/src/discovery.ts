import { GatewayDiscoveryError } from "./config.js";
import { defaultModel, type DiscoveredModel } from "./model.js";

export interface DiscoveryDiagnostic {
  index: number;
  reason: string;
}

export interface DiscoveryRequest {
  apiBaseUrl: string;
  bearerToken: string;
  signal: AbortSignal;
  fetchImpl?: typeof fetch;
  onDiagnostic?: (diagnostic: DiscoveryDiagnostic) => void;
}

interface GatewayCapabilitiesWire {
  reasoning?: unknown;
  input?: unknown;
}

interface GatewayModelWire {
  id?: unknown;
  name?: unknown;
  context_window?: unknown;
  max_output_tokens?: unknown;
  capabilities?: unknown;
}

interface GatewayModelsWire {
  data?: unknown;
}

function validModelId(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value.trim() === value &&
    !/[\u0000-\u001f\u007f]/u.test(value)
  );
}

function projectModel(row: GatewayModelWire & { id: string }): DiscoveredModel {
  const model = defaultModel(row.id);

  if (typeof row.name === "string" && row.name.length > 0 && row.name.trim() === row.name) {
    model.name = row.name;
  }
  if (typeof row.context_window === "number" && Number.isSafeInteger(row.context_window) && row.context_window > 0) {
    model.contextWindow = row.context_window;
  }
  if (
    typeof row.max_output_tokens === "number" &&
    Number.isSafeInteger(row.max_output_tokens) &&
    row.max_output_tokens > 0
  ) {
    model.maxTokens = row.max_output_tokens;
  }

  if (typeof row.capabilities === "object" && row.capabilities !== null && !Array.isArray(row.capabilities)) {
    const capabilities = row.capabilities as GatewayCapabilitiesWire;
    if (typeof capabilities.reasoning === "boolean") model.reasoning = capabilities.reasoning;
    if (Array.isArray(capabilities.input)) {
      const inputs: Array<"text" | "image"> = [];
      for (const value of capabilities.input) {
        if ((value === "text" || value === "image") && !inputs.includes(value)) inputs.push(value);
      }
      if (inputs.length > 0) model.input = inputs;
    }
  }

  return model;
}


function statusError(status: number, modelsUrl: string): GatewayDiscoveryError {
  if (status === 401 || status === 403) {
    return new GatewayDiscoveryError("authentication", `Gateway authentication failed with HTTP ${status} at ${modelsUrl}`);
  }
  if (status === 404) {
    return new GatewayDiscoveryError("not-found", `Gateway model endpoint was not found at ${modelsUrl}`);
  }
  return new GatewayDiscoveryError("http", `Gateway model discovery failed with HTTP ${status} at ${modelsUrl}`);
}

export async function discoverModels(request: DiscoveryRequest): Promise<DiscoveredModel[]> {
  if (request.bearerToken.length === 0) {
    throw new GatewayDiscoveryError("configuration", "GO_KIRO_GATEWAY_API_KEY is required");
  }

  const modelsUrl = `${request.apiBaseUrl.replace(/\/+$/, "")}/models`;
  let response: Response;
  try {
    response = await (request.fetchImpl ?? fetch)(modelsUrl, {
      method: "GET",
      headers: {
        accept: "application/json",
        authorization: `Bearer ${request.bearerToken}`,
      },
      signal: request.signal,
    });
  } catch (cause) {
    if (request.signal.aborted) {
      throw new GatewayDiscoveryError("timeout", `Gateway model discovery timed out at ${modelsUrl}`, { cause });
    }
    throw new GatewayDiscoveryError("network", `Gateway model discovery could not connect to ${modelsUrl}`, { cause });
  }

  if (!response.ok) throw statusError(response.status, modelsUrl);

  let payload: unknown;
  try {
    payload = await response.json();
  } catch (cause) {
    throw new GatewayDiscoveryError("malformed-response", `Gateway returned invalid JSON from ${modelsUrl}`, { cause });
  }

  const wire = typeof payload === "object" && payload !== null && !Array.isArray(payload) ? payload as GatewayModelsWire : undefined;
  if (!wire || !Array.isArray(wire.data)) {
    throw new GatewayDiscoveryError(
      "malformed-response",
      `Gateway response from ${modelsUrl} must contain a data array`,
    );
  }

  const modelsById = new Map<string, DiscoveredModel>();
  let invalidCount = 0;
  for (const [index, value] of wire.data.entries()) {
    const item = typeof value === "object" && value !== null && !Array.isArray(value) ? value as GatewayModelWire : undefined;
    if (!item || !validModelId(item.id)) {
      invalidCount += 1;
      request.onDiagnostic?.({ index, reason: "model row requires a safe non-empty id" });
      continue;
    }
    if (!modelsById.has(item.id)) modelsById.set(item.id, projectModel(item as GatewayModelWire & { id: string }));
  }

  if (modelsById.size === 0) {
    throw new GatewayDiscoveryError(
      "empty-catalog",
      `Gateway returned no usable models from ${modelsUrl} (${invalidCount} invalid rows)`,
    );
  }

  return [...modelsById.values()].sort((left, right) => (left.id < right.id ? -1 : left.id > right.id ? 1 : 0));
}
