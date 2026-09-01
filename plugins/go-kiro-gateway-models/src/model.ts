export interface ModelCost {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export interface DiscoveredModel {
  id: string;
  name: string;
  reasoning: boolean;
  input: Array<"text" | "image">;
  cost: ModelCost;
  contextWindow: number;
  maxTokens: number;
}

export const DEFAULT_CONTEXT_WINDOW = 200_000;
export const DEFAULT_MAX_TOKENS = 16_384;
export const DEFAULT_COST: Readonly<ModelCost> = {
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
};

export function defaultModel(id: string): DiscoveredModel {
  return {
    id,
    name: id,
    reasoning: false,
    input: ["text"],
    cost: { ...DEFAULT_COST },
    contextWindow: DEFAULT_CONTEXT_WINDOW,
    maxTokens: DEFAULT_MAX_TOKENS,
  };
}
