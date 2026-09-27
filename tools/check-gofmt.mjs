import { execFileSync } from "node:child_process";

const unformatted = execFileSync("gofmt", ["-l", "backend"], { encoding: "utf8" }).trim();
if (unformatted) {
  console.error(`Run task format; unformatted Go files:\n${unformatted}`);
  process.exit(1);
}
