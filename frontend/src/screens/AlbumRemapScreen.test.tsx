// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AlbumRemapScreen } from "./AlbumRemapScreen";

type FetchRequest = {
  readonly path: string;
  readonly options: RequestInit | undefined;
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const sources = [
  {
    source_id: "source-1",
    record_id: "record-1",
    path: "/incoming/Artist/Source Album/01 Alpha.flac",
    sha256: "a".repeat(64),
    duration_seconds: 180,
    source_metadata_revision: 2,
    recording_mbid: null,
    release_mbid: null,
    canonical_tags: { TITLE: "Alpha", ALBUM: "Source Album", ALBUMARTIST: "Artist" },
  },
  {
    source_id: "source-2",
    record_id: "record-2",
    path: "/incoming/Artist/Source Album/02 Beta.flac",
    sha256: "b".repeat(64),
    duration_seconds: 190,
    source_metadata_revision: 2,
    recording_mbid: null,
    release_mbid: null,
    canonical_tags: { TITLE: "Beta", ALBUM: "Source Album", ALBUMARTIST: "Artist" },
  },
] as const;

const release = {
  release_mbid: "release-1",
  title: "Selected release",
  artist_credit: "Artist",
  date: "2024-01-01",
  country: "RU",
} as const;

const preview = {
  release_snapshot_token: "release-token",
  release,
  track_slots: [
    {
      track_mbid: "track-1",
      recording_mbid: "recording-1",
      medium_position: 1,
      track_position: 1,
      title: "First track",
      artist_credit: "Artist",
      duration_seconds: 180,
      assignable: true,
      suggested_source_id: "source-1",
    },
    {
      track_mbid: "track-2",
      recording_mbid: "recording-2",
      medium_position: 1,
      track_position: 2,
      title: "Second track",
      artist_credit: "Artist",
      duration_seconds: 190,
      assignable: true,
      suggested_source_id: null,
    },
    {
      track_mbid: "track-data",
      recording_mbid: "recording-data",
      medium_position: 2,
      track_position: 1,
      title: "Data track",
      artist_credit: "Artist",
      duration_seconds: 0,
      assignable: false,
      suggested_source_id: null,
    },
  ],
} as const;

function previewWithoutSuggestions() {
  return {
    ...preview,
    track_slots: preview.track_slots.map((slot) => ({ ...slot, suggested_source_id: null })),
  };
}

function queuedFetch(responses: readonly (Response | Promise<Response>)[]) {
  const requests: FetchRequest[] = [];
  let responseIndex = 0;
  const fetcher = vi.fn((input: RequestInfo | URL, options?: RequestInit) => {
    requests.push({ path: String(input), options });
    const response = responses.at(responseIndex) ?? jsonResponse({});
    responseIndex += 1;
    return Promise.resolve(response);
  });
  return { fetcher, requests };
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const response = new Promise<Response>((next) => {
    resolve = next;
  });
  return { response, resolve };
}

function renderScreen(album = "album:Source Album", artist = "Artist") {
  const onNavigate = vi.fn();
  const onNotice = vi.fn();
  render(
    <AlbumRemapScreen
      artist={artist}
      album={album}
      artistMissing={false}
      albumMissing={false}
      onNavigate={onNavigate}
      onNotice={onNotice}
    />,
  );
  return { onNavigate, onNotice };
}

async function chooseRelease(): Promise<void> {
  fireEvent.change(screen.getByLabelText("Поиск релиза MusicBrainz"), {
    target: { value: "Selected release" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Найти релиз" }));
  await screen.findByRole("button", { name: "Выбрать релиз Selected release" });
  fireEvent.click(screen.getByRole("button", { name: "Выбрать релиз Selected release" }));
  await screen.findByRole("heading", { name: "Сопоставление треков" });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AlbumRemapScreen", () => {
  it("uses a release selector immediately after navigation from an identified album", async () => {
    const releaseMbid = "11111111-1111-4111-8111-111111111111";
    const { fetcher, requests } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
    ]);
    vi.stubGlobal("fetch", fetcher);

    renderScreen(releaseMbid);

    await screen.findByRole("heading", { name: "Поиск релиза" });
    expect(screen.getByText("Source Album")).toBeTruthy();
    expect(screen.getByText("Artist · Файлов: 2")).toBeTruthy();
    expect(JSON.parse(String(requests[0]?.options?.body))).toEqual({
      selector: {
        release_mbid: releaseMbid,
        artist_name: null,
        album_name: null,
        artist_missing: false,
        album_missing: false,
      },
    });
  });

  it("uses an unscoped tag selector for an album opened from the global album list", async () => {
    const { fetcher, requests } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
    ]);
    vi.stubGlobal("fetch", fetcher);

    renderScreen("album:Source Album", "");

    await screen.findByRole("heading", { name: "Поиск релиза" });
    expect(JSON.parse(String(requests[0]?.options?.body))).toEqual({
      selector: {
        release_mbid: null,
        artist_name: null,
        album_name: "Source Album",
        artist_missing: false,
        album_missing: false,
      },
    });
  });

  it("submits a complete source partition when at least one file is assigned", async () => {
    const { fetcher, requests } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
      jsonResponse({
        assigned_source_ids: ["source-1"],
        unmatched_source_ids: ["source-2"],
        publication_refresh_queued: false,
        queued_release_artwork: true,
      }),
    ]);
    vi.stubGlobal("fetch", fetcher);
    const { onNotice } = renderScreen();

    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();
    expect(screen.getByText("01 Alpha.flac")).toBeTruthy();
    expect(screen.getByText("02 Beta.flac")).toBeTruthy();
    expect(screen.getByText("Служебный трек: не назначается")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /Применить сопоставление/ }));

    await waitFor(() => expect(requests).toHaveLength(4));
    expect(onNotice).toHaveBeenCalledWith("Сопоставление релиза применено.");
    expect(requests.map((request) => request.path)).toEqual([
      "/api/library/album-remaps/context",
      "/api/library/album-remaps/releases/search",
      "/api/library/album-remaps/preview",
      "/api/library/album-remaps/apply",
    ]);
    expect(JSON.parse(String(requests[0]?.options?.body))).toEqual({
      selector: {
        release_mbid: null,
        artist_name: "Artist",
        album_name: "Source Album",
        artist_missing: false,
        album_missing: false,
      },
    });
    expect(JSON.parse(String(requests[3]?.options?.body))).toEqual({
      selector: {
        release_mbid: null,
        artist_name: "Artist",
        album_name: "Source Album",
        artist_missing: false,
        album_missing: false,
      },
      album_snapshot_token: "album-token",
      release_mbid: "release-1",
      release_snapshot_token: "release-token",
      assignments: [{ source_id: "source-1", track_mbid: "track-1" }],
      unmatched_source_ids: ["source-2"],
    });
  });

  it("opens tracks for the selected release after applying a remap", async () => {
    // Given
    const { fetcher } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
      jsonResponse({
        assigned_source_ids: ["source-1"],
        unmatched_source_ids: ["source-2"],
        publication_refresh_queued: false,
        queued_release_artwork: false,
      }),
    ]);
    vi.stubGlobal("fetch", fetcher);
    const { onNavigate } = renderScreen();
    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();

    // When
    fireEvent.click(screen.getByRole("button", { name: /Применить сопоставление/ }));

    // Then
    await waitFor(() =>
      expect(onNavigate).toHaveBeenCalledWith({
        screen: "tracks",
        album: "id:release-1",
        artist: "",
        artistMissing: false,
        albumMissing: false,
      }),
    );
  });

  it("announces a publication refresh from its response field", async () => {
    // Given
    const { fetcher } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
      jsonResponse({
        assigned_source_ids: ["source-1"],
        unmatched_source_ids: ["source-2"],
        publication_refresh_queued: true,
        queued_release_artwork: false,
      }),
    ]);
    vi.stubGlobal("fetch", fetcher);
    const { onNotice } = renderScreen();
    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();

    // When
    fireEvent.click(screen.getByRole("button", { name: /Применить сопоставление/ }));

    // Then
    await waitFor(() =>
      expect(onNotice).toHaveBeenCalledWith(
        "Сопоставление применено; обновление публикации поставлено в очередь.",
      ),
    );
  });

  it("disables an empty source context without posting a no-op", async () => {
    // Given
    const { fetcher, requests } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources: [] }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
    ]);
    vi.stubGlobal("fetch", fetcher);
    renderScreen();
    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();
    const applyButton = screen.getByRole("button", { name: /Применить сопоставление/ });

    // When
    fireEvent.click(applyButton);

    // Then
    expect(
      screen.getByText("В контексте альбома нет исходных файлов для сопоставления."),
    ).toBeTruthy();
    expect(applyButton.hasAttribute("disabled")).toBe(true);
    expect(requests.map((request) => request.path)).toEqual([
      "/api/library/album-remaps/context",
      "/api/library/album-remaps/releases/search",
      "/api/library/album-remaps/preview",
    ]);
  });

  it("disables applying until at least one source is assigned", async () => {
    // Given
    const { fetcher, requests } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(previewWithoutSuggestions()),
    ]);
    vi.stubGlobal("fetch", fetcher);
    renderScreen();
    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();
    const applyButton = screen.getByRole("button", { name: /Применить сопоставление/ });

    // When
    fireEvent.click(applyButton);

    // Then
    expect(
      screen.getByText("Назначьте хотя бы один исходный файл, чтобы применить сопоставление."),
    ).toBeTruthy();
    expect(applyButton.hasAttribute("disabled")).toBe(true);
    expect(requests).toHaveLength(3);
  });

  it("supports click and native select reassignment while keeping data tracks unavailable", async () => {
    const { fetcher } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
    ]);
    vi.stubGlobal("fetch", fetcher);
    renderScreen();

    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();
    const betaTile = screen.getByRole("button", { name: "Выбрать файл 02 Beta.flac" });
    expect(betaTile.draggable).toBe(true);
    fireEvent.click(betaTile);
    fireEvent.click(
      screen.getByRole("button", { name: "Назначить файл треку 01.02 Second track" }),
    );
    expect(screen.getByRole("button", { name: /Применить сопоставление/ }).textContent).toContain(
      "2",
    );

    fireEvent.change(screen.getByLabelText(/Назначение .*Beta\.flac/), {
      target: { value: "" },
    });
    expect(screen.getByRole("button", { name: /Применить сопоставление/ }).textContent).toContain(
      "1",
    );
    const dataTransfer = {
      effectAllowed: "",
      getData: () => "source-2",
      setData: vi.fn(),
    };
    const betaTileAfterUnassign = screen.getByRole("button", { name: "Выбрать файл 02 Beta.flac" });
    const secondTrackTarget = screen.getByRole("button", {
      name: "Назначить файл треку 01.02 Second track",
    });
    const secondTrackSlot = screen
      .getByText("Перетащите файл или выберите его слева.")
      .closest("li");
    expect(secondTrackTarget.hasAttribute("disabled")).toBe(true);
    if (!secondTrackSlot) throw new Error("Second track slot was not found.");
    fireEvent.dragStart(betaTileAfterUnassign, { dataTransfer });
    fireEvent.dragOver(secondTrackSlot, { dataTransfer });
    fireEvent.drop(secondTrackSlot, { dataTransfer });
    expect(dataTransfer.setData).toHaveBeenCalledWith("text/plain", "source-2");
    expect(screen.getByRole("button", { name: /Применить сопоставление/ }).textContent).toContain(
      "2",
    );
    expect(
      screen
        .getByRole("button", { name: "Назначить файл треку 02.01 Data track" })
        .hasAttribute("disabled"),
    ).toBe(true);
  });

  it("locks release and mapping controls while apply is pending", async () => {
    const pendingApply = deferredResponse();
    const { fetcher } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
      pendingApply.response,
    ]);
    vi.stubGlobal("fetch", fetcher);
    const { onNotice } = renderScreen();
    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();

    fireEvent.click(screen.getByRole("button", { name: /Применить сопоставление/ }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Применяем…" }).hasAttribute("disabled")).toBe(
        true,
      ),
    );
    expect(screen.getByLabelText("Поиск релиза MusicBrainz").hasAttribute("disabled")).toBe(true);
    expect(
      screen
        .getByRole("button", { name: "Выбрать релиз Selected release" })
        .hasAttribute("disabled"),
    ).toBe(true);
    expect(
      screen
        .getAllByRole("button", { name: /Выбрать файл/ })
        .every((button) => button.hasAttribute("disabled")),
    ).toBe(true);
    expect(screen.getAllByRole("combobox").every((select) => select.hasAttribute("disabled"))).toBe(
      true,
    );

    pendingApply.resolve(
      jsonResponse({
        assigned_source_ids: ["source-1"],
        unmatched_source_ids: ["source-2"],
        publication_refresh_queued: false,
        queued_release_artwork: false,
      }),
    );
    await waitFor(() => expect(onNotice).toHaveBeenCalledWith("Сопоставление релиза применено."));
  });

  it("keeps the workspace open with an actionable stale snapshot error", async () => {
    const { fetcher } = queuedFetch([
      jsonResponse({ album_snapshot_token: "album-token", sources }),
      jsonResponse({ releases: [release] }),
      jsonResponse(preview),
      jsonResponse({ detail: "stale snapshot" }, 409),
    ]);
    vi.stubGlobal("fetch", fetcher);
    const { onNavigate } = renderScreen();

    await screen.findByRole("heading", { name: "Поиск релиза" });
    await chooseRelease();
    fireEvent.click(screen.getByRole("button", { name: /Применить сопоставление/ }));

    expect((await screen.findByRole("alert")).textContent).toContain("Обновите контекст альбома");
    expect(onNavigate).not.toHaveBeenCalled();
  });
});
