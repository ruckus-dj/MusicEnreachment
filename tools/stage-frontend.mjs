import { cp, mkdir, readdir, rm } from "node:fs/promises";
import { join } from "node:path";

const source = "build/frontend";
const destination = "backend/internal/static/dist";

await mkdir(destination, { recursive: true });
for (const entry of await readdir(destination)) {
  if (entry !== ".gitkeep") await rm(join(destination, entry), { recursive: true, force: true });
}
await cp(source, destination, { recursive: true });
