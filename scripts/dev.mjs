#!/usr/bin/env node
import { access, mkdtemp, rm } from "node:fs/promises";
import { readdirSync, readFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { isIP } from "node:net";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const defaultListen = "127.0.0.1:8080";
const defaultWebHost = "127.0.0.1";
const defaultWebPort = 5173;
const shutdownGraceMs = 15_000;

export const DEV_HELP = `Usage: ./scripts/dev.sh [dev options] [workbench options]

Start a freshly compiled Go backend and the Vite development server.
Go source files are not watched; run this script again after Go changes.

Dev options:
  --web-port PORT   Vite port (default: ${defaultWebPort}; fixed, no port drift)
  --web-host HOST   Vite bind host (default: ${defaultWebHost})
  --dev-url URL     Browser-facing origin used for Vite HMR through a proxy
  -h, --help        Show this help without starting services

All other options are passed to ./workbench, including -listen, -auth,
-allowed-host, -public-url and -data. Both -flag value and --flag=value work.
`;

function portNumber(value, name) {
  if (!/^\d+$/.test(String(value))) throw new Error(`${name} must be a port from 1 to 65535`);
  const port = Number(value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`${name} must be a port from 1 to 65535`);
  }
  return port;
}

function browserOrigin(value, optionName) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`${optionName} must be an http(s) origin, for example https://dev.example.com`);
  }
  if (!["http:", "https:"].includes(url.protocol) || !url.hostname || url.username || url.password ||
      (url.pathname !== "" && url.pathname !== "/") || url.search || url.hash) {
    throw new Error(`${optionName} must be an http(s) origin without a path, credentials, query or fragment`);
  }
  return url.origin;
}

export function parseDevArgs(argv, env = process.env) {
  const options = {
    webHost: env.WORKBENCH_DEV_HOST || defaultWebHost,
    webPort: portNumber(env.WORKBENCH_DEV_PORT || defaultWebPort, "--web-port"),
    devUrl: "",
    goArgs: [],
    help: false,
  };
  if (!options.webHost.trim()) throw new Error("--web-host cannot be empty");

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--") {
      options.goArgs.push(...argv.slice(i));
      break;
    }
    if (["-h", "--help", "-help"].includes(arg)) {
      options.help = true;
      continue;
    }

    const match = /^--?(web-port|web-host|dev-url)(?:=(.*))?$/.exec(arg);
    if (!match) {
      options.goArgs.push(arg);
      continue;
    }

    let value = match[2];
    if (value === undefined) {
      if (i + 1 >= argv.length) throw new Error(`${arg} requires a value`);
      value = argv[i + 1];
      i += 1;
    }
    if (match[1] === "web-port") options.webPort = portNumber(value, "--web-port");
    if (match[1] === "web-host") {
      if (!value.trim()) throw new Error("--web-host cannot be empty");
      options.webHost = value;
    }
    if (match[1] === "dev-url") options.devUrl = browserOrigin(value, "--dev-url");
  }
  return options;
}

function parseGoFlags(args) {
  const values = new Map();
  const allowedHosts = [];
  const allowedIndexes = [];
  const valueFlags = new Set([
    "listen", "data", "futu-config-dir", "futu-runtime-dir", "futu-opend-binary",
    "futu-opend-address", "auth", "public-url", "log-level", "allowed-host",
  ]);
  const boolFlags = new Set(["futu-allow-non-local", "version", "healthcheck"]);
  const boolValues = new Map();
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg === "--" || !arg.startsWith("-") || arg === "-") break;
    const match = /^--?([^=]+)(?:=(.*))?$/.exec(arg);
    if (!match) continue;
    const name = match[1];
    if (!valueFlags.has(name) && !boolFlags.has(name)) continue;

    let value = match[2];
    const indexes = [i];
    if (valueFlags.has(name) && value === undefined && i + 1 < args.length) {
      value = args[i + 1];
      indexes.push(i + 1);
      i += 1;
    }
    if (name === "allowed-host") {
      allowedHosts.push({ value, indexes });
      allowedIndexes.push(...indexes);
    } else if (boolFlags.has(name)) {
      if (name === "version" || name === "healthcheck") {
        const normalized = value === undefined ? true : /^(1|t|T|true|TRUE|True)$/.test(value);
        const validFalse = value !== undefined && /^(0|f|F|false|FALSE|False)$/.test(value);
        boolValues.set(name, normalized || (!validFalse && value !== undefined));
      }
    } else if (value !== undefined) {
      values.set(name, value);
    }
  }
  const oneShot = boolValues.get("version") ? "version" : boolValues.get("healthcheck") ? "healthcheck" : "";
  return { values, allowedHosts, allowedIndexes, oneShot };
}

function splitListenAddress(address) {
  if (typeof address !== "string") return null;
  let host;
  let port;
  if (address.startsWith("[")) {
    const end = address.indexOf("]");
    if (end < 0 || address[end + 1] !== ":") return null;
    host = address.slice(1, end);
    port = address.slice(end + 2);
  } else {
    const split = address.lastIndexOf(":");
    if (split < 0) return null;
    host = address.slice(0, split);
    port = address.slice(split + 1);
    if (host.includes(":")) return null;
  }
  if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) return null;
  return { host, port: Number(port) };
}

function hostOnly(value) {
  const trimmed = String(value || "").trim();
  if (!trimmed) return "";
  if (trimmed.includes("://")) {
    try { return new URL(trimmed).hostname.replace(/^\[|\]$/g, "").toLowerCase(); } catch { return ""; }
  }
  if (trimmed.startsWith("[")) {
    const end = trimmed.indexOf("]");
    if (end > 0) return trimmed.slice(1, end).toLowerCase();
  }
  const lastColon = trimmed.lastIndexOf(":");
  if (lastColon > 0 && /^\d+$/.test(trimmed.slice(lastColon + 1)) && !trimmed.slice(0, lastColon).includes(":")) {
    return trimmed.slice(0, lastColon).toLowerCase();
  }
  return trimmed.toLowerCase();
}

function formatHostPort(host, port) {
  const normalized = host || "127.0.0.1";
  return `${normalized.includes(":") ? `[${normalized}]` : normalized}:${port}`;
}

function parseEnvHosts(env) {
  return String(env.WORKBENCH_ALLOWED_HOSTS || "")
    .split(",")
    .map((host) => host.trim())
    .filter(Boolean);
}

function unique(values) {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))];
}

export function makeDevPlan(options, env = process.env, root = repoRoot) {
  const flags = parseGoFlags(options.goArgs);
  const value = (name, key, fallback) => flags.values.has(name) ? flags.values.get(name) : (env[key] || fallback);
  const listen = value("listen", "WORKBENCH_LISTEN", defaultListen);
  const auth = value("auth", "WORKBENCH_AUTH", "local");
  const dataPath = value("data", "WORKBENCH_DATA", join(homedir(), ".workbench", "data.db"));
  const publicUrlRaw = value("public-url", "WORKBENCH_PUBLIC_URL", "");
  let publicUrl = "";
  if (publicUrlRaw) {
    try {
      const parsedPublicUrl = new URL(publicUrlRaw);
      if (parsedPublicUrl.hostname) publicUrl = parsedPublicUrl.origin;
    } catch {
      // Let the Go application report invalid -public-url values.
    }
  }
  const parsedBackend = splitListenAddress(listen);
  if (flags.oneShot === "healthcheck" && !parsedBackend) {
    throw new Error("-listen must use a fixed host:port with a port from 1 to 65535 for -healthcheck");
  }
  if (!flags.oneShot && !parsedBackend) {
    throw new Error("-listen must use a fixed host:port with a port from 1 to 65535 so Vite can proxy to it");
  }
  const backend = parsedBackend || splitListenAddress(defaultListen);
  const backendPort = backend.port;
  if (flags.oneShot !== "version" && backend.host && !/^[a-zA-Z0-9._:-]+$/.test(backend.host)) {
    throw new Error("-listen host cannot be used as a Vite proxy target");
  }
  const backendHost = !backend.host || backend.host === "0.0.0.0" ? "127.0.0.1" : backend.host === "::" ? "::1" : backend.host;
  const backendUrl = `http://${formatHostPort(backendHost, backendPort)}`;

  let baseAllowedHosts;
  const explicitAllowed = flags.allowedHosts.length > 0;
  const explicitInvalid = flags.allowedHosts.some(({ value: host }) => !host?.trim());
  if (explicitAllowed) {
    baseAllowedHosts = flags.allowedHosts.map(({ value: host }) => host?.trim() || "").filter(Boolean);
  } else {
    baseAllowedHosts = parseEnvHosts(env);
  }

  const webAddressHosts = [
    `localhost:${options.webPort}`,
    `127.0.0.1:${options.webPort}`,
    `${formatHostPort(options.webHost, options.webPort)}`,
  ];
  const healthHosts = [
    `localhost:${backendPort}`,
    `127.0.0.1:${backendPort}`,
    `[::1]:${backendPort}`,
    formatHostPort(backend.host, backendPort),
  ];
  const originHosts = [];
  for (const origin of [options.devUrl, publicUrl]) {
    if (!origin) continue;
    const url = new URL(origin);
    originHosts.push(url.host);
  }
  const generatedAllowedHosts = [...webAddressHosts, ...healthHosts, ...originHosts];
  const allowedHosts = flags.oneShot ? baseAllowedHosts : [...baseAllowedHosts];
  if (!flags.oneShot) {
    for (const host of generatedAllowedHosts) {
      if (!allowedHosts.some((existing) => existing.toLowerCase() === host.toLowerCase())) allowedHosts.push(host);
    }
  }
  const viteHosts = unique([
    ...baseAllowedHosts.map(hostOnly),
    ...originHosts.map(hostOnly),
    hostOnly(options.webHost),
  ]).filter((host) => !isIP(host));

  const goArgs = flags.oneShot
    ? options.goArgs
    : options.goArgs.filter((_, index) => !(explicitAllowed && !explicitInvalid && flags.allowedIndexes.includes(index)));
  const childEnv = { ...env };
  if (!flags.oneShot) {
    if (!explicitInvalid) childEnv.WORKBENCH_ALLOWED_HOSTS = allowedHosts.join(",");
    childEnv.WORKBENCH_DEV_CHART_DIR = resolve(root, "internal/modules/investment/chart");
    childEnv.WORKBENCH_DEV_BACKEND_URL = backendUrl;
    childEnv.WORKBENCH_DEV_PORT = String(options.webPort);
    childEnv.WORKBENCH_DEV_HOST = options.webHost;
    childEnv.WORKBENCH_DEV_HOSTS = viteHosts.join(",");
    childEnv.WORKBENCH_DEV_PUBLIC_URL = options.devUrl || publicUrl;
  }

  const externalHosts = unique([...baseAllowedHosts, ...originHosts].map(hostOnly))
    .filter((host) => host && host !== "localhost" && host !== "127.0.0.1" && host !== "::1" && !isIP(host));
  const displayWebHost = options.webHost === "0.0.0.0" || options.webHost === "::" ? "localhost" : options.webHost;

  return {
    options: { ...options, goArgs },
    env: childEnv,
    listen,
    auth,
    dataPath,
    publicUrl,
    backendUrl,
    webUrl: `http://${formatHostPort(displayWebHost, options.webPort)}`,
    externalHosts,
    oneShot: flags.oneShot,
  };
}

function delay(ms) {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, ms));
}

function groupHasLiveProcesses(pgid) {
  if (process.platform === "linux") {
    try {
      const entries = readdirSync("/proc");
      let found = false;
      for (const entry of entries) {
        if (!/^\d+$/.test(entry)) continue;
        let stat;
        try { stat = readFileSync(`/proc/${entry}/stat`, "utf8"); } catch { continue; }
        const close = stat.lastIndexOf(")");
        if (close < 0) continue;
        const fields = stat.slice(close + 1).trim().split(/\s+/);
        if (Number(fields[2]) !== pgid) continue;
        found = true;
        if (fields[0] !== "Z" && fields[0] !== "X") return true;
      }
      if (found) return false;
    } catch {
      // Fall through to kill(0) when procfs is unavailable.
    }
  }
  try {
    process.kill(-pgid, 0);
    return true;
  } catch (error) {
    return error.code !== "ESRCH";
  }
}

function signalGroup(pgid, signal) {
  if (!pgid || process.platform === "win32") return;
  try { process.kill(-pgid, signal); } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}

export async function terminateProcessGroup(pgid, graceMs = shutdownGraceMs) {
  if (!pgid || process.platform === "win32") return;
  signalGroup(pgid, "SIGTERM");
  const deadline = Date.now() + graceMs;
  while (Date.now() < deadline && groupHasLiveProcesses(pgid)) await delay(50);
  if (!groupHasLiveProcesses(pgid)) return;

  signalGroup(pgid, "SIGKILL");
  const killDeadline = Date.now() + 2_000;
  while (Date.now() < killDeadline && groupHasLiveProcesses(pgid)) await delay(50);
}

function startManagedProcess(spec, onSpawn) {
  let resolveStarted;
  let resolveDone;
  let settledStarted = false;
  const started = new Promise((resolveStartedPromise) => { resolveStarted = resolveStartedPromise; });
  const done = new Promise((resolveDonePromise) => { resolveDone = resolveDonePromise; });
  let spawnError;
  const child = spawn(spec.command, spec.args || [], {
    cwd: spec.cwd,
    env: spec.env,
    stdio: "inherit",
    detached: true,
  });
  child.once("spawn", () => {
    settledStarted = true;
    onSpawn(child.pid);
    resolveStarted({ pid: child.pid });
  });
  child.once("error", (error) => {
    spawnError = error;
    if (!settledStarted) {
      settledStarted = true;
      resolveStarted({ error });
    }
  });
  child.once("close", (code, signal) => {
    if (!settledStarted) {
      settledStarted = true;
      resolveStarted({ error: spawnError || new Error("process failed to start") });
    }
    resolveDone({ code, signal, error: spawnError });
  });
  return { child, started, done };
}

export async function supervise({ build, services, graceMs = shutdownGraceMs, log = () => {} }) {
  if (process.platform === "win32") {
    log("[dev] This launcher requires POSIX process groups; run it from Linux or macOS.");
    return 1;
  }

  const groups = new Set();
  const terminating = new Map();
  let requestedStop;
  let resolveStop;
  const stopRequested = new Promise((resolveStopPromise) => { resolveStop = resolveStopPromise; });
  let stopTask;
  let exitCode = 0;

  const stopRecord = (pgid) => {
    if (!pgid) return Promise.resolve();
    if (!terminating.has(pgid)) {
      const task = terminateProcessGroup(pgid, graceMs).finally(() => groups.delete(pgid));
      terminating.set(pgid, task);
    }
    return terminating.get(pgid);
  };
  const stopAll = () => {
    if (!stopTask) {
      stopTask = (async () => {
        while (true) {
          const pending = [...groups].filter((pgid) => !terminating.has(pgid));
          if (pending.length === 0) break;
          await Promise.all(pending.map(stopRecord));
        }
        await Promise.all([...terminating.values()]);
      })();
    }
    return stopTask;
  };
  const requestStop = (reason, code) => {
    if (!requestedStop) {
      requestedStop = reason;
      exitCode = code;
      resolveStop(reason);
    }
    void stopAll();
  };
  const onSignal = (signal) => {
    const code = signal === "SIGINT" ? 130 : signal === "SIGHUP" ? 129 : 143;
    log(`[dev] Received ${signal}; stopping services...`);
    requestStop({ kind: "signal", signal }, code);
  };
  const onSigint = () => onSignal("SIGINT");
  const onSigterm = () => onSignal("SIGTERM");
  const onSighup = () => onSignal("SIGHUP");
  process.on("SIGINT", onSigint);
  process.on("SIGTERM", onSigterm);
  process.on("SIGHUP", onSighup);

  const launch = (spec) => startManagedProcess(spec, (pid) => {
    if (pid) groups.add(pid);
    if (requestedStop) void stopRecord(pid);
  });

  try {
    const buildProcess = launch(build);
    const buildStarted = await buildProcess.started;
    if (buildStarted.error) {
      log(`[dev] Could not start Go build: ${buildStarted.error.message}`);
      requestStop({ kind: "build-error" }, 1);
      await buildProcess.done;
      await stopAll();
      return exitCode;
    }
    const buildResult = await Promise.race([
      buildProcess.done.then((result) => ({ kind: "build", result })),
      stopRequested.then((reason) => ({ kind: "stop", reason })),
    ]);
    if (buildResult.kind === "stop") {
      await buildProcess.done;
      await stopAll();
      return exitCode;
    }
    await stopRecord(buildStarted.pid);
    if (requestedStop) {
      await stopAll();
      return exitCode;
    }
    if (buildResult.result.code !== 0) {
      const status = buildResult.result.code ?? 1;
      log(`[dev] Go build failed${buildResult.result.signal ? ` (${buildResult.result.signal})` : ` (exit ${status})`}.`);
      requestStop({ kind: "build-exit" }, status || 1);
      await stopAll();
      return exitCode;
    }

    const running = [];
    for (const spec of services) {
      if (requestedStop) break;
      log(`[dev] Starting ${spec.name}...`);
      const service = launch(spec);
      running.push({ name: spec.name, ...service });
      const started = await service.started;
      if (started.error) {
        log(`[dev] Could not start ${spec.name}: ${started.error.message}`);
        requestStop({ kind: "service-start-error", name: spec.name }, 1);
        break;
      }
      if (spec.oneShot) {
        const outcome = await Promise.race([
          service.done.then((result) => ({ kind: "done", result })),
          stopRequested.then((reason) => ({ kind: "stop", reason })),
        ]);
        if (outcome.kind === "stop") {
          await service.done;
          await stopAll();
          return exitCode;
        }
        await stopRecord(started.pid);
        await stopAll();
        if (outcome.result.error) {
          log(`[dev] ${spec.name} failed: ${outcome.result.error.message}`);
          return 1;
        }
        return outcome.result.code ?? 1;
      }
      service.done.then((result) => {
        if (requestedStop) return;
        const detail = result.error ? result.error.message : result.signal ? `signal ${result.signal}` : `exit ${result.code}`;
        log(`[dev] ${spec.name} stopped unexpectedly (${detail}); stopping remaining services.`);
        requestStop({ kind: "service-exit", name: spec.name }, result.code && result.code > 0 ? result.code : 1);
      });
    }

    if (!requestedStop) {
      await Promise.race([
        stopRequested,
        ...running.map((service) => service.done),
      ]);
      if (!requestedStop) requestStop({ kind: "service-exit" }, 1);
    }
    await stopAll();
    await Promise.all(running.map((service) => service.done));
    await stopAll();
    return exitCode;
  } finally {
    process.off("SIGINT", onSigint);
    process.off("SIGTERM", onSigterm);
    process.off("SIGHUP", onSighup);
  }
}

async function main(argv = process.argv.slice(2), env = process.env) {
  let options;
  try {
    options = parseDevArgs(argv, env);
  } catch (error) {
    console.error(`[dev] ${error.message}`);
    console.error("[dev] Run ./scripts/dev.sh --help for usage.");
    return 2;
  }
  if (options.help) {
    console.log(DEV_HELP);
    return 0;
  }

  let plan;
  try {
    plan = makeDevPlan(options, env);
  } catch (error) {
    console.error(`[dev] ${error.message}`);
    return 2;
  }
  const webDir = join(repoRoot, "web");
  const viteEntry = join(webDir, "node_modules", "vite", "bin", "vite.js");
  if (!plan.oneShot) {
    try {
      await access(viteEntry);
    } catch {
      console.error("[dev] Front-end dependencies are missing. Install them with: npm ci --prefix web");
      return 1;
    }
  }

  if (process.platform === "win32") {
    console.error("[dev] This launcher requires POSIX process groups; run it from Linux or macOS.");
    return 1;
  }

  let temporaryDirectory;
  try {
    temporaryDirectory = await mkdtemp(join(env.TMPDIR || "/tmp", "workbench-dev-"));
    const backendBinary = join(temporaryDirectory, "workbench");
    if (plan.oneShot) {
      console.log(`[dev] Compiling Go backend with -tags dev for one-shot -${plan.oneShot} command...`);
    } else {
      console.log("[dev] Compiling Go backend with -tags dev...");
      console.log(`[dev] Go API: ${plan.backendUrl} (auth: ${plan.auth})`);
      console.log(`[dev] Data: ${plan.dataPath}`);
      console.log(`[dev] Vite: ${plan.webUrl}`);
      if (options.devUrl || plan.publicUrl) console.log(`[dev] Browser origin: ${options.devUrl || plan.publicUrl}`);
      for (const host of plan.externalHosts) {
        console.log(`[dev] Reverse proxy for ${host} should target ${plan.webUrl} and forward WebSocket traffic.`);
      }
      console.log("[dev] Press Ctrl+C to stop both services and their child processes.");
      console.log("[dev] Go source files are not watched; rerun this script after Go changes.");
    }

    return await supervise({
      build: {
        command: env.GO_BIN || "go",
        args: ["build", "-tags", "dev", "-o", backendBinary, "./cmd/workbench"],
        cwd: repoRoot,
        env: { ...plan.env, WORKBENCH_DEV_CHART_DIR: resolve(repoRoot, "internal/modules/investment/chart") },
      },
      services: [
        {
          name: "Go backend",
          command: backendBinary,
          args: plan.options.goArgs,
          cwd: repoRoot,
          env: plan.env,
          oneShot: Boolean(plan.oneShot),
        },
        ...(plan.oneShot ? [] : [{
          name: "Vite",
          command: process.execPath,
          args: [viteEntry, "--host", options.webHost, "--port", String(options.webPort), "--strictPort"],
          cwd: webDir,
          env: plan.env,
        }]),
      ],
      log: (message) => console.log(message),
    });
  } catch (error) {
    console.error(`[dev] ${error.message}`);
    return 1;
  } finally {
    if (temporaryDirectory) await rm(temporaryDirectory, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const code = await main();
  process.exitCode = code;
}
