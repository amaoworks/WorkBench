import assert from "node:assert/strict";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { ESLint } from "eslint";

test("module import boundaries automatically cover new module directories", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "workbench-module-boundaries-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const id of ["todo", "reading"]) mkdirSync(join(directory, "src/modules", id), { recursive: true });
  const config = join(directory, "eslint.config.mjs");
  copyFileSync(new URL("../eslint.config.js", import.meta.url), config);
  symlinkSync(fileURLToPath(new URL("../node_modules", import.meta.url)), join(directory, "node_modules"), "dir");
  const eslint = new ESLint({ cwd: directory, overrideConfigFile: config });

  for (const [filePath, imported, blocked] of [
    ["src/modules/reading/ReadingPage.tsx", "../todo/queries", true],
    ["src/modules/todo/TodoPage.tsx", "../reading/queries", true],
    ["src/modules/reading/nested/ReadingPage.tsx", "../../todo/queries", true],
    ["src/modules/reading/ReadingPage.tsx", "../../app/navigation", true],
    ["src/modules/reading/ReadingPage.tsx", "./queries", false],
    ["src/modules/reading/ReadingPage.tsx", "../registry", false],
    ["src/modules/reading/ReadingPage.tsx", "../../shared/api", false],
    ["src/modules/reading/ReadingPage.tsx", "../../features/settings/queries", false],
  ]) {
    const [result] = await eslint.lintText(`import { value } from ${JSON.stringify(imported)};\nexport { value };`, { filePath });
    assert.equal(result.messages.length, blocked ? 1 : 0, `${filePath} → ${imported}: ${JSON.stringify(result.messages)}`);
    if (blocked) assert.equal(result.messages[0].ruleId, "no-restricted-imports");
  }
});
