import { execFileSync } from "node:child_process";

try {
  const paths = ["frontend/openapi.json", "frontend/src/api/generated"];
  execFileSync("git", ["diff", "--exit-code", "--", ...paths], { stdio: "inherit" });
  const untracked = execFileSync("git", ["ls-files", "--others", "--exclude-standard", "--", ...paths], { encoding: "utf8" }).trim();
  if (untracked) throw new Error(`Untracked generated files:\n${untracked}`);
} catch (error) {
  console.error(error.message);
  console.error("Generated API files are stale. Run task generate.");
  process.exit(1);
}
