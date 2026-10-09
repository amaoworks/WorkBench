// Docker Compose compatible env-file loader for the development launcher.
// Supports KEY=VALUE, comments, blank lines, `export`, single/double quotes,
// multi-line quoted values and ${VAR} interpolation. Values are never echoed in errors.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const keyPattern = /^[A-Za-z_][A-Za-z0-9_.-]*$/;
const secretPattern = /(PASSWORD|SECRET|TOKEN|API_KEY|PRIVATE|CREDENTIAL|DATABASE_URL|DSN)/i;

export function isSecretKey(key) {
  return secretPattern.test(key);
}

export function maskValue(key, value) {
  if (value === undefined) return "(unset)";
  if (isSecretKey(key)) return value === "" ? "(empty)" : "(set, hidden)";
  return value;
}

class EnvFileError extends Error {}

function fail(source, line, message) {
  throw new EnvFileError(`${source}:${line}: ${message}`);
}

function findClosingBrace(text, start) {
  let depth = 1;
  for (let i = start; i < text.length; i += 1) {
    if (text[i] === "$" && text[i + 1] === "{") { depth += 1; i += 1; continue; }
    if (text[i] === "}") { depth -= 1; if (depth === 0) return i; }
  }
  return -1;
}

// Expand $VAR, ${VAR}, ${VAR:-default}, ${VAR-default}, ${VAR:+alt}, ${VAR+alt},
// ${VAR:?message}, ${VAR?message}; `$$` is a literal dollar sign.
export function interpolate(text, lookup, where = "value") {
  let out = "";
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (ch !== "$") { out += ch; continue; }
    const next = text[i + 1];
    if (next === "$") { out += "$"; i += 1; continue; }
    if (next === "{") {
      const end = findClosingBrace(text, i + 2);
      if (end < 0) throw new EnvFileError(`${where}: unterminated \${ in variable reference`);
      const body = text.slice(i + 2, end);
      const match = /^([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-+?])([\s\S]*))?$/.exec(body);
      if (!match) throw new EnvFileError(`${where}: invalid variable reference \${...}`);
      const [, name, op, arg = ""] = match;
      const value = lookup(name);
      const missing = op?.startsWith(":") ? !value : value === undefined;
      if (!op) out += value ?? "";
      else if (op.endsWith("-")) out += missing ? interpolate(arg, lookup, where) : value;
      else if (op.endsWith("+")) out += missing ? "" : interpolate(arg, lookup, where);
      else if (missing) throw new EnvFileError(`${where}: required variable ${name} is not set${arg ? ` (${arg})` : ""}`);
      else out += value;
      i = end;
      continue;
    }
    const simple = /^[A-Za-z_][A-Za-z0-9_]*/.exec(text.slice(i + 1));
    if (simple) {
      out += lookup(simple[0]) ?? "";
      i += simple[0].length;
      continue;
    }
    out += "$";
  }
  return out;
}

const doubleQuoteEscapes = { n: "\n", r: "\r", t: "\t", "\\": "\\", '"': '"', $: "$$" };

// Parse env-file text. Interpolation resolves names like Compose: `lookup` (the shell
// environment) first, then earlier assignments in this file, then `fallback`
// (values from previously loaded files). Returns ordered entries.
export function parseEnvFile(text, { source = "config", lookup = () => undefined, fallback = () => undefined } = {}) {
  const entries = [];
  const assigned = new Map();
  const resolveName = (name) => {
    const fromShell = lookup(name);
    if (fromShell !== undefined) return fromShell;
    return assigned.has(name) ? assigned.get(name) : fallback(name);
  };
  const input = text.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n");
  let pos = 0;
  let line = 1;
  while (pos < input.length) {
    const startLine = line;
    let eol = input.indexOf("\n", pos);
    if (eol < 0) eol = input.length;
    let rawLine = input.slice(pos, eol);
    const trimmed = rawLine.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      pos = eol + 1; line += 1; continue;
    }
    let body = rawLine.replace(/^\s+/, "");
    if (/^export\s+/.test(body)) body = body.replace(/^export\s+/, "");
    const eq = body.indexOf("=");
    if (eq < 0) {
      const bare = body.replace(/\s+#.*$/, "").trim();
      if (!keyPattern.test(bare)) fail(source, startLine, "expected KEY=VALUE");
      // Compose passes a bare KEY through from the shell; the merged environment already does that.
      pos = eol + 1; line += 1; continue;
    }
    const key = body.slice(0, eq).trim();
    if (!keyPattern.test(key)) fail(source, startLine, `invalid variable name before "="`);
    const offset = pos + (rawLine.length - body.length) + eq + 1;
    let cursor = offset;
    while (cursor < input.length && (input[cursor] === " " || input[cursor] === "\t")) cursor += 1;
    const quote = input[cursor];
    let value;
    let after;
    if (quote === "'" || quote === '"') {
      let end = cursor + 1;
      let raw = "";
      for (; end < input.length; end += 1) {
        const ch = input[end];
        if (ch === quote) break;
        if (quote === '"' && ch === "\\" && end + 1 < input.length) {
          const escaped = input[end + 1];
          raw += escaped in doubleQuoteEscapes ? doubleQuoteEscapes[escaped] : `\\${escaped}`;
          if (escaped === "\n") line += 1;
          end += 1;
          continue;
        }
        if (ch === "\n") line += 1;
        // Single-quoted text is literal; protect `$` from interpolation.
        raw += quote === "'" && ch === "$" ? "$$" : ch;
      }
      if (end >= input.length) fail(source, startLine, `unterminated ${quote === '"' ? "double" : "single"} quote for ${key}`);
      after = input.indexOf("\n", end + 1);
      if (after < 0) after = input.length;
      const rest = input.slice(end + 1, after).trim();
      if (rest && !rest.startsWith("#")) fail(source, line, `unexpected text after closing quote for ${key}`);
      value = interpolate(raw, resolveName, `${source}:${startLine}: ${key}`);
    } else {
      after = eol;
      const raw = input.slice(cursor, eol).replace(/(^|\s)#.*$/, "").trim();
      value = interpolate(raw, resolveName, `${source}:${startLine}: ${key}`);
    }
    assigned.set(key, value);
    entries.push({ key, value, line: startLine });
    pos = after + 1;
    line += 1;
  }
  return entries;
}

// Load config files in order (later files override earlier ones) and merge them under
// the shell environment: shell variables, even empty ones, win over file values.
export function loadConfigEnv(files, env = process.env, { cwd = process.cwd(), readFile = readFileSync } = {}) {
  const fileValues = new Map();
  const sources = new Map();
  for (const file of files) {
    const path = resolve(cwd, file);
    let text;
    try {
      text = readFile(path, "utf8");
    } catch (error) {
      const reason = error.code === "ENOENT" ? "file not found" : error.code === "EACCES" ? "permission denied" : error.code || error.message;
      throw new Error(`cannot read config file ${path}: ${reason}`);
    }
    let entries;
    try {
      entries = parseEnvFile(text, {
        source: path,
        lookup: (name) => (Object.hasOwn(env, name) ? env[name] : undefined),
        fallback: (name) => fileValues.get(name),
      });
    } catch (error) {
      if (error instanceof EnvFileError) throw new Error(`invalid config file ${error.message}`);
      throw error;
    }
    for (const { key, value } of entries) {
      fileValues.set(key, value);
      sources.set(key, path);
    }
  }
  const merged = { ...env };
  const report = [];
  for (const [key, value] of fileValues) {
    if (Object.hasOwn(env, key)) {
      report.push({ key, source: "shell", value: env[key] });
    } else {
      merged[key] = value;
      report.push({ key, source: sources.get(key), value });
    }
  }
  return { env: merged, report };
}

// Remove -config/--config FILE and --config=FILE from argv, stopping at "--".
export function splitConfigArgs(argv) {
  const configFiles = [];
  const rest = [];
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--") { rest.push(...argv.slice(i)); break; }
    const match = /^--?config(?:=(.*))?$/.exec(arg);
    if (!match) { rest.push(arg); continue; }
    let value = match[1];
    if (value === undefined) {
      if (i + 1 >= argv.length) throw new Error(`${arg} requires a file path`);
      value = argv[i + 1];
      i += 1;
    }
    if (!value.trim()) throw new Error(`${arg} requires a file path`);
    configFiles.push(value);
  }
  return { configFiles, rest };
}
