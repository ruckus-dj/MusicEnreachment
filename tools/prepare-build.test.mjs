import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const script = fileURLToPath(new URL("./prepare-build.mjs", import.meta.url));
const notices = "# Third-Party Notices\n\nKnown fixture notice text for prepare-build.\n";

function prepareBuildFixture() {
  const directory = mkdtempSync(join(tmpdir(), "music-prepare-build-"));
  try {
    mkdirSync(join(directory, "backend"), { recursive: true });
    writeFileSync(join(directory, "backend/THIRD_PARTY_NOTICES.md"), notices);
    const prepare = () => spawnSync(process.execPath, [script], { cwd: directory, stdio: "pipe" });
    return { directory, prepare };
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw error;
  }
}

test("prepare-build creates build/backend and copies the notices byte-exact", () => {
  const { directory, prepare } = prepareBuildFixture();
  try {
    const result = prepare();
    assert.equal(result.status, 0, result.stderr?.toString());
    assert.deepEqual(
      readFileSync(join(directory, "build/backend/THIRD_PARTY_NOTICES.md")),
      Buffer.from(notices),
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("prepare-build overwrites a stale copied notice on repeat", () => {
  const { directory, prepare } = prepareBuildFixture();
  try {
    mkdirSync(join(directory, "build/backend"), { recursive: true });
    writeFileSync(join(directory, "build/backend/THIRD_PARTY_NOTICES.md"), "stale notice\n");
    assert.equal(prepare().status, 0);
    assert.deepEqual(
      readFileSync(join(directory, "build/backend/THIRD_PARTY_NOTICES.md")),
      Buffer.from(notices),
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
