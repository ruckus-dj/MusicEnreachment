import { spawn } from "node:child_process";
import { copyFile, cp, mkdir, readdir, rm } from "node:fs/promises";
import path from "node:path";
import { createInterface } from "node:readline";

const MAX_STDERR_BYTES = 8192;

// objdump can print megabytes of unrelated PE details. Stream stdout line by
// line and retain only import names, so the default buffer limits never apply.
export async function inspectNativeImports(source, { spawnProcess = spawn } = {}) {
	return new Promise((resolve, reject) => {
		let child;
		try {
			child = spawnProcess("objdump", ["-p", source], { stdio: ["ignore", "pipe", "pipe"] });
		} catch (error) {
			reject(error);
			return;
		}
		const imports = [];
		let stderr = "";
		let settled = false;
		const fail = (error) => {
			if (settled) return;
			settled = true;
			reject(error);
		};
		const appendStderr = (chunk) => {
			if (stderr.length >= MAX_STDERR_BYTES) return;
			stderr += chunk.toString("utf8", 0, Math.max(0, MAX_STDERR_BYTES - stderr.length));
		};
		child.on("error", fail);
		child.stderr?.on("data", appendStderr);
		const lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
		lines.on("line", (line) => {
			const match = /^\s*DLL Name:\s*(\S+)/i.exec(line);
			if (match) imports.push(match[1]);
		});
		lines.on("error", fail);
		child.on("close", (code, signal) => {
			if (settled) return;
			if (code !== 0) {
				const detail = stderr.trim() || (signal ? `terminated by ${signal}` : `exit code ${code}`);
				fail(new Error(`objdump failed to inspect ${source}: ${detail}`));
				return;
			}
			settled = true;
			resolve(imports);
		});
	});
}

// Only Windows native builds need an adjacent DLL closure. Resolve actual PE
// imports instead of bundling every optional codec installed on the builder.
export async function stageNativeLibraries(prefix, destination, toolchainBin, { inspectImports = inspectNativeImports } = {}) {
	const searchDirectories = [path.join(prefix, "bin"), toolchainBin];
	const available = new Map();
	for (const directory of searchDirectories) {
		for (const name of await readdir(directory)) {
			if (name.toLowerCase().endsWith(".dll") && !available.has(name.toLowerCase())) {
				available.set(name.toLowerCase(), path.join(directory, name));
			}
		}
	}
	const root = available.get("libvips-42.dll");
	if (!root) throw new Error("Native libvips DLL is missing from the build prefix");
	const pending = [root];
	const copied = new Set();
	await mkdir(destination, { recursive: true });
	// The destination is generated build output. An earlier SDK build must not
	// leave unused codec DLLs in the new package.
	for (const name of await readdir(destination)) {
		if (name.toLowerCase().endsWith(".dll")) await rm(path.join(destination, name));
	}
	await rm(path.join(destination, "native-sources"), { recursive: true, force: true });
	while (pending.length) {
		const source = pending.pop();
		const name = path.basename(source).toLowerCase();
		if (copied.has(name)) continue;
		copied.add(name);
		await copyFile(source, path.join(destination, path.basename(source)));
		const inspected = await inspectImports(source);
		const imports = typeof inspected === "string"
			? [...inspected.matchAll(/DLL Name:\s*(\S+)/g)].map((match) => match[1])
			: inspected;
		for (const imported of imports) {
			const dependency = available.get(imported.toLowerCase());
			if (dependency) pending.push(dependency);
			else if (!/^(api-ms-|ext-ms-)/i.test(imported) && !windowsSystemLibraries.has(imported.toLowerCase())) {
				throw new Error(`Unresolved native import ${imported} required by ${name}`);
			}
		}
	}
	await cp(path.join(prefix, "share", "native-sources"), path.join(destination, "native-sources"), { recursive: true });
	await cp(path.join(toolchainBin, "..", "share", "licenses"), path.join(destination, "native-sources", "toolchain-licenses"), { recursive: true });
}

const windowsSystemLibraries = new Set([
	"advapi32.dll", "bcrypt.dll", "crypt32.dll", "dnsapi.dll", "gdi32.dll",
	"imm32.dll", "iphlpapi.dll", "kernel32.dll", "msvcrt.dll", "netapi32.dll",
	"normaliz.dll", "ntdll.dll", "ole32.dll", "oleaut32.dll", "powrprof.dll",
	"psapi.dll", "rpcrt4.dll", "secur32.dll", "setupapi.dll", "shell32.dll",
	"shlwapi.dll", "ucrtbase.dll", "urlmon.dll", "user32.dll", "userenv.dll", "usp10.dll",
	"version.dll", "winmm.dll", "winspool.drv", "ws2_32.dll", "wtsapi32.dll",
]);
