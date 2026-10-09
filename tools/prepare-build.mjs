import { copyFile, mkdir } from "node:fs/promises";

await mkdir("build/backend", { recursive: true });
await copyFile(
	"backend/THIRD_PARTY_NOTICES.md",
	"build/backend/THIRD_PARTY_NOTICES.md",
);
