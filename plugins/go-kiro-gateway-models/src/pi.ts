import type { ExtensionAPI, ProviderModelConfig } from "@earendil-works/pi-coding-agent";

import {
  GATEWAY_API_KEY_REFERENCE,
  loadGatewayConfig,
  PROVIDER_ID,
} from "./config.js";
import { discoverModels } from "./discovery.js";

export default function registerPiGateway(pi: ExtensionAPI): void {
  const { apiBaseUrl } = loadGatewayConfig(process.env);

  pi.registerProvider(PROVIDER_ID, {
    name: PROVIDER_ID,
    baseUrl: apiBaseUrl,
    apiKey: GATEWAY_API_KEY_REFERENCE,
    authHeader: true,
    api: "openai-completions",
    models: [],
    async refreshModels(context): Promise<ProviderModelConfig[]> {
      if (!context.allowNetwork) {
        return context.stored?.models.map((model) => ({
          id: model.id,
          name: model.name,
          reasoning: model.reasoning,
          input: [...model.input],
          cost: model.cost,
          contextWindow: model.contextWindow,
          maxTokens: model.maxTokens,
        })) ?? [];
      }

      const bearerToken = context.credential?.type === "api_key" ? context.credential.key ?? "" : "";
      const models = await discoverModels({ apiBaseUrl, bearerToken, signal: context.signal });
      await context.publish({
        persist: {
          checkedAt: Date.now(),
          models: models.map((model) => ({
            ...model,
            api: "openai-completions" as const,
            baseUrl: apiBaseUrl,
            provider: PROVIDER_ID,
          })),
        },
      });
      return models;
    },
  });
}
