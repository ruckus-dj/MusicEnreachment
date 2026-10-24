import { copyFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { stageNativeLibraries } from "./stage-native-libraries.mjs";

await mkdir("build/backend", { recursive: true });
await copyFile(
	"backend/THIRD_PARTY_NOTICES.md",
	"build/backend/THIRD_PARTY_NOTICES.md",
);

if (process.platform === "win32") {
	if (!process.env.VIPSHOME) {
		throw new Error("Windows native packaging requires the VIPSHOME build path");
	}
	const compiler = !process.env.CC || process.env.CC === "gcc"
		? process.env.PATH.split(path.delimiter).find((directory) => /mingw64[\\/]bin$/i.test(directory))
		: path.dirname(process.env.CC);
	if (!compiler) throw new Error("Windows MinGW dependency directory is unavailable");
	await stageNativeLibraries(process.env.VIPSHOME, "build/backend", compiler);
}
