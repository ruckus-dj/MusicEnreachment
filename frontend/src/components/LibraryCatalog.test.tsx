// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LibraryTrack } from "../api/client";
import { LibraryCatalog } from "./LibraryCatalog";

afterEach(cleanup);

function track(overrides: Partial<LibraryTrack> = {}): LibraryTrack {
  return {
    record_id: "record-1",
    source_id: "source-a",
    source_path: "/incoming/artist/album/01 track.flac",
    artist_name: "Artist",
    album_name: "Album",
    album_id: "album-1",
    title: "Track",
    track_number: "1",
    source_state: "present",
    processing_state: "complete",
    match_state: "matched",
    publication_state: "current",
    lyrics_status: "synced",
    lyrics_synced: true,
    ...overrides,
  };
}

function renderTracks(albumTracks: LibraryTrack[]) {
  return render(
    <LibraryCatalog
      screen="tracks"
      artist="Artist"
      album="album-1"
      artists={["Artist"]}
      artistTrackCounts={{ Artist: albumTracks.length }}
      albums={[]}
      albumTracks={albumTracks}
      loading={false}
      onNavigate={vi.fn()}
      onRefresh={vi.fn()}
    />,
  );
}

describe("LibraryCatalog lyrics indication", () => {
  it("marks a track with synchronized lyrics by text, not color alone", () => {
    renderTracks([track()]);

    const flag = screen.getByTestId("track-lyrics-flag");
    expect(flag.tagName).toBe("SPAN");
    expect(flag.getAttribute("data-lyrics-status")).toBe("synced");
    expect(flag.textContent).toContain("Синхронный текст: есть");
    expect(flag.className).toContain("ready");
    expect(flag.getAttribute("title")).toContain("Синхронный текст найден");
  });

  it("marks tracks without synchronized lyrics for pending and rejected candidates", () => {
    renderTracks([
      track({ lyrics_status: "pending", lyrics_synced: false }),
      track({
        record_id: "record-2",
        source_id: "source-b",
        title: "Other",
        lyrics_status: "validation_rejected",
        lyrics_synced: false,
      }),
      track({
        record_id: "record-3",
        source_id: "source-c",
        title: "Third",
        lyrics_status: "error",
        lyrics_synced: false,
      }),
    ]);

    const flags = screen.getAllByTestId("track-lyrics-flag");
    expect(flags.map((flag) => flag.getAttribute("data-lyrics-status"))).toEqual([
      "pending",
      "validation_rejected",
      "error",
    ]);
    for (const flag of flags) {
      expect(flag.textContent).toContain("Синхронный текст: нет");
      expect(flag.className).not.toContain("ready");
    }
    expect(flags[0].className).toContain("pending");
    expect(flags[1].className).toContain("error");
  });

  it("keeps the row button as the only interactive control and renders no lyric content", () => {
    renderTracks([track()]);

    expect(screen.getAllByRole("button")).toHaveLength(2);
    expect(document.body.textContent).not.toMatch(/\[00:|\.lrc/);
  });
});
