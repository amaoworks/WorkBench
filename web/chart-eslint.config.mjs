import js from '@eslint/js';
import globals from 'globals';

// The embedded terminal is served by Go, so it is outside Vite's source tree.
export default [js.configs.recommended, {
  languageOptions: { ecmaVersion: 2023, sourceType: 'module', globals: globals.browser },
  rules: { 'no-unused-vars': ['error', { args: 'none', caughtErrors: 'none' }] }
}];
