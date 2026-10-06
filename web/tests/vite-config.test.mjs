import assert from "node:assert/strict";
import { EventEmitter, once } from "node:events";
import { createServer, request } from "node:http";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createServer as createViteServer, loadConfigFromFile } from "vite";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const configPath = resolve(webDir, "vite.config.ts");
const chartDir = resolve(webDir, "../internal/modules/investment/chart");
const envNames = [
	"WORKBENCH_DEV_BACKEND_URL",
	"WORKBENCH_DEV_PORT",
	"WORKBENCH_DEV_HOST",
	"WORKBENCH_DEV_HOSTS",
	"WORKBENCH_DEV_PUBLIC_URL",
];

async function withWorkbenchDevEnv(values, run) {
	const previous = new Map(envNames.map((name) => [name, process.env[name]]));
	for (const name of envNames) delete process.env[name];
	Object.assign(process.env, values);
	try {
		return await run();
	} finally {
		for (const [name, value] of previous) {
			if (value === undefined) delete process.env[name];
			else process.env[name] = value;
		}
	}
}

async function loadDevConfig() {
	const loaded = await loadConfigFromFile({ command: "serve", mode: "development" }, configPath, webDir, "silent");
	assert.ok(loaded, "Vite config should load");
	return loaded.config;
}

test("dev defaults keep the local backend proxy and strict Vite port", async () => {
	await withWorkbenchDevEnv({}, async () => {
		const config = await loadDevConfig();
		const server = config.server;
		assert.equal(server.host, "127.0.0.1");
		assert.equal(server.port, 5173);
		assert.equal(server.strictPort, true);
		assert.notEqual(server.allowedHosts, true);
		assert.equal(server.ws, undefined);

		const proxy = server.proxy;
		assert.equal(proxy["/api"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/api"].ws, true);
		assert.equal(proxy["/modules"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/modules"].ws, true);
		assert.equal(proxy["/health"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/oauth/schwab"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/investment/terminal"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/charting_library"].target, "http://127.0.0.1:8080");
		assert.equal(proxy["/api"].changeOrigin, false);
	});
});

test("dev environment configures explicit hosts, backend and external HMR websocket", async () => {
	await withWorkbenchDevEnv(
		{
			WORKBENCH_DEV_BACKEND_URL: "https://api.example.test:8443",
			WORKBENCH_DEV_PORT: "5179",
			WORKBENCH_DEV_HOST: "0.0.0.0",
			WORKBENCH_DEV_HOSTS: ".example.test, front.dev.example",
			WORKBENCH_DEV_PUBLIC_URL: "https://preview.example.test",
		},
		async () => {
			const server = (await loadDevConfig()).server;
			assert.equal(server.host, "0.0.0.0");
			assert.equal(server.port, 5179);
			assert.deepEqual(server.allowedHosts, [".example.test", "front.dev.example", "preview.example.test"]);
			assert.deepEqual(server.ws, {
				protocol: "wss",
				host: "preview.example.test",
				clientPort: 443,
			});
			assert.equal(server.proxy["/api"].target, "https://api.example.test:8443");
			assert.equal(server.proxy["/api"].changeOrigin, false);
		},
	);
});

test("chart asset watcher reloads for file updates and ignores outside files", async () => {
	await withWorkbenchDevEnv({}, async () => {
		const config = await loadDevConfig();
		const plugins = config.plugins.flat(Infinity);
		const plugin = plugins.find((candidate) => candidate?.name === "workbench-investment-chart-full-reload");
		assert.ok(plugin, "chart full-reload plugin should be installed");

		const watcher = new EventEmitter();
		watcher.add = (directory) => {
			watcher.directory = directory;
			return watcher;
		};
		const messages = [];
		const httpServer = new EventEmitter();
		const server = { watcher, ws: { send: (message) => messages.push(message) }, httpServer };
		const configureServer = typeof plugin.configureServer === "function"
			? plugin.configureServer
			: plugin.configureServer.handler;
		configureServer(server);
		assert.equal(watcher.directory, chartDir);
		assert.equal(watcher.listenerCount("all"), 1);
		assert.equal(watcher.listenerCount("ready"), 1);

		watcher.emit("all", "change", resolve(chartDir, "early.js"));
		watcher.emit("ready");
		watcher.emit("all", "change", resolve(webDir, "src/main.tsx"));
		watcher.emit("all", "change", resolve(chartDir, "broker.js"));
		watcher.emit("all", "add", resolve(chartDir, "new-module.js"));
		watcher.emit("all", "unlink", resolve(chartDir, "removed-module.js"));
		await new Promise((resolveTimer) => setTimeout(resolveTimer, 110));
		assert.deepEqual(messages, [{ type: "full-reload" }]);

		httpServer.emit("close");
	});
});

test("dev config rejects invalid ports and URLs", async () => {
	await withWorkbenchDevEnv({ WORKBENCH_DEV_PORT: "70000" }, async () => {
		await assert.rejects(loadDevConfig(), /WORKBENCH_DEV_PORT/);
	});
	await withWorkbenchDevEnv({ WORKBENCH_DEV_BACKEND_URL: "relative/backend" }, async () => {
		await assert.rejects(loadDevConfig(), /WORKBENCH_DEV_BACKEND_URL/);
	});
});

test("dev proxies preserve the browser Host and Origin on every backend route", async () => {
	const origin = "https://dev-proxy.workbench.test:8443";
	const backend = createServer((req, res) => {
		res.setHeader("Content-Type", "application/json");
		res.end(JSON.stringify({ host: req.headers.host, origin: req.headers.origin, path: req.url }));
	});
	let vite;
	try {
		backend.listen(0, "127.0.0.1");
		await once(backend, "listening");
		await withWorkbenchDevEnv({
			WORKBENCH_DEV_BACKEND_URL: `http://127.0.0.1:${backend.address().port}`,
			WORKBENCH_DEV_PUBLIC_URL: origin,
		}, async () => {
			vite = await createViteServer({
				configFile: configPath,
				root: webDir,
				logLevel: "silent",
				server: { port: 0, ws: false },
				optimizeDeps: { noDiscovery: true, include: [] },
			});
			await vite.listen();
			const paths = [
				"/api/auth/status", "/health/ready", "/oauth/schwab?code=fixture",
				"/investment/terminal", "/investment/terminal/datafeed.js",
				"/charting_library/charting_library.esm.js", "/modules/fixture/page",
			];
			const results = await Promise.all(paths.map(path => new Promise((resolveResponse, reject) => {
				const req = request({
					host: "127.0.0.1", port: vite.httpServer.address().port, path,
					headers: { Host: new URL(origin).host, Origin: origin },
				}, res => {
					let body = "";
					res.on("data", chunk => { body += chunk; });
					res.on("end", () => {
						try { resolveResponse({ path, status: res.statusCode, body: JSON.parse(body) }); }
						catch (error) { reject(error); }
					});
				});
				req.on("error", reject);
				req.end();
			})));
			for (const result of results) {
				assert.equal(result.status, 200, result.path);
				assert.deepEqual(result.body, { host: new URL(origin).host, origin, path: result.path }, result.path);
			}
		});
	} finally {
		if (vite) await vite.close();
		await new Promise(resolveClose => backend.close(resolveClose));
	}
});
