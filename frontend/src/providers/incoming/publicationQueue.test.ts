import { describe, expect, it } from "vitest";
import { buildPublicationEditions } from "./publicationQueue";

const credit = {
  artist_id: "artist-1",
  artist_name: "Arden Trio",
  credit_name: "Arden Trio",
  join_phrase: "",
};

function position(overrides = {}) {
  return {
    id: "position-1",
    release: {
      id: "release-1",
      title: "Northern Lines",
      artists: [credit],
    },
    source_variant_count: 1,
    selected_media_variant_id: "source-1",
    title: "Still Water",
    medium: 1,
    position: 1,
    displayed_number: "1",
    ...overrides,
  };
}

function track(overrides = {}) {
  return {
    id: "position-1",
    title: "Still Water",
    medium: 1,
    position: 1,
    displayed_number: "1",
    publication: null,
    sources: [
      {
        selected: true,
        locations: [
          {
            relative_path: "inbox/01-still-water.flac",
            probe_status: "audio",
          },
        ],
      },
    ],
    ...overrides,
  };
}

function release(overrides = {}) {
  return {
    id: "release-1",
    title: "Northern Lines",
    metadata: {
      local_metadata: {
        desired: { release_date: { state: "value", value: "2024-05-01" } },
      },
    },
    primary_artist: {
      effective_artist_id: "artist-1" as string | null,
      candidate_artist_ids: ["artist-1"],
    },
    artists: [credit],
    tracks: [track()],
    ...overrides,
  };
}

function build(
  disposition: { disposition: string; reason?: string; path_clash?: boolean },
  options: {
    positions?: ReturnType<typeof position>[];
    release?: ReturnType<typeof release>;
  } = {},
) {
  return buildPublicationEditions({
    positions: options.positions ?? [position()],
    releases: new Map([["release-1", options.release ?? release()]]),
    preview: new Map([["release-1:position-1", disposition]]),
  });
}

describe("publication queue projection", () => {
  it("uses the planner create disposition as real manual readiness", () => {
    const [edition] = build({ disposition: "create" });
    expect(edition).toMatchObject({
      id: "publication-release-1",
      releaseId: "release-1",
      title: "Northern Lines",
      artist: "Arden Trio",
      year: "2024",
      reason: { code: "manualReady" },
    });
    expect(edition.nextStep).toEqual({
      kind: "track",
      href: "#/library/releases/release-1/positions/position-1?tab=publication&from=publication-queue",
    });
    expect(edition.tracks[0]).toEqual({
      id: "position-1",
      number: "1",
      title: "Still Water",
      href: "#/library/releases/release-1/positions/position-1?tab=publication&from=publication-queue",
      source: "inbox/01-still-water.flac",
    });
  });

  it("keeps source selection as an attention reason with a track action", () => {
    const [edition] = build({
      disposition: "blocked",
      reason: "source_selection_required",
    });
    expect(edition.reason).toEqual({
      code: "sourceNeedsSelection",
      trackId: "position-1",
      trackTitle: "Still Water",
    });
    expect(edition.tracks[0].attention).toBe("sourceNeedsSelection");
    expect(edition.tracks[0].nextStep).toEqual({
      kind: "track",
      href: "#/library/releases/release-1/positions/position-1?tab=publication&from=publication-queue",
    });
  });

  it("does not turn an unavailable path into an unsafe filename claim", () => {
    const [edition] = build({
      disposition: "blocked",
      reason: "publication_path_unavailable",
    });
    expect(edition.reason).toEqual({
      code: "publicationBlocked",
      trackId: "position-1",
      trackTitle: "Still Water",
    });
    expect(edition.tracks[0].attention).toBe("publicationBlocked");
  });

  it("never treats a path clash as ready", () => {
    const [edition] = build({ disposition: "create", path_clash: true });
    expect(edition.reason).toEqual({
      code: "publicationBlocked",
      trackId: "position-1",
      trackTitle: "Still Water",
    });
    expect(edition.tracks[0].attention).toBe("publicationBlocked");
  });

  it.each([
    ["missing_audio", "audioUnavailable"],
    ["publication_attempt_active", "publicationBlocked"],
  ] as const)("maps %s to %s", (reason, attention) => {
    const [edition] = build({ disposition: "blocked", reason });
    expect(edition.reason.code).toBe(attention);
    expect(edition.tracks[0].attention).toBe(attention);
  });

  it("treats a missing preview as blocked rather than ready", () => {
    const editions = buildPublicationEditions({
      positions: [position()],
      releases: new Map([["release-1", release()]]),
      preview: new Map(),
    });
    expect(editions[0].reason).toEqual({
      code: "publicationBlocked",
      trackId: "position-1",
      trackTitle: "Still Water",
    });
  });

  it("offers the missing primary artist when the planner cannot render the path", () => {
    const [edition] = build(
      { disposition: "blocked", reason: "publication_path_unavailable" },
      {
        release: release({
          primary_artist: {
            effective_artist_id: null,
            candidate_artist_ids: ["artist-1"],
          },
        }),
      },
    );
    expect(edition.reason).toEqual({ code: "missingPrimaryArtist" });
    expect(edition.artist).toBe("");
    expect(edition.nextStep).toEqual({
      kind: "primaryArtist",
      href: "#/library/releases/release-1?resolve=primary-artist&position=position-1",
    });

    const [ready] = build(
      { disposition: "create" },
      {
        release: release({
          primary_artist: {
            effective_artist_id: null,
            candidate_artist_ids: [],
          },
        }),
      },
    );
    expect(ready.reason).toEqual({ code: "manualReady" });
  });

  it("does not offer an artist picker if there is no candidate", () => {
    const [edition] = build(
      { disposition: "blocked", reason: "publication_path_unavailable" },
      {
        release: release({
          primary_artist: {
            effective_artist_id: null,
            candidate_artist_ids: [],
          },
        }),
      },
    );
    expect(edition.reason.code).toBe("publicationBlocked");
    expect(edition.nextStep.kind).toBe("track");
  });

  it.each([
    "update",
    "unchanged",
  ])("excludes positions that the planner now observes as current (%s)", (disposition) =>
    expect(build({ disposition })).toEqual([]));

  it("points the edition action at the attention track, not the first track", () => {
    const second = track({
      id: "position-2",
      title: "Second",
      position: 2,
      displayed_number: "2",
    });
    const [edition] = buildPublicationEditions({
      positions: [
        position(),
        position({
          id: "position-2",
          title: "Second",
          position: 2,
          displayed_number: "2",
        }),
      ],
      releases: new Map([
        ["release-1", release({ tracks: [track(), second] })],
      ]),
      preview: new Map([
        ["release-1:position-1", { disposition: "create" }],
        [
          "release-1:position-2",
          { disposition: "blocked", reason: "publication_context_blocked" },
        ],
      ]),
    });
    expect(edition.reason).toEqual({
      code: "publicationBlocked",
      trackId: "position-2",
      trackTitle: "Second",
    });
    expect(edition.nextStep).toEqual({
      kind: "track",
      href: "#/library/releases/release-1/positions/position-2?tab=publication&from=publication-queue",
    });
  });

  it("does not present an arbitrary linked variant as the selected source", () => {
    const [edition] = build(
      { disposition: "blocked", reason: "source_selection_required" },
      {
        release: release({
          tracks: [
            track({
              sources: [
                {
                  selected: false,
                  locations: [
                    {
                      relative_path: "inbox/unselected.flac",
                      probe_status: "audio",
                    },
                  ],
                },
              ],
            }),
          ],
        }),
      },
    );
    expect(edition.tracks[0].source).toBeUndefined();
    expect(edition.reason.code).toBe("sourceNeedsSelection");
  });

  it("keeps real one-based track numbers", () => {
    const [edition] = build(
      { disposition: "create" },
      {
        positions: [
          position({ position: 2, displayed_number: "", title: "Second" }),
        ],
        release: release({
          tracks: [
            track({ position: 2, displayed_number: "", title: "Second" }),
          ],
        }),
      },
    );
    expect(edition.tracks[0].number).toBe("2");
  });

  it("excludes positions without a linked source and tracks that are already current", () => {
    expect(
      build(
        { disposition: "create" },
        { positions: [position({ source_variant_count: 0 })] },
      ),
    ).toEqual([]);
    expect(
      build(
        { disposition: "create" },
        {
          release: release({
            tracks: [track({ publication: { epoch: "e" } })],
          }),
        },
      ),
    ).toEqual([]);
    expect(
      buildPublicationEditions({
        positions: [position()],
        releases: new Map(),
        preview: new Map([["release-1:position-1", { disposition: "create" }]]),
      }),
    ).toEqual([]);
  });
});
