import js from "@eslint/js";
import { readdirSync } from "node:fs";
import { URL } from "node:url";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

// Frontend mirror of internal/modules/architecture_test.go:
// business modules must not import each other or app/.
const businessModules = readdirSync(new URL("./src/modules/", import.meta.url), { withFileTypes: true })
  .filter((entry) => entry.isDirectory())
  .map((entry) => entry.name);

const moduleBoundaryConfigs = businessModules.map((module) => {
  const others = businessModules.filter((id) => id !== module);
  return {
    files: [`src/modules/${module}/**/*.{ts,tsx}`],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: others.flatMap((id) => [
                `**/../${id}`,
                `**/../${id}/**`,
                `**/modules/${id}`,
                `**/modules/${id}/**`,
              ]),
              message: "业务模块不得 import 其他业务模块；由 features/app 组合各模块。",
            },
            {
              group: ["**/app", "**/app/*", "../../app", "../../app/*", "../app", "../app/*"],
              message: "业务模块不得 import app/；由 app/features 组合模块。",
            },
          ],
        },
      ],
    },
  };
});

export default tseslint.config(
  { ignores: ["../internal/webui/dist", "node_modules"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  reactRefresh.configs.vite,
  { files: ["tests/**/*.mjs"], languageOptions: { globals: globals.node } },
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2023, globals: globals.browser },
    rules: { "@typescript-eslint/no-explicit-any": "off" },
  },
  ...moduleBoundaryConfigs
);
