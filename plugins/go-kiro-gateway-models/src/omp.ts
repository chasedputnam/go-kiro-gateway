import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";

import {
  GATEWAY_API_KEY_ENV,
  loadGatewayConfig,
  PROVIDER_ID,
} from "./config.js";
import { discoverModels } from "./discovery.js";

const OMP_DISCOVERY_TIMEOUT_MS = 14_000;

export function registerOmpGateway(pi: ExtensionAPI, timeoutMs = OMP_DISCOVERY_TIMEOUT_MS): void {
  const { apiBaseUrl } = loadGatewayConfig(process.env);

  pi.registerProvider(PROVIDER_ID, {
    baseUrl: apiBaseUrl,
    apiKey: GATEWAY_API_KEY_ENV,
    api: "openai-completions",
    models: [],
    async fetchDynamicModels(apiKey) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(new Error("Gateway model discovery deadline exceeded")), timeoutMs);
      try {
        return await discoverModels({
          apiBaseUrl,
          bearerToken: apiKey ?? "",
          signal: controller.signal,
        });
      } finally {
        clearTimeout(timeout);
      }
    },
  });
}

export default function ompGatewayExtension(pi: ExtensionAPI): void {
  registerOmpGateway(pi);
}
