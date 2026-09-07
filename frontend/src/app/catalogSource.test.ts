import { describe, expect, it } from "vitest";
import type { Source, Summary } from "../types";
import { catalogSource } from "./useAppController";

function summary(overrides: Partial<Summary>): Summary {
  return {
    record_id: "record-1",
    source_state: "present",
    processing_state: "complete",
    match_state: "matched",
    publication_state: "current",
    metadata_state: "final",
    sources: [],
    publications: [],
    ...overrides,
  } as Summary;
}

function source(id: string, overrides: Partial<Source> = {}): Source {
  return { source_id: id, path: `/${id}.flac`, sha256: id, state: "present", ...overrides };
}

describe("catalogSource", () => {
  it("prefers the source backing the current publication", () => {
    const item = summary({
      sources: [source("source-a"), source("source-b")],
      publications: [
        { publication_id: "pub-1", path: "/b.mka", source_id: "source-b", state: "current" },
      ],
    });

    expect(catalogSource(item)?.source_id).toBe("source-b");
  });

  it("falls back to the first non-disappeared source when there is no current publication", () => {
    const item = summary({
      sources: [
        source("source-a", { state: "disappeared" }),
        source("source-b", { state: "present" }),
      ],
      publications: [],
    });

    expect(catalogSource(item)?.source_id).toBe("source-b");
  });

  it("falls back to the first non-disappeared source when the publication's source is unknown", () => {
    const item = summary({
      sources: [
        source("source-a", { state: "disappeared" }),
        source("source-b", { state: "present" }),
      ],
      publications: [
        {
          publication_id: "pub-1",
          path: "/gone.mka",
          source_id: "source-missing",
          state: "current",
        },
      ],
    });

    expect(catalogSource(item)?.source_id).toBe("source-b");
  });

  it("falls back to the first source of all when every source has disappeared", () => {
    const item = summary({
      sources: [
        source("source-a", { state: "disappeared" }),
        source("source-b", { state: "disappeared" }),
      ],
      publications: [],
    });

    expect(catalogSource(item)?.source_id).toBe("source-a");
  });

  it("returns undefined when there are no sources at all", () => {
    const item = summary({ sources: [], publications: [] });

    expect(catalogSource(item)).toBeUndefined();
  });
});
