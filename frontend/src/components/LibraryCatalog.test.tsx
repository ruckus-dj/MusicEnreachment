// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

function renderTracks(albumTracks: LibraryTrack[], onNavigate = vi.fn(), album = "id:album-1") {
  return render(
    <LibraryCatalog
      screen="tracks"
      artist="Artist"
      album={album}
      albumMissing={album === "album:"}
      artists={["Artist"]}
      artistTrackCounts={{ Artist: albumTracks.length }}
      albums={[]}
      albumTracks={albumTracks}
      loading={false}
      onNavigate={onNavigate}
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
    expect(document.body.textContent).toContain("Artist · Album");
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

  it("keeps track opening and album remapping as explicit actions without lyric content", () => {
    renderTracks([track()]);

    expect(screen.getAllByRole("button")).toHaveLength(3);
    expect(document.body.textContent).not.toMatch(/\[00:|\.lrc/);
  });

  it("opens album remapping from the selected album track list", () => {
    const onNavigate = vi.fn();
    renderTracks([track()], onNavigate);

    fireEvent.click(screen.getByRole("button", { name: "Сменить релиз" }));

    expect(onNavigate).toHaveBeenCalledWith({
      screen: "album-remap",
      artist: "Artist",
      artistMissing: false,
      album: "id:album-1",
      albumMissing: false,
    });
  });

  it("hides album remapping from the unfiltered all-tracks list", () => {
    // Given
    renderTracks([track()], vi.fn(), "");

    // When
    const remapAction = screen.queryByRole("button", { name: "Сменить релиз" });

    // Then
    expect(remapAction).toBeNull();
  });

  it("renders a pregap at position zero", () => {
    // Given
    renderTracks([track({ track_number: "0" })]);

    // When
    const pregap = screen.getByRole("button", { name: /Track/ });

    // Then
    expect(pregap.querySelector("b")?.textContent).toBe("0");
  });
});
