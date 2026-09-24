import { isIP } from "node:net";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";

const webDir = dirname(fileURLToPath(import.meta.url));
const chartDir = resolve(webDir, "../internal/modules/investment/chart");

function envValue(name: string, fallback: string): string {
	return process.env[name]?.trim() || fallback;
}

function parseOrigin(name: string, fallback: string): URL {
	const value = envValue(name, fallback);
	let url: URL;
	try {
		url = new URL(value);
	} catch {
		throw new Error(`${name} must be an absolute http(s) origin`);
	}
	if (
		(url.protocol !== "http:" && url.protocol !== "https:") ||
		url.username !== "" ||
		url.password !== "" ||
		url.pathname !== "/" ||
		url.search !== "" ||
		url.hash !== ""
	) {
		throw new Error(`${name} must be an absolute http(s) origin without a path`);
	}
	return url;
}

function parsePort(name: string, fallback: number): number {
	const configured = process.env[name]?.trim();
	if (!configured) return fallback;
	const port = Number(configured);
	if (!Number.isInteger(port) || port < 1 || port > 65535) {
		throw new Error(`${name} must be an integer from 1 to 65535`);
	}
	return port;
}

function devAllowedHosts(devHost: string, publicHost: string | undefined): string[] {
	const hosts = (process.env.WORKBENCH_DEV_HOSTS ?? "")
		.split(",")
		.map((host) => host.trim().toLowerCase())
		.filter(Boolean);
	if (isIP(devHost) === 0 && devHost !== "0.0.0.0" && devHost !== "::") hosts.push(devHost.toLowerCase());
	if (publicHost && isIP(publicHost) === 0) hosts.push(publicHost.toLowerCase());
	return [...new Set(hosts)];
}

function chartAssetReloadPlugin(directory: string): Plugin {
	return {
		name: "workbench-investment-chart-full-reload",
		configureServer(server) {
			let ready = false;
			let reloadTimer: ReturnType<typeof setTimeout> | undefined;
			server.watcher.on("ready", () => {
				ready = true;
			});
			server.watcher.on("all", (event, file) => {
				if (!ready || !["add", "change", "unlink"].includes(event)) return;
				const pathWithinDirectory = relative(directory, resolve(file));
				if (
					pathWithinDirectory === "" ||
					pathWithinDirectory === ".." ||
					pathWithinDirectory.startsWith(`..${sep}`) ||
					isAbsolute(pathWithinDirectory)
				) {
					return;
				}
				if (reloadTimer) clearTimeout(reloadTimer);
				reloadTimer = setTimeout(() => {
					reloadTimer = undefined;
					server.ws.send({ type: "full-reload" });
				}, 75);
			});
			server.httpServer?.once("close", () => {
				if (reloadTimer) clearTimeout(reloadTimer);
			});
			server.watcher.add(directory);
		},
	};
}

const backendOrigin = parseOrigin("WORKBENCH_DEV_BACKEND_URL", "http://127.0.0.1:8080");
const devHost = envValue("WORKBENCH_DEV_HOST", "127.0.0.1");
const devPort = parsePort("WORKBENCH_DEV_PORT", 5173);
const publicURLValue = process.env.WORKBENCH_DEV_PUBLIC_URL?.trim();
const publicURL = publicURLValue ? parseOrigin("WORKBENCH_DEV_PUBLIC_URL", publicURLValue) : undefined;

export default defineConfig({
	plugins: [react(), tailwindcss(), chartAssetReloadPlugin(chartDir)],
	server: {
		host: devHost,
		port: devPort,
		strictPort: true,
		allowedHosts: devAllowedHosts(devHost, publicURL?.hostname),
		ws: publicURL
			? {
					protocol: publicURL.protocol === "https:" ? "wss" : "ws",
					host: publicURL.hostname,
					clientPort: Number(publicURL.port) || (publicURL.protocol === "https:" ? 443 : 80),
				}
			: undefined,
		proxy: {
			"/api": { target: backendOrigin.origin, ws: true },
			"/health": backendOrigin.origin,
			"/oauth/schwab": backendOrigin.origin,
			"/investment/terminal": backendOrigin.origin,
			"/charting_library": backendOrigin.origin,
			"/modules": { target: backendOrigin.origin, ws: true },
		},
	},
	build: {
		outDir: "../internal/webui/dist",
		emptyOutDir: true,
	},
});
