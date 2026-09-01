export const PROVIDER_ID = "go-kiro-gateway";
export const GATEWAY_URL_ENV = "GO_KIRO_GATEWAY_URL";
export const GATEWAY_API_KEY_ENV = "GO_KIRO_GATEWAY_API_KEY";
export const GATEWAY_API_KEY_REFERENCE = `$${GATEWAY_API_KEY_ENV}`;

export type GatewayDiscoveryErrorKind =
  | "configuration"
  | "authentication"
  | "not-found"
  | "http"
  | "timeout"
  | "network"
  | "malformed-response"
  | "empty-catalog";

export class GatewayDiscoveryError extends Error {
  readonly kind: GatewayDiscoveryErrorKind;

  constructor(kind: GatewayDiscoveryErrorKind, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "GatewayDiscoveryError";
    this.kind = kind;
  }
}

export interface GatewayConfig {
  apiBaseUrl: string;
  modelsUrl: string;
}

export function normalizeGatewayUrl(value: string): GatewayConfig {
  if (value.length === 0) {
    throw new GatewayDiscoveryError("configuration", `${GATEWAY_URL_ENV} is required`);
  }

  let url: URL;
  try {
    url = new URL(value);
  } catch (cause) {
    throw new GatewayDiscoveryError(
      "configuration",
      `${GATEWAY_URL_ENV} must be an absolute HTTP(S) URL`,
      { cause },
    );
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new GatewayDiscoveryError("configuration", `${GATEWAY_URL_ENV} must use HTTP or HTTPS`);
  }
  if (url.username.length > 0 || url.password.length > 0) {
    throw new GatewayDiscoveryError("configuration", `${GATEWAY_URL_ENV} must not contain credentials`);
  }
  if (url.search.length > 0 || url.hash.length > 0) {
    throw new GatewayDiscoveryError("configuration", `${GATEWAY_URL_ENV} must not contain a query or fragment`);
  }

  const path = url.pathname.replace(/\/+$/, "");
  url.pathname = path.endsWith("/v1") ? path : `${path}/v1`;
  const apiBaseUrl = url.toString().replace(/\/$/, "");
  return { apiBaseUrl, modelsUrl: `${apiBaseUrl}/models` };
}

export function loadGatewayConfig(env: Readonly<Record<string, string | undefined>>): GatewayConfig {
  return normalizeGatewayUrl(env[GATEWAY_URL_ENV] ?? "");
}
