import { execFileSync } from "node:child_process";
import { copyFile, cp, mkdir, readdir, rm } from "node:fs/promises";
import path from "node:path";

// Only Windows native builds need an adjacent DLL closure. Resolve actual PE
// imports instead of bundling every optional codec installed on the builder.
export async function stageNativeLibraries(prefix, destination, toolchainBin, { inspectImports = (source) => execFileSync("objdump", ["-p", source], { encoding: "utf8" }) } = {}) {
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
			const imports = inspectImports(source);
		for (const match of imports.matchAll(/DLL Name:\s*(\S+)/g)) {
			const dependency = available.get(match[1].toLowerCase());
			if (dependency) pending.push(dependency);
			else if (!/^(api-ms-|ext-ms-)/i.test(match[1]) && !windowsSystemLibraries.has(match[1].toLowerCase())) {
				throw new Error(`Unresolved native import ${match[1]} required by ${name}`);
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
	"shlwapi.dll", "ucrtbase.dll", "user32.dll", "userenv.dll", "usp10.dll",
	"version.dll", "winmm.dll", "winspool.drv", "ws2_32.dll", "wtsapi32.dll",
]);
