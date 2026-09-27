import { mkdir } from "node:fs/promises";

await mkdir("build/backend", { recursive: true });
