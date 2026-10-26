import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { makeDevPlan, parseDevArgs, supervise } from "./dev.mjs";
import { loadConfigEnv, maskValue, parseEnvFile, splitConfigArgs } from "./env-file.mjs";

test("dev options and Go flags preserve CLI precedence and separated flag values", () => {
  const options = parseDevArgs([
    "--web-port=5180", "--web-host", "workbench.test", "--dev-url=https://w.vm2.de5.net",
    "--log-level", "debug", "-listen", "127.0.0.1:9090", "--auth=password",
    "--allowed-host", "legacy.example.test", "-data=/tmp/workbench-dev-test.db",
  ], { WORKBENCH_DEV_PORT: "5173" });
  const plan = makeDevPlan(options, {
    WORKBENCH_LISTEN: "127.0.0.1:8080",
    WORKBENCH_AUTH: "local",
    WORKBENCH_DATA: "/tmp/from-env.db",
    WORKBENCH_ALLOWED_HOSTS: "environment.example.test",
  }, "/repo");

  assert.equal(plan.listen, "127.0.0.1:9090");
  assert.equal(plan.auth, "password");
  assert.equal(plan.dataPath, "/tmp/workbench-dev-test.db");
  assert.equal(plan.backendUrl, "http://127.0.0.1:9090");
  assert.equal(plan.env.WORKBENCH_DEV_PORT, "5180");
  assert.equal(plan.env.WORKBENCH_DEV_HOSTS, "legacy.example.test,w.vm2.de5.net,workbench.test");
  assert.equal(plan.env.WORKBENCH_DEV_PUBLIC_URL, "https://w.vm2.de5.net");
  assert.match(plan.env.WORKBENCH_ALLOWED_HOSTS, /localhost:5180/);
  assert.match(plan.env.WORKBENCH_ALLOWED_HOSTS, /127\.0\.0\.1:9090/);
  assert.match(plan.env.WORKBENCH_ALLOWED_HOSTS, /legacy\.example\.test/);
  assert.doesNotMatch(plan.env.WORKBENCH_ALLOWED_HOSTS, /environment\.example\.test/);
  assert.deepEqual(plan.options.goArgs, [
    "--log-level", "debug", "-listen", "127.0.0.1:9090", "--auth=password", "-data=/tmp/workbench-dev-test.db",
  ]);
});

test("allowed hosts are de-duplicated case-insensitively in order", () => {
  const options = parseDevArgs(["--dev-url=https://w.vm2.de5.net"], {});
  const plan = makeDevPlan(options, {
    WORKBENCH_ALLOWED_HOSTS: "W.vm2.de5.net, w.vm2.de5.net,other.example.test,OTHER.example.test",
    WORKBENCH_PUBLIC_URL: "https://w.vm2.de5.net",
  }, "/repo");
  const hosts = plan.env.WORKBENCH_ALLOWED_HOSTS.split(",");
  assert.deepEqual(hosts.slice(0, 2), ["W.vm2.de5.net", "other.example.test"]);
  assert.equal(new Set(hosts.map((host) => host.toLowerCase())).size, hosts.length);
  const devHosts = plan.env.WORKBENCH_DEV_HOSTS.split(",");
  assert.equal(new Set(devHosts.map((host) => host.toLowerCase())).size, devHosts.length);
});

test("help is parsed without requiring services", () => {
  assert.equal(parseDevArgs(["--help", "-listen", "127.0.0.1:8080"]).help, true);
});

test("one-shot Go flags preserve arguments and allowed-host environment", () => {
  const env = { WORKBENCH_ALLOWED_HOSTS: "existing.example.test", WORKBENCH_LISTEN: "127.0.0.1:9090" };
  const options = parseDevArgs(["--version", "-allowed-host", "cli.example.test"]);
  const plan = makeDevPlan(options, env, "/repo");
  assert.equal(plan.oneShot, "version");
  assert.deepEqual(plan.options.goArgs, ["--version", "-allowed-host", "cli.example.test"]);
  assert.equal(plan.env.WORKBENCH_ALLOWED_HOSTS, "existing.example.test");
  assert.equal(plan.env.WORKBENCH_DEV_BACKEND_URL, undefined);

  const healthOptions = parseDevArgs(["--healthcheck", "--listen=127.0.0.1:9091"]);
  const healthPlan = makeDevPlan(healthOptions, { WORKBENCH_ALLOWED_HOSTS: "kept.example.test" }, "/repo");
  assert.equal(healthPlan.oneShot, "healthcheck");
  assert.deepEqual(healthPlan.options.goArgs, ["--healthcheck", "--listen=127.0.0.1:9091"]);
  assert.equal(healthPlan.env.WORKBENCH_ALLOWED_HOSTS, "kept.example.test");
  assert.equal(makeDevPlan(parseDevArgs(["--version=false"]), {}).oneShot, "");
});

test("dev mode rejects backend listen addresses without a fixed proxy port", () => {
  for (const address of [":0", "127.0.0.1:65536", "not-an-address", "bad host:9090"]) {
    assert.throws(
      () => makeDevPlan(parseDevArgs([`--listen=${address}`]), {}, "/repo"),
      /-listen must use a fixed host:port|host cannot be used as a Vite proxy target/,
      address,
    );
  }
  assert.equal(makeDevPlan(parseDevArgs(["--listen=:9090"]), {}, "/repo").backendUrl, "http://127.0.0.1:9090");
});

test("env files follow Docker Compose .env syntax and interpolation", () => {
  const text = [
    "\uFEFF# comment",
    "export A=alpha",
    "B = beta  # inline comment",
    'C="dq ${A} \\n x # kept"',
    "D='sq ${A} $A'",
    "E=${A}-$A-${NOPE:-def}-${NOPE-d2}-${A:+alt}-$$lit",
    "F=x#kept",
    "P=file",
    "G=${P}",
    'H="multi',
    'line"',
    "I=",
    "BARE",
    "J=${UNSET_THING}end",
  ].join("\r\n");
  const values = Object.fromEntries(parseEnvFile(text, { lookup: (name) => ({ P: "shell" })[name] })
    .map(({ key, value }) => [key, value]));
  assert.deepEqual(values, {
    A: "alpha", B: "beta", C: "dq alpha \n x # kept", D: "sq ${A} $A",
    E: "alpha-alpha-def-d2-alt-$lit", F: "x#kept", P: "file", G: "shell", H: "multi\nline", I: "", J: "end",
  });
});

test("env file errors name the line but never the value", () => {
  for (const [text, pattern] of [
    ["SECRET_PASSWORD='hunter2", /x\.env:1: unterminated single quote for SECRET_PASSWORD/],
    ["OK=1\n1BAD=hunter2", /x\.env:2: invalid variable name/],
    ["just some words", /x\.env:1: expected KEY=VALUE/],
    ['K="hunter2" trailing', /unexpected text after closing quote/],
    ["K=${NEEDED:?set it}", /required variable NEEDED is not set/],
  ]) {
    assert.throws(() => parseEnvFile(text, { source: "x.env" }), (error) => {
      assert.match(error.message, pattern);
      assert.doesNotMatch(error.message, /hunter2/);
      return true;
    });
  }
});

test("config precedence is flags, then shell environment, then later files, then earlier files", async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-config-test-"));
  try {
    const base = join(directory, "base.env");
    const local = join(directory, "local.env");
    await writeFile(base, [
      "WORKBENCH_LISTEN=127.0.0.1:8080", "WORKBENCH_AUTH=password", "WORKBENCH_DATA=/data/data.db",
      "POSTGRES_USER=workbench", "POSTGRES_PASSWORD=from-file", "WORKBENCH_DEV_PORT=5190",
    ].join("\n"));
    await writeFile(local, [
      "WORKBENCH_DATA=.workbench/dev/data.db",
      "WORKBENCH_DATABASE_URL=postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:5432/dev",
    ].join("\n"));
    const shell = { POSTGRES_PASSWORD: "from-shell", WORKBENCH_AUTH: "local" };
    const { configFiles, rest } = splitConfigArgs(["--config", base, `--config=${local}`, "-listen", "127.0.0.1:19091", "--", "-config", "x"]);
    assert.deepEqual(configFiles, [base, local]);
    assert.deepEqual(rest, ["-listen", "127.0.0.1:19091", "--", "-config", "x"]);
    const { env, report } = loadConfigEnv(configFiles, shell);
    assert.equal(env.WORKBENCH_AUTH, "local");
    assert.equal(env.POSTGRES_PASSWORD, "from-shell");
    assert.equal(env.WORKBENCH_DATA, ".workbench/dev/data.db");
    assert.equal(env.WORKBENCH_DATABASE_URL, "postgresql://workbench:from-shell@127.0.0.1:5432/dev");
    assert.equal(report.find((entry) => entry.key === "WORKBENCH_AUTH").source, "shell");
    const plan = makeDevPlan(parseDevArgs(rest.slice(0, 2), env), env, "/repo");
    assert.equal(plan.listen, "127.0.0.1:19091");
    assert.equal(plan.webUrl, "http://127.0.0.1:5190");
    assert.equal(maskValue("WORKBENCH_DATABASE_URL", env.WORKBENCH_DATABASE_URL), "(set, hidden)");
    assert.throws(() => loadConfigEnv([join(directory, "missing.env")], {}), /cannot read config file .*missing\.env: file not found/);
    assert.throws(() => splitConfigArgs(["--config"]), /requires a file path/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("WORKBENCH_HTTP_PORT replaces the listen port unless -listen is explicit", () => {
  const env = { WORKBENCH_LISTEN: "127.0.0.1:8080", WORKBENCH_HTTP_PORT: "9191" };
  assert.equal(makeDevPlan(parseDevArgs([]), env, "/repo").backendUrl, "http://127.0.0.1:9191");
  assert.equal(makeDevPlan(parseDevArgs(["-listen", "127.0.0.1:9092"]), env, "/repo").backendUrl, "http://127.0.0.1:9092");
  assert.throws(() => makeDevPlan(parseDevArgs([]), { WORKBENCH_HTTP_PORT: "x" }, "/repo"), /WORKBENCH_HTTP_PORT/);
});

test("dev uses its own data path unless -data or WORKBENCH_DATA is set", async () => {
  const defaultPlan = makeDevPlan(parseDevArgs([]), {}, "/repo");
  assert.equal(defaultPlan.dataPath, "/repo/.workbench/dev/data.db");
  assert.equal(defaultPlan.dataSource, "dev default");
  assert.equal(defaultPlan.env.WORKBENCH_DATA, "/repo/.workbench/dev/data.db");
  assert.deepEqual(defaultPlan.options.goArgs, []);
  assert.equal(makeDevPlan(parseDevArgs([]), { WORKBENCH_DATA: "" }, "/repo").dataSource, "dev default");

  const cliPlan = makeDevPlan(parseDevArgs(["-data", "/tmp/cli.db"]), { WORKBENCH_DATA: "/tmp/env.db" }, "/repo");
  assert.equal(cliPlan.dataPath, "/tmp/cli.db");
  assert.equal(cliPlan.dataSource, "-data");
  assert.equal(cliPlan.env.WORKBENCH_DATA, "/tmp/env.db");

  const shellPlan = makeDevPlan(parseDevArgs([]), { WORKBENCH_DATA: "/tmp/shell.db" }, "/repo");
  assert.equal(shellPlan.dataPath, "/tmp/shell.db");
  assert.equal(shellPlan.dataSource, "WORKBENCH_DATA");
  assert.equal(shellPlan.env.WORKBENCH_DATA, "/tmp/shell.db");

  const directory = await mkdtemp(join(tmpdir(), "workbench-config-test-"));
  try {
    const file = join(directory, "dev.env");
    await writeFile(file, "WORKBENCH_DATA=/tmp/config.db\n");
    const { env } = loadConfigEnv([file], {});
    const configPlan = makeDevPlan(parseDevArgs([], env), env, "/repo");
    assert.equal(configPlan.dataPath, "/tmp/config.db");
    assert.equal(configPlan.dataSource, "WORKBENCH_DATA");
    assert.equal(configPlan.env.WORKBENCH_DATA, "/tmp/config.db");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("a config file alone configures backend, Vite proxy, hosts and dev options", async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-config-test-"));
  try {
    const file = join(directory, "dev.env");
    await writeFile(file, [
      "WORKBENCH_AUTH=password",
      "WORKBENCH_LISTEN=127.0.0.1:8080",
      "WORKBENCH_HTTP_PORT=9091",
      "WORKBENCH_PUBLIC_URL=https://w.vm2.de5.net",
      "WORKBENCH_ALLOWED_HOSTS=w.vm2.de5.net,other.example.test",
      "WORKBENCH_DEV_WEB_PORT=9090",
      "WORKBENCH_DEV_WEB_HOST=127.0.0.1",
      "WORKBENCH_DEV_URL=https://hmr.example.test",
    ].join("\n"));
    const { env } = loadConfigEnv([file], {});
    const options = parseDevArgs([], env);
    const plan = makeDevPlan(options, env, "/repo");
    assert.equal(plan.auth, "password");
    assert.equal(plan.listen, "127.0.0.1:9091");
    assert.equal(plan.listenSource, "WORKBENCH_LISTEN + WORKBENCH_HTTP_PORT");
    assert.equal(plan.backendUrl, "http://127.0.0.1:9091");
    assert.equal(plan.env.WORKBENCH_DEV_BACKEND_URL, "http://127.0.0.1:9091");
    assert.equal(plan.webUrl, "http://127.0.0.1:9090");
    assert.equal(plan.env.WORKBENCH_DEV_PORT, "9090");
    assert.equal(plan.env.WORKBENCH_DEV_PUBLIC_URL, "https://hmr.example.test");
    assert.deepEqual(plan.options.sources, {
      mode: "default", webHost: "WORKBENCH_DEV_WEB_HOST", webPort: "WORKBENCH_DEV_WEB_PORT", devUrl: "WORKBENCH_DEV_URL",
    });
    assert.deepEqual(plan.env.WORKBENCH_DEV_HOSTS.split(","), ["w.vm2.de5.net", "other.example.test", "hmr.example.test"]);
    for (const host of ["w.vm2.de5.net", "other.example.test", "localhost:9090", "127.0.0.1:9091"]) {
      assert.ok(plan.env.WORKBENCH_ALLOWED_HOSTS.split(",").includes(host), host);
    }
    assert.deepEqual(plan.options.goArgs, []);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("public URL host reaches Vite and backend allowed hosts without WORKBENCH_ALLOWED_HOSTS", () => {
  const env = { WORKBENCH_PUBLIC_URL: "https://w.vm2.de5.net", WORKBENCH_LISTEN: "127.0.0.1:9091" };
  const plan = makeDevPlan(parseDevArgs([], env), env, "/repo");
  assert.equal(plan.env.WORKBENCH_DEV_HOSTS, "w.vm2.de5.net");
  assert.ok(plan.env.WORKBENCH_ALLOWED_HOSTS.split(",").includes("w.vm2.de5.net"));
  assert.equal(plan.env.WORKBENCH_DEV_PUBLIC_URL, "https://w.vm2.de5.net");
});

test("dev option precedence is CLI, then WORKBENCH_DEV_WEB_*, then legacy names, then defaults", () => {
  assert.equal(parseDevArgs([], {}).webPort, 5173);
  assert.deepEqual(parseDevArgs([], {}).sources, { mode: "default", webHost: "default", webPort: "default", devUrl: "" });
  const legacy = parseDevArgs([], { WORKBENCH_DEV_PORT: "5180", WORKBENCH_DEV_HOST: "0.0.0.0" });
  assert.equal(legacy.webPort, 5180);
  assert.equal(legacy.webHost, "0.0.0.0");
  assert.equal(legacy.sources.webPort, "WORKBENCH_DEV_PORT");
  const env = { WORKBENCH_DEV_WEB_PORT: "5190", WORKBENCH_DEV_PORT: "5180", WORKBENCH_DEV_URL: "https://env.example.test" };
  assert.equal(parseDevArgs([], env).webPort, 5190);
  const cli = parseDevArgs(["--web-port", "19090", "--dev-url=https://cli.example.test"], env);
  assert.equal(cli.webPort, 19090);
  assert.equal(cli.sources.webPort, "--web-port");
  assert.equal(cli.devUrl, "https://cli.example.test");
  assert.throws(() => parseDevArgs([], { WORKBENCH_DEV_WEB_PORT: "abc" }), /WORKBENCH_DEV_WEB_PORT must be a port/);
  assert.throws(() => parseDevArgs([], { WORKBENCH_DEV_URL: "https://x.test/path" }), /WORKBENCH_DEV_URL must be/);
});

test("shell environment overrides the config file and CLI flags override both", async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-config-test-"));
  try {
    const file = join(directory, "dev.env");
    await writeFile(file, "WORKBENCH_LISTEN=127.0.0.1:9091\nWORKBENCH_DEV_WEB_PORT=9090\nWORKBENCH_AUTH=password\n");
    const { env } = loadConfigEnv([file], { WORKBENCH_LISTEN: "127.0.0.1:19091", WORKBENCH_DEV_WEB_PORT: "19090" });
    const plan = makeDevPlan(parseDevArgs(["-auth", "local"], env), env, "/repo");
    assert.equal(plan.backendUrl, "http://127.0.0.1:19091");
    assert.equal(plan.webUrl, "http://127.0.0.1:19090");
    assert.equal(plan.auth, "local");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

function fixture(command, args) {
  return { command: process.execPath, args: ["-e", command, ...(args || [])], env: process.env };
}

function childService(name, command) {
  return { name, ...fixture(command) };
}

function processGroupCode(marker, { ignoreTerm = false } = {}) {
  const signalHandler = ignoreTerm ? "process.on('SIGTERM', () => {});" : "";
  const childCode = `${signalHandler} setInterval(() => {}, 1000);`;
  return `
    const { spawn } = await import('node:child_process');
    const { writeFileSync } = await import('node:fs');
    ${signalHandler}
    const child = spawn(process.execPath, ['-e', ${JSON.stringify(childCode)}], {stdio: 'ignore'});
    writeFileSync(${JSON.stringify(marker)}, String(process.pid) + ' ' + child.pid);
    setInterval(() => {}, 1000);
  `;
}

async function waitForFile(path, timeoutMs = 5_000) {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    try { return (await readFile(path, "utf8")).trim().split(/\s+/).map(Number); } catch { /* wait */ }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`timed out waiting for ${path}`);
}

function processIsRunning(pid) {
  try {
    process.kill(pid, 0);
    if (process.platform === "linux") {
      const stat = readFileSync(`/proc/${pid}/stat`, "utf8");
      const close = stat.lastIndexOf(")");
      const state = stat.slice(close + 1).trim().split(/\s+/)[0];
      return state !== "Z" && state !== "X";
    }
    return true;
  } catch (error) {
    if (error.code === "ESRCH" || error.code === "ENOENT") return false;
    throw error;
  }
}

async function assertStopped(pids) {
  const end = Date.now() + 3_000;
  while (Date.now() < end && pids.some(processIsRunning)) {
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  assert.deepEqual(pids.filter(processIsRunning), [], "supervisor left a service process running");
}

test("an unexpected nonzero service exit stops its sibling and orphan descendants", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "processes");
  const backend = `
    const { spawn } = await import('node:child_process');
    const { writeFileSync } = await import('node:fs');
    const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {stdio: 'ignore'});
    writeFileSync(${JSON.stringify(marker)}, String(process.pid) + ' ' + child.pid);
    setInterval(() => {}, 1000);
  `;
  try {
    const code = await supervise({
      build: fixture("process.exit(0)"),
      services: [
        childService("Go backend", backend),
        childService("Vite", "setTimeout(() => process.exit(7), 350)"),
      ],
      graceMs: 1_000,
    });
    const pids = await waitForFile(marker);
    assert.equal(code, 7);
    await assertStopped(pids);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("SIGTERM stops the complete service process group", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "processes");
  let supervisor;
  try {
    supervisor = supervise({
      build: fixture("process.exit(0)"),
      services: [childService("Go backend", processGroupCode(marker)), childService("Vite", "setInterval(() => {}, 1000)")],
      graceMs: 1_000,
    });
    const pids = await waitForFile(marker);
    process.emit("SIGTERM");
    assert.equal(await supervisor, 143);
    await assertStopped(pids);
  } finally {
    if (supervisor) await supervisor;
    await rm(directory, { recursive: true, force: true });
  }
});

test("SIGHUP stops the complete service process group", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "processes");
  let supervisor;
  try {
    supervisor = supervise({
      build: fixture("process.exit(0)"),
      services: [childService("Go backend", processGroupCode(marker)), childService("Vite", "setInterval(() => {}, 1000)")],
      graceMs: 1_000,
    });
    const pids = await waitForFile(marker);
    process.emit("SIGHUP");
    assert.equal(await supervisor, 129);
    await assertStopped(pids);
  } finally {
    if (supervisor) await supervisor;
    await rm(directory, { recursive: true, force: true });
  }
});

test("ignored SIGTERM escalates to SIGKILL for child process groups", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "processes");
  try {
    const code = await supervise({
      build: fixture("process.exit(0)"),
      services: [
        childService("Go backend", processGroupCode(marker, { ignoreTerm: true })),
        childService("Vite", "setTimeout(() => process.exit(8), 350)"),
      ],
      graceMs: 100,
    });
    assert.equal(code, 8);
    await assertStopped(await waitForFile(marker));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("SIGINT during Go compilation kills the compiler process group", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "compiler-processes");
  const serviceMarker = join(directory, "should-not-start");
  let supervisor;
  try {
    supervisor = supervise({
      build: fixture(processGroupCode(marker, { ignoreTerm: true })),
      services: [childService("Go backend", `await import('node:fs').then(({writeFileSync}) => writeFileSync(${JSON.stringify(serviceMarker)}, 'started'))`)],
      graceMs: 100,
    });
    const pids = await waitForFile(marker);
    process.emit("SIGINT");
    assert.equal(await supervisor, 130);
    await assertStopped(pids);
    await assert.rejects(readFile(serviceMarker), { code: "ENOENT" });
  } finally {
    if (supervisor) await supervisor;
    await rm(directory, { recursive: true, force: true });
  }
});

test("a service spawn error returns a startup failure", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const code = await supervise({
    build: fixture("process.exit(0)"),
    services: [{ name: "Vite", command: join(tmpdir(), "workbench-missing-dev-service"), args: [], env: process.env }],
    graceMs: 100,
  });
  assert.equal(code, 1);
});

test("a one-shot service may exit successfully or return its own error code", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  for (const expected of [0, 5]) {
    const code = await supervise({
      build: fixture("process.exit(0)"),
      services: [{ name: "workbench version", ...fixture(`process.exit(${expected})`), oneShot: true }],
      graceMs: 100,
    });
    assert.equal(code, expected);
  }
});

test("a Go build failure does not start services", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const marker = join(directory, "should-not-start");
  try {
    const code = await supervise({
      build: fixture("process.exit(4)"),
      services: [childService("service", `await import('node:fs').then(({writeFileSync}) => writeFileSync(${JSON.stringify(marker)}, 'started'))`)],
      graceMs: 1_000,
    });
    assert.equal(code, 4);
    await assert.rejects(readFile(marker), { code: "ENOENT" });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("mode precedence is --mode, then WORKBENCH_MODE, then dev", () => {
  assert.equal(parseDevArgs([], {}).mode, "dev");
  assert.equal(parseDevArgs([], { WORKBENCH_MODE: "prod" }).mode, "prod");
  assert.equal(parseDevArgs([], { WORKBENCH_MODE: "prod" }).sources.mode, "WORKBENCH_MODE");
  const cli = parseDevArgs(["--mode", "dev"], { WORKBENCH_MODE: "prod" });
  assert.equal(cli.mode, "dev");
  assert.equal(cli.sources.mode, "--mode");
  assert.equal(parseDevArgs(["--mode=PROD"], {}).mode, "prod");
  assert.throws(() => parseDevArgs([], { WORKBENCH_MODE: "staging" }), /WORKBENCH_MODE must be dev or prod/);
  assert.throws(() => parseDevArgs(["--mode", "x"], {}), /--mode must be dev or prod/);
});

test("prod mode targets the backend only and passes no Vite settings", () => {
  const env = { WORKBENCH_MODE: "prod", WORKBENCH_PUBLIC_URL: "https://w.vm2.de5.net", WORKBENCH_LISTEN: "127.0.0.1:9091", WORKBENCH_DEV_WEB_PORT: "9090" };
  const plan = makeDevPlan(parseDevArgs([], env), env, "/repo");
  assert.equal(plan.mode, "prod");
  assert.equal(plan.backendUrl, "http://127.0.0.1:9091");
  const hosts = plan.env.WORKBENCH_ALLOWED_HOSTS.split(",");
  assert.ok(hosts.includes("w.vm2.de5.net"));
  assert.ok(hosts.includes("127.0.0.1:9091"));
  assert.ok(!hosts.includes("127.0.0.1:9090"));
  for (const key of ["WORKBENCH_DEV_CHART_DIR", "WORKBENCH_DEV_BACKEND_URL", "WORKBENCH_DEV_HOSTS", "WORKBENCH_DEV_PUBLIC_URL"]) {
    assert.equal(plan.env[key], undefined, key);
  }
  const dev = makeDevPlan(parseDevArgs(["--mode", "dev"], env), env, "/repo");
  assert.equal(dev.mode, "dev");
  assert.equal(dev.env.WORKBENCH_DEV_BACKEND_URL, "http://127.0.0.1:9091");
  assert.ok(dev.env.WORKBENCH_ALLOWED_HOSTS.split(",").includes("127.0.0.1:9090"));
});

test("build steps run in order and a failed web build stops before Go build and services", {
  skip: process.platform === "win32",
  timeout: 20_000,
}, async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-test-"));
  const goMarker = join(directory, "go-build");
  const serviceMarker = join(directory, "service");
  const touch = (file) => `await import('node:fs').then(({writeFileSync}) => writeFileSync(${JSON.stringify(file)}, 'ran'))`;
  try {
    const failed = await supervise({
      build: [{ name: "web build", ...fixture("process.exit(3)") }, { name: "Go build", ...fixture(touch(goMarker)) }],
      services: [childService("service", touch(serviceMarker))],
      graceMs: 1_000,
    });
    assert.equal(failed, 3);
    await assert.rejects(readFile(goMarker), { code: "ENOENT" });
    await assert.rejects(readFile(serviceMarker), { code: "ENOENT" });

    const order = join(directory, "order");
    const append = (label) => `await import('node:fs').then(({appendFileSync}) => appendFileSync(${JSON.stringify(order)}, ${JSON.stringify(label)}))`;
    const ok = await supervise({
      build: [{ name: "web build", ...fixture(append("web,")) }, { name: "Go build", ...fixture(append("go,")) }],
      services: [{ name: "backend", ...fixture(append("service")), oneShot: true }],
      graceMs: 1_000,
    });
    assert.equal(ok, 0);
    assert.equal(await readFile(order, "utf8"), "web,go,service");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
