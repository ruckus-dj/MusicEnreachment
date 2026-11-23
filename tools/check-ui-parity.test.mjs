import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { compareUiSources } from "./check-ui-parity.mjs";

test("a prototype lead permits only the named source versions and expires on port", () => {
  const app = Buffer.from("approved UI");
  const design = Buffer.from("next design");
  const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
  const pairs = [["View.tsx", "View.tsx"]];
  const lead = {
    from: "View.tsx",
    reason: "Next screen revision",
    prototypeSHA256: sha256(design),
    applicationSHA256: sha256(app),
  };
  const check = (source, copy, receipts) =>
    compareUiSources(pairs, receipts, () => source, () => copy);

  assert.match(check(design, app, []).errors[0], /Different source bytes/);
  const approved = check(design, app, [lead]);
  assert.deepEqual(approved.errors, []);
  assert.equal(approved.pending.length, 1);
  assert.match(
    check(design, Buffer.from("unapproved"), [lead]).errors[0],
    /Different source bytes/,
  );
  assert.match(
    check(app, app, [lead]).errors[0],
    /Remove the completed prototype lead/,
  );
  const checkBeforeFirstPort = (receipt) => compareUiSources(
    pairs,
    [receipt],
    () => design,
    () => { throw Object.assign(new Error("missing"), { code: "ENOENT" }); },
  );
  assert.equal(
    checkBeforeFirstPort({ ...lead, applicationSHA256: null }).pending.length,
    1,
  );
  assert.match(checkBeforeFirstPort(lead).errors[0], /Cannot compare/);
});
