// @vitest-environment jsdom

import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { useAppController } from "./useAppController";

afterEach(() => {
  cleanup();
  window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function ControllerProbe() {
  const controller = useAppController();
  return (
    <>
      <button type="button" onClick={() => void controller.selectEffectiveSource("source-b")}>
        choose
      </button>
      <button
        type="button"
        onClick={() =>
          void controller.overrideRecording({
            recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          })
        }
      >
        correct
      </button>
      <output data-testid="effective-source-id">{controller.effectiveSourceId}</output>
      <output data-testid="effective-source-success">{controller.effectiveSourceSuccess}</output>
      <output data-testid="notice">{controller.notice}</output>
      <output data-testid="correction-error">{controller.recordingCorrectionError}</output>
      <output data-testid="correction-review">{controller.recordingCorrectionReview}</output>
    </>
  );
}

function CatalogProbe({
  albumRoute = "release-collision-course",
}: {
  readonly albumRoute?: string;
}) {
  const controller = useAppController();
  const selectedAlbumRoute = {
    screen: "tracks" as const,
    artist: controller.artist,
    album: albumRoute,
  };
  return (
    <>
      <button
        type="button"
        onClick={() => controller.navigate({ screen: "albums", artist: "Busta Rhymes" })}
      >
        busta
      </button>
      <button
        type="button"
        onClick={() => controller.navigate({ screen: "albums", artist: "Linkin Park" })}
      >
        linkin
      </button>
      <button type="button" onClick={() => controller.navigate(selectedAlbumRoute)}>
        album
      </button>
      <output data-testid="artists">{controller.artists.join("|")}</output>
      <output data-testid="albums">{controller.albums.map(({ title }) => title).join("|")}</output>
      <output data-testid="album-tracks">{controller.albumTracks.length}</output>
      <output data-testid="album-track-states">
        {controller.albumTracks
          .map((track) => `${track.source_state}/${track.processing_state}/${track.match_state}`)
          .join("|")}
      </output>
    </>
  );
}

describe("useAppController error messages", () => {
  it.each([
    [new Error("Network unavailable"), "Network unavailable"],
    [new Error(""), ""],
    [new ApiError(500, "Ответ сервера"), "Ответ сервера"],
    [null, "Не удалось загрузить медиатеку"],
    ["failure", "Не удалось загрузить медиатеку"],
  ])("preserves library error or fallback for %s", async (failure, expected) => {
    window.history.replaceState({}, "", "/library");
    vi.spyOn(globalThis, "fetch").mockRejectedValue(failure);
    render(<ControllerProbe />);
    await act(async () => {});
    expect(screen.getByTestId("notice").textContent).toBe(expected);
  });

  it.each([
    [409, "Исправление конфликтует с сохранёнными свидетельствами провайдера."],
    [422, "Проверьте MBID записи."],
    [503, "MusicBrainz временно недоступен. Повторите исправление позже."],
    [404, "Выбранный источник записи больше недоступен. Обновите данные трека."],
    [500, "Не удалось отправить исправление записи."],
  ])("preserves correction feedback for HTTP %i", async (status, expected) => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/musicbrainz/override")) {
        return Response.json({ detail: "Server detail" }, { status });
      }
      if (String(input) === "/api/library/records/record-1") {
        return Response.json({
          record_id: "record-1",
          sources: [
            {
              source_id: "source-a",
              path: "/track.flac",
              sha256: "a",
              state: "present",
              tag_observations: [],
            },
          ],
          publications: [],
          events: [],
        });
      }
      return Response.json({ items: [] });
    });
    render(<ControllerProbe />);
    await act(async () => {
      screen.getByRole("button", { name: "correct" }).click();
    });
    expect(screen.getByTestId("correction-error").textContent).toBe(expected);
    expect(screen.getByTestId("correction-review").textContent).toBe(
      status === 409
        ? "Требуется проверка исправления записи. Сверьте свидетельства и повторите позже."
        : "",
    );
    expect(screen.getByTestId("notice").textContent).toBe(
      status === 409
        ? "Исправление не применено: требуется проверка конфликта."
        : "Исправление записи не применено.",
    );
  });
});

describe("useAppController effective source", () => {
  it("posts the choice and refreshes the record and library state", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    const detail = {
      record_id: "record-1",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      musicbrainz_release_id: "release-collision-course",
      sources: [
        { source_id: "source-a", path: "/old.flac", sha256: "a", state: "present" },
        { source_id: "source-b", path: "/new.flac", sha256: "b", state: "present" },
      ],
      publications: [],
      states: {
        source: "present",
        processing: "complete",
        match: "matched",
        publication: "current",
        metadata: "final",
      },
      events: [],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/api/library/records" && !init?.method)
        return Response.json({ items: [detail] });
      if (url === "/api/library/records/record-1" && !init?.method) return Response.json(detail);
      if (url === "/api/library/records/record-1/effective-source") {
        expect(JSON.parse(String(init?.body))).toEqual({ source_id: "source-b" });
        return Response.json({
          source_id: "source-b",
          baseline_source_id: "source-b",
          policy_version: "quality-policy-v1",
        });
      }
      return Response.json({ items: [detail] });
    });

    render(<ControllerProbe />);
    await waitFor(() =>
      expect(screen.getByTestId("effective-source-id").textContent).toBe("source-a"),
    );

    await act(async () => {
      screen.getByRole("button", { name: "choose" }).click();
    });

    await waitFor(() => {
      expect(screen.getByTestId("effective-source-id").textContent).toBe("source-b");
      expect(screen.getByTestId("effective-source-success").textContent).toContain("выбран");
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/library/records/record-1/effective-source",
      expect.objectContaining({ method: "POST" }),
    );
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
  });
});

describe("useAppController worker queue", () => {
  it("refreshes the open worker queue every two seconds", async () => {
    vi.useFakeTimers();
    window.history.replaceState({}, "", "/workers");
    const queue = {
      observed_at: "2026-08-14T10:00:00+00:00",
      worker: { configured_concurrency: 1, liveness: "available", slots: [] },
      summary: { running: 0, ready: 0, retry_wait: 0 },
      total_jobs: 0,
      jobs: [],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/workers/queue") return Response.json(queue);
      return Response.json({ items: [] });
    });

    render(<ControllerProbe />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_000);
    });

    expect(
      fetchMock.mock.calls.filter(([input]) => String(input) === "/api/workers/queue"),
    ).toHaveLength(3);
  });
});

describe("useAppController recording correction", () => {
  it("posts only the recording MBID, then refreshes the selected source record", async () => {
    const detail = {
      record_id: "record-1",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      sources: [{ source_id: "source-a", path: "/track.flac", sha256: "a", state: "present" }],
      publications: [],
      states: {
        source: "present",
        processing: "complete",
        match: "matched",
        publication: "current",
        metadata: "final",
      },
      events: [],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/api/library/records" && !init?.method)
        return Response.json({ items: [detail] });
      if (url === "/api/library/records/record-1" && !init?.method) return Response.json(detail);
      if (url.endsWith("/musicbrainz/override")) {
        expect(JSON.parse(String(init?.body))).toEqual({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
        });
        return Response.json({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          record_id: "record-1",
        });
      }
      return Response.json({ items: [detail] });
    });

    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    render(<ControllerProbe />);
    await act(async () => {
      screen.getByRole("button", { name: "correct" }).click();
    });

    await waitFor(() => expect(screen.getByTestId("notice").textContent).toContain("исправлена"));
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
  });
});

describe("useAppController catalog", () => {
  it("lists each semicolon-separated album artist with the same album and tracks", async () => {
    const _sharedAlbum = {
      record_id: "record-collaboration",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      musicbrainz_release_id: "release-collision-course",
      sources: [
        {
          source_id: "source-collaboration",
          path: "/collaboration.flac",
          sha256: "c",
          state: "present",
          tag_observations: [
            { name: "ARTIST", value: "Featured Guest", format: "flac" },
            { name: "ALBUMARTIST", value: "Busta Rhymes; Linkin Park", format: "flac" },
            { name: "ALBUM", value: "Collision Course", format: "flac" },
          ],
        },
      ],
      publications: [],
    };
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.startsWith("/api/library/albums")) {
        return Response.json({
          items: [
            {
              album_id: "release-collision-course",
              album_name: "Collision Course",
              track_count: 1,
            },
          ],
        });
      }
      if (url.startsWith("/api/library/tracks")) {
        return Response.json({
          items: [
            {
              record_id: "record-collaboration",
              source_id: "source-collaboration",
              artist_name: url.includes("Linkin+Park") ? "Linkin Park" : "Busta Rhymes",
              album_name: "Collision Course",
              album_id: "release-collision-course",
              title: "Track",
              track_number: "1",
              source_state: "disappeared",
              processing_state: "analyzing",
              match_state: "unmatched",
              publication_state: "current",
            },
          ],
        });
      }
      return Response.json({ items: [{ name: "Busta Rhymes" }, { name: "Linkin Park" }] });
    });

    window.history.replaceState({}, "", "/library/artists");
    render(<CatalogProbe />);

    await waitFor(() =>
      expect(screen.getByTestId("artists").textContent).toBe("Busta Rhymes|Linkin Park"),
    );

    await act(async () => {
      screen.getByRole("button", { name: "busta" }).click();
    });
    await waitFor(() => {
      expect(screen.getByTestId("albums").textContent).toBe("Collision Course");
    });
    await act(async () => {
      screen.getByRole("button", { name: "album" }).click();
    });
    await waitFor(() => {
      expect(screen.getByTestId("album-tracks").textContent).toBe("1");
      expect(screen.getByTestId("album-track-states").textContent).toBe(
        "disappeared/analyzing/unmatched",
      );
    });

    await act(async () => {
      screen.getByRole("button", { name: "linkin" }).click();
    });
    await waitFor(() => {
      expect(screen.getByTestId("albums").textContent).toBe("Collision Course");
    });
    await act(async () => {
      screen.getByRole("button", { name: "album" }).click();
    });
    await waitFor(() => {
      expect(screen.getByTestId("album-tracks").textContent).toBe("1");
    });
  });

  it("groups same-named albums when release MBID is missing", async () => {
    const firstTrack = {
      record_id: "record-without-release-a",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      sources: [
        {
          source_id: "source-without-release-a",
          path: "/track-a.flac",
          sha256: "a",
          state: "present",
          tag_observations: [
            { name: "ALBUMARTIST", value: "Busta Rhymes", format: "flac" },
            { name: "ALBUM", value: "Collision Course", format: "flac" },
          ],
        },
      ],
      publications: [],
    };
    const secondTrack = {
      ...firstTrack,
      record_id: "record-without-release-b",
      sources: [
        {
          ...firstTrack.sources[0],
          source_id: "source-without-release-b",
          path: "/track-b.flac",
        },
      ],
    };
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.startsWith("/api/library/albums")) {
        return Response.json({
          items: [{ album_id: null, album_name: "Collision Course", track_count: 2 }],
        });
      }
      if (url.startsWith("/api/library/tracks")) {
        return Response.json({
          items: [firstTrack, secondTrack].map((track) => ({
            record_id: track.record_id,
            source_id: track.sources[0].source_id,
            artist_name: "Busta Rhymes",
            album_name: "Collision Course",
            album_id: null,
            title: "Track",
            track_number: null,
            source_state: "present",
            processing_state: "complete",
            match_state: "matched",
            publication_state: "current",
          })),
        });
      }
      return Response.json({ items: [{ name: "Busta Rhymes" }] });
    });

    window.history.replaceState({}, "", "/library/artists");
    render(<CatalogProbe albumRoute="record:record-without-release-a" />);

    await act(async () => {
      screen.getByRole("button", { name: "busta" }).click();
    });
    await waitFor(() => expect(screen.getByTestId("albums").textContent).toBe("Collision Course"));

    await act(async () => {
      screen.getByRole("button", { name: "album" }).click();
    });
    await waitFor(() => expect(screen.getByTestId("album-tracks").textContent).toBe("2"));
  });

  it("removes the route id marker before requesting album tracks", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async () => Response.json({ items: [] }));
    window.history.replaceState(
      {},
      "",
      "/library/tracks?artist_name=Busta%20Rhymes&album_id=0f481339-f7bb-40b4-ab4a-f24c1c2a7009",
    );

    render(<CatalogProbe />);

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([request]) =>
          String(request).includes("album_id=0f481339-f7bb-40b4-ab4a-f24c1c2a7009"),
        ),
      ).toBe(true),
    );
  });
});

function SettingsProbe() {
  const controller = useAppController();
  const draft = controller.settingsDraft;
  return (
    <>
      <button type="button" onClick={() => void controller.saveSettings()}>
        save-settings
      </button>
      <button
        type="button"
        onClick={() =>
          draft
            ? controller.setSettingsDraft({
                ...draft,
                lrclib_host: "https://mirror.lrclib.net",
                lrclib_max_attempts: 5,
              })
            : undefined
        }
      >
        edit-settings
      </button>
      <output data-testid="settings-host">{draft?.lrclib_host ?? ""}</output>
      <output data-testid="settings-enabled">{String(draft?.lrclib_enabled ?? "")}</output>
      <output data-testid="settings-notice">{controller.notice}</output>
    </>
  );
}

const settingsResponse = {
  confidence_threshold: 0.8,
  timeout_seconds: 30,
  retry_delay_seconds: 10,
  max_attempts: 3,
  worker_pools: {
    filesystem_scan: 4,
    acoustid_analysis: 1,
    musicbrainz_analysis: 4,
    candidate_selection: 4,
    folder_release_selection: 2,
    final_publish: 4,
    selection_refresh: 4,
    lrclib_fetch: 1,
    artwork_enrichment: 2,
    reconciliation_scan: 1,
  },
  musicbrainz_enabled: true,
  musicbrainz_user_agent: "Music Ingest",
  musicbrainz_host: "https://musicbrainz.org",
  musicbrainz_request_delay_seconds: 1.5,
  acoustid_enabled: false,
  acoustid_request_delay_seconds: 1 / 3,
  acoustid_client_key_configured: false,
  artwork_enabled: true,
  lrclib_enabled: true,
  lrclib_host: "https://lrclib.net",
  lrclib_user_agent: "Music Ingest",
  lrclib_timeout_seconds: 15,
  lrclib_max_attempts: 3,
  lrclib_request_delay_seconds: 0.3,
  lrclib_max_response_bytes: 4_194_304,
  lrclib_match_confidence_threshold: 0.7,
};

describe("useAppController runtime settings", () => {
  it("loads every runtime setting and saves the complete LRCLIB payload", async () => {
    window.history.replaceState({}, "", "/settings");
    const putBodies: Record<string, unknown>[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (String(input) !== "/api/settings") return Response.json({ items: [] });
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        putBodies.push(body);
        return Response.json({ ...settingsResponse, ...body });
      }
      return Response.json(settingsResponse);
    });

    render(<SettingsProbe />);
    await waitFor(() =>
      expect(screen.getByTestId("settings-host").textContent).toBe("https://lrclib.net"),
    );
    expect(screen.getByTestId("settings-enabled").textContent).toBe("true");

    await act(async () => {
      screen.getByRole("button", { name: "edit-settings" }).click();
    });
    await act(async () => {
      screen.getByRole("button", { name: "save-settings" }).click();
    });

    const { acoustid_client_key_configured: _configured, ...expected } = settingsResponse;
    expect(putBodies).toEqual([
      {
        ...expected,
        acoustid_client_key: null,
        lrclib_host: "https://mirror.lrclib.net",
        lrclib_max_attempts: 5,
      },
    ]);
    expect(screen.getByTestId("settings-notice").textContent).toBe("Настройки сохранены");
  });

  it("fills missing provider fields from defaults so a save is never partial", async () => {
    window.history.replaceState({}, "", "/settings");
    const putBodies: Record<string, unknown>[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      if (String(input) !== "/api/settings") return Response.json({ items: [] });
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        putBodies.push(body);
        return Response.json({ ...settingsResponse, ...body });
      }
      return Response.json({ confidence_threshold: 0.9 });
    });

    render(<SettingsProbe />);
    await waitFor(() =>
      expect(screen.getByTestId("settings-host").textContent).toBe("https://lrclib.net"),
    );

    await act(async () => {
      screen.getByRole("button", { name: "save-settings" }).click();
    });

    expect(putBodies).toHaveLength(1);
    for (const key of [
      "confidence_threshold",
      "timeout_seconds",
      "retry_delay_seconds",
      "max_attempts",
      "worker_pools",
      "musicbrainz_enabled",
      "musicbrainz_user_agent",
      "musicbrainz_host",
      "musicbrainz_request_delay_seconds",
      "acoustid_enabled",
      "acoustid_request_delay_seconds",
      "acoustid_client_key",
      "artwork_enabled",
      "lrclib_enabled",
      "lrclib_host",
      "lrclib_user_agent",
      "lrclib_timeout_seconds",
      "lrclib_max_attempts",
      "lrclib_request_delay_seconds",
      "lrclib_max_response_bytes",
    ])
      expect(putBodies[0]).toHaveProperty(key);
    expect(putBodies[0].confidence_threshold).toBe(0.9);
    expect(putBodies[0].lrclib_max_response_bytes).toBe(4_194_304);
  });
});
