// @vitest-environment jsdom

import { act, cleanup, render, renderHook, screen, waitFor } from "@testing-library/react";
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
          void controller.loadMusicBrainzCandidates({
            recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          })
        }
      >
        correct
      </button>
      <button
        type="button"
        onClick={() =>
          void controller.loadMusicBrainzCandidates({
            recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
            release_mbid: "release-id",
          })
        }
      >
        load-release
      </button>
      <output data-testid="record-id">{controller.recordId}</output>
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
    [409, "MusicBrainz не подтвердил связь recording с указанным release."],
    [422, "Проверьте MBID recording и release."],
    [503, "MusicBrainz временно недоступен. Повторите поиск позже."],
    [404, "Выбранный источник записи больше недоступен. Обновите данные трека."],
    [500, "Не удалось загрузить кандидатов MusicBrainz."],
  ])("preserves correction feedback for HTTP %i", async (status, expected) => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/musicbrainz/release-candidates")) {
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
        ? "Уберите release MBID, чтобы загрузить все связанные релизы, или проверьте оба идентификатора."
        : "",
    );
    expect(screen.getByTestId("notice").textContent).toBe(
      status === 409
        ? "Кандидаты не добавлены: MusicBrainz не подтвердил пару."
        : "Кандидаты MusicBrainz не загружены.",
    );
  });
});

describe("useAppController manual MusicBrainz identifiers", () => {
  it("loads a validated recording-release pair through the candidates endpoint", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/musicbrainz/release-candidates")) {
        return Response.json({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          release_mbid: "release-id",
          status: "review_required",
          candidate_count: 1,
        });
      }
      if (String(input) === "/api/library/records/record-1") {
        return Response.json(encodingRecord());
      }
      return Response.json({ items: [] });
    });

    render(<ControllerProbe />);
    await act(async () => {
      screen.getByRole("button", { name: "load-release" }).click();
    });

    expect(fetcher).toHaveBeenCalledWith(
      "/api/library/records/record-1/sources/source-a/musicbrainz/release-candidates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          release_mbid: "release-id",
        }),
      }),
    );
    expect(screen.getByTestId("notice").textContent).toContain("добавлена для проверки");
  });

  it("loads every release candidate without changing the current route", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/musicbrainz/release-candidates")) {
        return Response.json({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          release_mbid: null,
          status: "review_required",
          candidate_count: 2,
        });
      }
      if (String(input) === "/api/library/records/record-1") {
        return Response.json(encodingRecord());
      }
      return Response.json({ items: [] });
    });

    const { result } = renderHook(() => useAppController());
    await act(async () => {
      await result.current.loadMusicBrainzCandidates({
        recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
      });
    });

    expect(result.current.recordId).toBe("record-1");
    expect(result.current.notice).toContain("2");
    expect(window.location.pathname).toBe("/library/record/record-1/source/source-a");
    expect(window.location.search).toBe("");
  });
});

describe("useAppController bulk metadata refresh", () => {
  it("queues known MusicBrainz identities and reports the count", async () => {
    window.history.replaceState({}, "", "/library");
    const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/library/metadata/refresh") {
        return Response.json({ queued: 3 });
      }
      return Response.json({ items: [], total_track_count: 0 });
    });
    const { result } = renderHook(() => useAppController());

    await act(async () => {
      await result.current.refreshMetadata();
    });

    expect(fetcher).toHaveBeenCalledWith(
      "/api/library/metadata/refresh",
      expect.objectContaining({ method: "POST" }),
    );
    expect(result.current.notice).toBe("Обновление метаданных поставлено в очередь: 3");
    expect(result.current.refreshingMetadata).toBe(false);
  });

  it("reports a fallback when metadata refresh cannot be queued", async () => {
    window.history.replaceState({}, "", "/library");
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/library/metadata/refresh") throw null;
      return Response.json({ items: [], total_track_count: 0 });
    });
    const { result } = renderHook(() => useAppController());

    await act(async () => {
      await result.current.refreshMetadata();
    });

    expect(result.current.notice).toBe("Не удалось обновить метаданные MusicBrainz");
    expect(result.current.refreshingMetadata).toBe(false);
  });
});

function encodingRecord(recordId = "record-1", sourceId = "source-a", title = "Saved") {
  return {
    record_id: recordId,
    sources: [{ source_id: sourceId, path: "/track.flac", sha256: "a", state: "present" }],
    publications: [],
    metadata_revisions: [{ source_id: sourceId, layer: "final", tags: { TITLE: title } }],
    events: [],
  };
}

describe("useAppController source encoding refresh", () => {
  it("preserves unsaved Final through queued encoding polling and job completion", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    let completed = false;
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/library/records/record-1") {
        return Response.json({
          ...encodingRecord(),
          states: { processing: completed ? "complete" : "analyzing", publication: "current" },
          events: completed ? [{ kind: "encoding-completed" }] : [],
        });
      }
      return Response.json({ items: [] });
    });
    const { result } = renderHook(() => useAppController());
    await act(async () => {});
    act(() => result.current.setDraft({ TITLE: "Unsaved edit" }));
    await act(async () => result.current.encodingApplied(true));
    expect(Object.keys(result.current.watchedRecords)).toHaveLength(1);
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
    expect(result.current.watchedRecords["record-1:source-a"].sawPending).toBe(true);
    completed = true;
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.detail?.events).toEqual([{ kind: "encoding-completed" }]);
    expect(result.current.items[0]).toEqual(result.current.detail);
    expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
    expect(result.current.watchedRecords).toEqual({});
    expect(
      fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/record-1")).length,
    ).toBeGreaterThanOrEqual(4);
  });

  it.each(["complete", "error"])(
    "ignores stale queued polling %s after navigation and keeps polling the current route",
    async (outcome) => {
      vi.useFakeTimers();
      vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
      window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
      let resolvePoll: (response: Response) => void = () => {};
      let rejectPoll: (error: Error) => void = () => {};
      const pending = new Promise<Response>((resolve, reject) => {
        resolvePoll = resolve;
        rejectPoll = reject;
      });
      const deferred = { promise: pending, resolve: resolvePoll, reject: rejectPoll };
      let polling = false;
      vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
        if (String(input) === "/api/library/records/record-1") {
          if (polling) {
            polling = false;
            return deferred.promise;
          }
          return Response.json({
            ...encodingRecord(),
            states: { processing: "complete", publication: "current" },
            events: [{ kind: "A completed" }],
          });
        }
        if (String(input) === "/api/library/records/record-2") {
          return Response.json({
            ...encodingRecord("record-2", "source-b", "B saved"),
            states: { processing: "complete", publication: "current" },
            events: [{ kind: "B completed" }],
          });
        }
        return Response.json({ items: [] });
      });
      const { result } = renderHook(() => useAppController());
      await act(async () => {});
      await act(async () => result.current.encodingApplied(true));
      polling = true;
      await act(async () => vi.advanceTimersByTimeAsync(1_000));
      await act(async () =>
        result.current.navigate({ screen: "track", recordId: "record-2", sourceId: "source-b" }),
      );
      act(() => {
        result.current.setDraft({ TITLE: "B unsaved" });
        result.current.setNotice("B notice");
      });
      await act(async () => {
        if (outcome === "error") deferred.reject(new Error("A stale failure"));
        else
          deferred.resolve(
            Response.json({
              ...encodingRecord(),
              states: { processing: "complete", publication: "current" },
              events: [{ kind: "A completed" }],
            }),
          );
      });
      expect(result.current.detail?.record_id).toBe("record-2");
      expect(result.current.items[0]?.record_id).toBe("record-2");
      expect(result.current.draft).toEqual({ TITLE: "B unsaved" });
      expect(result.current.notice).toBe("B notice");
      // A watch remains useful in the background, but cannot surface A errors on B.
      await act(async () => result.current.encodingApplied(true));
      await act(async () => vi.advanceTimersByTimeAsync(2_000));
      expect(result.current.detail?.record_id).toBe("record-2");
      expect(result.current.draft).toEqual({ TITLE: "B unsaved" });
      expect(result.current.watchedRecords["record-2:source-b"]).toBeUndefined();
    },
  );

  it.each(["complete", "error"])(
    "ignores an older queued poll %s after a newer encoding refresh on the same route",
    async (outcome) => {
      vi.useFakeTimers();
      vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
      window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
      let resolvePoll: (response: Response) => void = () => {};
      let rejectPoll: (error: Error) => void = () => {};
      const pending = new Promise<Response>((resolve, reject) => {
        resolvePoll = resolve;
        rejectPoll = reject;
      });
      let deferNext = false;
      let version = "initial";
      vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
        if (String(input) !== "/api/library/records/record-1") return Response.json({ items: [] });
        if (deferNext) {
          deferNext = false;
          return pending;
        }
        return Response.json({
          ...encodingRecord(),
          states: { processing: "analyzing", publication: "current" },
          events: [{ kind: version }],
        });
      });
      const { result } = renderHook(() => useAppController());
      await act(async () => {});
      await act(async () => result.current.encodingApplied(true));
      deferNext = true;
      await act(async () => vi.advanceTimersByTimeAsync(1_000));
      version = "new encoding";
      await act(async () => result.current.encodingApplied(true));
      act(() => {
        result.current.setDraft({ TITLE: "Unsaved edit" });
        result.current.setNotice("New notice");
      });
      await act(async () => {
        if (outcome === "error") rejectPoll(new Error("Old error"));
        else
          resolvePoll(
            Response.json({
              ...encodingRecord(),
              states: { processing: "complete", publication: "current" },
              events: [{ kind: "old encoding" }],
            }),
          );
      });
      expect(result.current.detail?.events).toEqual([{ kind: "new encoding" }]);
      expect(result.current.items[0]).toEqual(result.current.detail);
      expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
      expect(result.current.notice).toBe("New notice");
      expect(Object.keys(result.current.watchedRecords)).toHaveLength(1);
      await act(async () => vi.advanceTimersByTimeAsync(1_000));
      expect(result.current.watchedRecords["record-1:source-a"].sawPending).toBe(true);
    },
  );

  it("keeps Final intact during scan completion and subsequent library polling", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    let version = "initial";
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/api/reconciliation/scan") return Response.json({ job_id: "scan-1" });
      if (url === "/api/reconciliation/scan/scan-1") {
        return Response.json({
          state: "completed",
          result: { added: 1, changed: 0, moved: 0, removed: 0, queued_jobs: 1 },
        });
      }
      if (url === "/api/library/records/record-1") {
        return Response.json({ ...encodingRecord(), events: [{ kind: version }] });
      }
      return Response.json({ items: [] });
    });
    const { result } = renderHook(() => useAppController());
    await act(async () => {});
    act(() => result.current.setDraft({ TITLE: "Unsaved edit" }));
    await act(async () => result.current.scan());
    version = "scan completed";
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.scanning).toBe(false);
    expect(result.current.detail?.events).toEqual([{ kind: "scan completed" }]);
    expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
    expect(result.current.watchedLibraryUntil).toBeGreaterThan(Date.now());
    version = "background progress";
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.detail?.events).toEqual([{ kind: "background progress" }]);
    expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
  });

  it("ignores record A readback when navigation to B finishes while refresh is pending", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    let refreshing = false;
    let resolveRefresh: (response: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      resolveRefresh = resolve;
    });
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/library/records/record-1") {
        return refreshing ? pending : Response.json(encodingRecord());
      }
      if (String(input) === "/api/library/records/record-2") {
        return Response.json(encodingRecord("record-2", "source-b", "B saved"));
      }
      return Response.json({ items: [] });
    });
    const { result } = renderHook(() => useAppController());
    await waitFor(() => expect(result.current.detail?.record_id).toBe("record-1"));
    refreshing = true;
    let refresh: Promise<void> = Promise.resolve();
    act(() => {
      refresh = result.current.encodingApplied(false);
    });
    act(() =>
      result.current.navigate({ screen: "track", recordId: "record-2", sourceId: "source-b" }),
    );
    await waitFor(() => expect(result.current.detail?.record_id).toBe("record-2"));
    act(() => result.current.setDraft({ TITLE: "B unsaved" }));
    const callsBeforeReadback = fetchMock.mock.calls.length;
    await act(async () => {
      resolveRefresh(Response.json(encodingRecord()));
      await refresh;
    });
    expect(result.current.recordId).toBe("record-2");
    expect(result.current.detail?.record_id).toBe("record-2");
    expect(result.current.items[0]?.record_id).toBe("record-2");
    expect(result.current.draft).toEqual({ TITLE: "B unsaved" });
    expect(fetchMock.mock.calls).toHaveLength(callsBeforeReadback);
  });

  it("refreshes detail and history without replacing an unsaved Final draft", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    let applied = false;
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input) === "/api/library/records/record-1") {
        return Response.json({
          ...encodingRecord(),
          events: applied ? [{ kind: "encoding" }] : [],
        });
      }
      return Response.json({ items: [] });
    });
    const { result } = renderHook(() => useAppController());
    await waitFor(() => expect(result.current.draft).toEqual({ TITLE: "Saved" }));
    act(() => result.current.setDraft({ TITLE: "Unsaved edit" }));
    applied = true;
    await act(async () => result.current.encodingApplied(false));
    expect(result.current.detail?.events).toEqual([{ kind: "encoding" }]);
    expect(result.current.items[0]).toEqual(result.current.detail);
    expect(result.current.draft).toEqual({ TITLE: "Unsaved edit" });
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

describe("useAppController MusicBrainz candidate lookup", () => {
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
      if (url.endsWith("/musicbrainz/release-candidates")) {
        expect(JSON.parse(String(init?.body))).toEqual({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
        });
        return Response.json({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          release_mbid: null,
          status: "review_required",
          candidate_count: 2,
        });
      }
      return Response.json({ items: [detail] });
    });

    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    render(<ControllerProbe />);
    await act(async () => {
      screen.getByRole("button", { name: "correct" }).click();
    });

    await waitFor(() =>
      expect(screen.getByTestId("notice").textContent).toContain("найдено релизов: 2"),
    );
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
  });
});

describe("useAppController catalog", () => {
  it.each([
    ["/library/albums", "/api/library/albums"],
    ["/library/tracks", "/api/library/tracks"],
  ])("omits an empty artist selector for the global route %s", async (route, expectedRequest) => {
    window.history.replaceState({}, "", route);
    const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url.startsWith("/api/library/artists"))
        return Response.json({ items: [], total_track_count: 0 });
      return Response.json({ items: [] });
    });

    renderHook(() => useAppController());

    await waitFor(() => expect(fetcher).toHaveBeenCalledWith(expectedRequest, expect.anything()));
    expect(fetcher.mock.calls.map(([input]) => String(input))).not.toContain(
      `${expectedRequest}?artist=`,
    );
  });

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
