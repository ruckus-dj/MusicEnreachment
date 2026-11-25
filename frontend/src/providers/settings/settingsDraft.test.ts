import { beforeEach, describe, expect, it } from "vitest";
import { createMemoryStorage } from "../../test/memoryStorage";
import {
  readSettingsDraft,
  rebaseSettingsDraft,
  resetSettingsDraftMemory,
  writeSettingsDraft,
} from "./settingsDraft";

describe("settings draft persistence", () => {
  beforeEach(() => resetSettingsDraftMemory());

  it("restores raw drafts, rebases only pristine fields, and discards stale data", () => {
    const storage = createMemoryStorage();
    const snapshot = {
      sourceFileConcurrency: {
        value: "invalid 8",
        baseline: "4",
        revision: 2,
      },
      publicationPathTemplate: {
        value: "old template",
        baseline: "old template",
        revision: 0,
      },
    };

    expect(writeSettingsDraft(storage, snapshot)).toBe(true);
    resetSettingsDraftMemory();
    const restored = readSettingsDraft(storage);
    expect(restored).toEqual(snapshot);
    if (!restored) throw new Error("Expected the saved settings draft");
    expect(
      rebaseSettingsDraft(restored, {
        sourceFileConcurrency: "6",
        publicationPathTemplate: "new template",
      }),
    ).toEqual({
      sourceFileConcurrency: snapshot.sourceFileConcurrency,
      publicationPathTemplate: {
        value: "new template",
        baseline: "new template",
        revision: 0,
      },
    });

    expect(writeSettingsDraft(storage, null)).toBe(true);
    resetSettingsDraftMemory();
    expect(readSettingsDraft(storage)).toBeNull();
  });
});
