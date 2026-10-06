import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

function checkGeneratedFixture(environment = process.env) {
  const directory = mkdtempSync(join(tmpdir(), "music-generated-"));
  const script = fileURLToPath(new URL("./check-generated.mjs", import.meta.url));
  const env = Object.fromEntries(
    Object.entries(environment).filter(([key]) => !key.startsWith("GIT_")),
  );
  const git = (...args) => execFileSync("git", args, { cwd: directory, stdio: "pipe", env });
  const check = () => spawnSync(process.execPath, [script], { cwd: directory, env });
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
}

test("generated check rejects changed and untracked contracts", () => {
  checkGeneratedFixture();
});

test("generated fixture preserves an inherited repository and index", () => {
  const directory = mkdtempSync(join(tmpdir(), "music-generated-parent-"));
  const gitDirectory = join(directory, ".git");
  const index = join(gitDirectory, "index");
  const config = join(gitDirectory, "config");
  try {
    mkdirSync(gitDirectory);
    writeFileSync(index, "parent index sentinel\n");
    writeFileSync(config, "[core]\n\tbare = false\n");
    checkGeneratedFixture({
      ...process.env,
      GIT_DIR: gitDirectory,
      GIT_COMMON_DIR: gitDirectory,
      GIT_WORK_TREE: directory,
      GIT_INDEX_FILE: index,
    });
    assert.equal(readFileSync(index, "utf8"), "parent index sentinel\n");
    assert.equal(readFileSync(config, "utf8"), "[core]\n\tbare = false\n");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
