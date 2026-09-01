import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { describe, it } from "node:test";
import ts from "typescript";

const sourceRoot = new URL("../../src/", import.meta.url);
const catalogName = /(?:model.*ids?|catalog|allowlist)/iu;
const knownModelId = /(?:claude|gpt|gemini|nova|mistral|deepseek)[-_.:/][a-z0-9]/iu;

describe("production model-catalog invariant", () => {
  it("contains no maintained model catalog", async () => {
    const violations: string[] = [];
    for (const file of await readdir(sourceRoot)) {
      if (!file.endsWith(".ts")) continue;
      const text = await readFile(new URL(file, sourceRoot), "utf8");
      const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);

      function visit(node: ts.Node): void {
        if (
          ts.isPropertyAssignment(node) &&
          node.name.getText(source) === "models" &&
          ts.isArrayLiteralExpression(node.initializer) &&
          node.initializer.elements.length !== 0
        ) {
          violations.push(`${file}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1} static models array must remain empty`);
        }
        if (
          ts.isVariableDeclaration(node) &&
          ts.isIdentifier(node.name) &&
          catalogName.test(node.name.text) &&
          node.initializer &&
          (ts.isArrayLiteralExpression(node.initializer) || ts.isObjectLiteralExpression(node.initializer))
        ) {
          violations.push(`${file}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1} catalog-shaped declaration`);
        }
        if (ts.isStringLiteralLike(node) && knownModelId.test(node.text)) {
          violations.push(`${file}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1} model-id literal`);
        }
        ts.forEachChild(node, visit);
      }

      visit(source);
    }
    assert.deepEqual(violations, []);
  });
});
