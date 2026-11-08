import { copyFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { selectWindowsToolchainBin, stageNativeLibraries } from "./stage-native-libraries.mjs";

await mkdir("build/backend", { recursive: true });
await copyFile(
	"backend/THIRD_PARTY_NOTICES.md",
	"build/backend/THIRD_PARTY_NOTICES.md",
);

if (process.platform === "win32") {
	if (!process.env.VIPSHOME) {
		throw new Error("Windows native packaging requires the VIPSHOME build path");
	}
	const compiler =
		process.env.NATIVE_WINDOWS_TOOLCHAIN_BIN ??
		selectWindowsToolchainBin(process.env.PATH.split(path.delimiter), process.env.CC);
	if (!compiler) throw new Error("Windows UCRT64 MinGW dependency directory is unavailable");
	await stageNativeLibraries(process.env.VIPSHOME, "build/backend", compiler);
}
