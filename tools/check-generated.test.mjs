import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

test("generated check rejects changed and untracked contracts", () => {
  const directory = mkdtempSync(join(tmpdir(), "music-generated-"));
  const script = fileURLToPath(new URL("./check-generated.mjs", import.meta.url));
  const git = (...args) => execFileSync("git", args, { cwd: directory, stdio: "pipe" });
  const check = () => spawnSync(process.execPath, [script], { cwd: directory });
  try {
    git("init");
    mkdirSync(join(directory, "frontend/src/api/generated"), { recursive: true });
    const contract = join(directory, "frontend/openapi.json");
    writeFileSync(contract, "{}\n");
    git("add", ".");
    assert.equal(check().status, 0);

    writeFileSync(contract, '{"paths":{}}\n');
    assert.equal(check().status, 1);
    git("add", ".");
    assert.equal(check().status, 0);

    writeFileSync(join(directory, "frontend/src/api/generated/new.ts"), "export {};\n");
    assert.equal(check().status, 1);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
