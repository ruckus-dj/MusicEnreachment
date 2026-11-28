import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import { useIncomingPublicationQueue } from "./publicationQueue";

const credit = {
  artist_id: "artist-1",
  artist_name: "Arden Trio",
  credit_name: "Arden Trio",
  join_phrase: "",
  role: "",
};

function position(id: string, number: string, title: string) {
  return {
    artists: [credit],
    audio_available: true,
    displayed_number: number,
    id,
    local_track_id: `track-${id}`,
    medium: 1,
    position: Number(number),
    provider_duration_ms: null,
    publication: null,
    recording_id: null,
    release: { artists: [credit], id: "release-1", title: "Northern Lines" },
    selected_media_variant_id: `variant-${id}`,
    source_variant_count: 1,
    title,
  };
}

function page(
  items: ReturnType<typeof position>[],
  total: number,
  offset: number,
) {
  return {
    items,
    limit: 100,
    offset,
    summary: {
      artist_count: 1,
      release_count: 1,
      position_count: total,
      published_position_count: 0,
    },
    total,
  };
}

function release() {
  const detail = (id: string, number: string, title: string) => ({
    id,
    local_track_id: `track-${id}`,
    recording_id: null,
    medium: 1,
    position: Number(number),
    duration_ms: null,
    displayed_number: number,
    title,
    metadata: {},
    metadata_version: 1,
    metadata_revision: 1,
    track_metadata_revision: 1,
    primary_artist: {
      status: "automatic",
      selected_artist_id: null,
      effective_artist_id: "artist-1",
      effective_artist_mbid: null,
      candidate_artist_ids: ["artist-1"],
    },
    artists: [credit],
    sources: [
      {
        media_variant_id: `variant-${id}`,
        selected: true,
        compatible: true,
        decision_method: "manual",
        policy_version: null,
        metadata: {},
        locations: [
          {
            root_id: "root-1",
            location_id: `location-${id}`,
            relative_path: `inbox/${number}-${id}.flac`,
            size_bytes: 1,
            probe_status: "audio",
            root_status: "available",
          },
        ],
      },
    ],
    publication: null,
  });
  return {
    id: "release-1",
    title: "Northern Lines",
    metadata: {
      local_metadata: {
        desired: { release_date: { state: "value", value: "2024" } },
      },
    },
    metadata_version: 1,
    metadata_revision: 1,
    can_refresh_musicbrainz: false,
    primary_artist: {
      status: "automatic",
      selected_artist_id: null,
      effective_artist_id: "artist-1",
      effective_artist_mbid: null,
      candidate_artist_ids: ["artist-1"],
    },
    artists: [credit],
    media: [],
    tracks: [
      detail("position-1", "1", "Still Water"),
      detail("position-2", "2", "Second"),
    ],
  };
}

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useIncomingPublicationQueue", () => {
  it("does not request the catalogue or claim an empty queue while disabled", () => {
    let requests = 0;
    server.use(
      http.get("/api/library/positions", () => {
        requests += 1;
        return HttpResponse.json(page([], 0, 0));
      }),
    );
    const { result } = renderHook(() => useIncomingPublicationQueue(false), {
      wrapper,
    });
    expect(requests).toBe(0);
    expect(result.current.editions).toEqual([]);
    expect(result.current.status).toEqual({ kind: "loading" });
  });

  it("pages all positions and groups planner-confirmed editions", async () => {
    let positionRequests = 0;
    let previewRequests = 0;
    server.use(
      http.get("/api/library/positions", ({ request }) => {
        positionRequests += 1;
        const offset = Number(new URL(request.url).searchParams.get("offset"));
        return HttpResponse.json(
          offset === 0
            ? page([position("position-1", "1", "Still Water")], 2, 0)
            : page([position("position-2", "2", "Second")], 2, 1),
        );
      }),
      http.post("/api/library/publication/preview", async ({ request }) => {
        previewRequests += 1;
        const body = (await request.json()) as {
          positions: { release_id: string; position_id: string }[];
        };
        return HttpResponse.json({
          mode: "ordinary",
          items: body.positions.map((item) => ({
            release_id: item.release_id,
            position_id: item.position_id,
            disposition: "create",
            path_clash: false,
            resolved_path: `Arden Trio/Northern Lines/${item.position_id}.flac`,
          })),
        });
      }),
      http.get("/api/library/releases/:releaseId", () =>
        HttpResponse.json(release()),
      ),
    );
    const { result } = renderHook(() => useIncomingPublicationQueue(true), {
      wrapper,
    });
    expect(result.current.status).toEqual({ kind: "loading" });
    await waitFor(() => expect(result.current.status).toBeUndefined(), {
      timeout: 5000,
    });
    expect(positionRequests).toBe(2);
    expect(previewRequests).toBe(1);
    expect(result.current.editions).toHaveLength(1);
    expect(
      result.current.editions[0].tracks.map((track) => track.number),
    ).toEqual(["1", "2"]);
    expect(result.current.editions[0].reason).toEqual({ code: "manualReady" });
  });

  it("surfaces planner failure and retries through the same query", async () => {
    server.use(
      http.get("/api/library/positions", () =>
        HttpResponse.json(
          page([position("position-1", "1", "Still Water")], 1, 0),
        ),
      ),
      http.post(
        "/api/library/publication/preview",
        () =>
          new HttpResponse(JSON.stringify({ detail: "planner unavailable" }), {
            status: 503,
            headers: { "Content-Type": "application/json" },
          }),
      ),
      http.get("/api/library/releases/:releaseId", () =>
        HttpResponse.json(release()),
      ),
    );
    const { result } = renderHook(() => useIncomingPublicationQueue(true), {
      wrapper,
    });
    await waitFor(() => expect(result.current.status?.kind).toBe("error"));
    expect(result.current.editions).toEqual([]);

    server.use(
      http.post("/api/library/publication/preview", () =>
        HttpResponse.json({
          mode: "ordinary",
          items: [
            {
              release_id: "release-1",
              position_id: "position-1",
              disposition: "create",
              path_clash: false,
              resolved_path: "Arden Trio/Northern Lines/file.flac",
            },
          ],
        }),
      ),
    );
    if (result.current.status?.kind === "error") {
      result.current.status.retry();
    }
    await waitFor(() => expect(result.current.status).toBeUndefined(), {
      timeout: 5000,
    });
    expect(result.current.editions).toHaveLength(1);
  });
});
