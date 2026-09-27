import { execFileSync } from "node:child_process";

try {
  execFileSync("git", ["diff", "--exit-code", "--", "frontend/openapi.json", "frontend/src/api/generated"], { stdio: "inherit" });
} catch {
  console.error("Generated API files are stale. Run task generate.");
  process.exit(1);
}
