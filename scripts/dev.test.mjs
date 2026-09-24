import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { makeDevPlan, parseDevArgs, supervise } from "./dev.mjs";

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
