import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type {
  LibraryMetadataResponse,
  LibraryPublicationResponse,
  LibraryReleaseDetailResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { LibraryReleaseScreen } from "./LibraryReleaseScreen";

vi.mock("./FirstPublicationPreview", () => ({
  FirstPublicationPreview: ({
    refreshToken,
  }: {
    refreshToken?: string | number;
  }) => (
    <div data-testid="publication-preview" data-refresh-token={refreshToken} />
  ),
}));
vi.mock("./newReleaseCoverEditor", () => ({
  ReleaseCoverEditor: () => null,
}));

const releaseID = "release-1";
const positionID = "position-1";
const validatedAt = "2026-10-20T12:34:56Z";

function release(published: boolean): LibraryReleaseDetailResponse {
  return {
    id: releaseID,
    title: "Contextual Release",
    artists: null,
    can_refresh_musicbrainz: false,
    metadata: {},
    metadata_revision: 1,
    metadata_version: 1,
    primary_artist: {
      candidate_artist_ids: null,
      effective_artist_id: "",
      effective_artist_mbid: "",
      selected_artist_id: "",
      status: "unknown",
    },
    media: null,
    tracks: [
      {
        id: positionID,
        title: "Position One",
        artists: [],
        displayed_number: "1",
        position: 1,
        medium: 1,
        local_track_id: "local-track-1",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        track_metadata_revision: 1,
        primary_artist: {
          candidate_artist_ids: null,
          effective_artist_id: "",
          effective_artist_mbid: "",
          selected_artist_id: "",
          status: "unknown",
        },
        duration_ms: null,
        recording_id: null,
        sources: [],
        publication: published
          ? {
              container: "flac",
              codec: "FLAC",
              generation: "generation-1",
              relative_path: "Artist/Album/01 - Position One.flac",
              validated_at: validatedAt,
            }
          : undefined,
      },
    ],
  };
}

const metadata: LibraryMetadataResponse = {
  envelope: { desired: {}, scope: "position", version: 1 },
  publication_applied: false,
  revision: 1,
};

function publication(published: boolean): LibraryPublicationResponse {
  return {
    active_epoch: "epoch-1",
    can_apply_manual_lyrics: false,
    metadata_revision: 1,
    position_id: positionID,
    release_id: releaseID,
    ...(published
      ? {
          current_generation: "generation-1",
          current_path: "Artist/Album/01 - Position One.flac",
          current_validated_at: validatedAt,
          current_audio: {
            alac_capabilities_verified: false,
            codec: "FLAC",
            container: "flac",
            lossless: true,
            stream_index: 0,
            supported_source_container: true,
          },
        }
      : {}),
  };
}

function installHandlers(published: boolean) {
  server.use(
    http.get("*/api/library/releases/:releaseId", () =>
      HttpResponse.json(release(published)),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/metadata",
      () => HttpResponse.json(metadata),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/publication",
      () => HttpResponse.json(publication(published)),
    ),
  );
}

function renderScreen(releaseId: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <LibraryReleaseScreen releaseId={releaseId} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
});

beforeEach(() => {
  server.use(
    http.get("*/api/library/releases/:releaseId/metadata", () =>
      HttpResponse.json({
        envelope: { desired: {}, scope: "release", version: 1 },
        publication_applied: false,
        revision: 1,
      }),
    ),
  );
});

it("shows the current publication summary from release detail without source inventory", async () => {
  installHandlers(true);

  renderScreen(releaseID);

  const summary = await screen.findByRole("region", {
    name: "Текущая публикация",
  });
  expect(summary).toHaveTextContent("Опубликовано");
  expect(summary).toHaveTextContent("flac · FLAC");
  expect(summary).toHaveTextContent("Artist/Album/01 - Position One.flac");
  expect(summary.querySelector("time")).toHaveAttribute(
    "dateTime",
    validatedAt,
  );
  expect(
    new Intl.DateTimeFormat("ru", {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(validatedAt)),
  ).toBe(summary.querySelector("time")?.textContent);
  expect(summary).not.toHaveTextContent("Источники");
});

it("shows an explicit unpublished state when release detail has no publication summary", async () => {
  installHandlers(false);

  renderScreen(releaseID);

  const summary = await screen.findByRole("region", {
    name: "Текущая публикация",
  });
  expect(summary).toHaveTextContent("Не опубликовано");
  expect(summary).not.toHaveTextContent("Artist/Album");
});

it("invalidates preview context immediately even if release detail refresh fails", async () => {
  let releaseReads = 0;
  let resolveReleaseRefresh!: (response: Response) => void;
  const releaseGenreMetadata: LibraryMetadataResponse = {
    envelope: { desired: {}, scope: "release", version: 1 },
    publication_applied: false,
    revision: 1,
  };
  server.use(
    http.get("*/api/library/releases/:releaseId", () => {
      releaseReads += 1;
      if (releaseReads > 1)
        return new Promise<Response>((resolve) => {
          resolveReleaseRefresh = resolve;
        });
      return HttpResponse.json(release(false));
    }),
    http.get("*/api/library/releases/:releaseId/metadata", () =>
      HttpResponse.json(releaseGenreMetadata),
    ),
    http.patch("*/api/library/releases/:releaseId/metadata", () =>
      HttpResponse.json({
        ...releaseGenreMetadata,
        revision: 2,
        envelope: {
          ...releaseGenreMetadata.envelope,
          desired: { genre: { state: "explicit_empty" } },
        },
      }),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/metadata",
      () => HttpResponse.json(metadata),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/publication",
      () => HttpResponse.json(publication(false)),
    ),
  );

  renderScreen(releaseID);
  await screen.findByRole("region", { name: "Текущая публикация" });
  const preview = screen.getByTestId("publication-preview");
  const initialToken = preview.getAttribute("data-refresh-token");
  const genres = await screen.findByRole("region", {
    name: "Release genres",
  });
  await waitFor(() =>
    expect(within(genres).getByRole("button", { name: "Edit" })).toBeEnabled(),
  );
  fireEvent.click(within(genres).getByRole("button", { name: "Edit" }));
  fireEvent.click(within(genres).getByRole("button", { name: "Clear genres" }));
  fireEvent.click(within(genres).getByRole("button", { name: "Save" }));

  await waitFor(() => expect(releaseReads).toBeGreaterThan(1));
  await waitFor(() =>
    expect(preview).not.toHaveAttribute("data-refresh-token", initialToken),
  );
  resolveReleaseRefresh(
    HttpResponse.json({ detail: "detail refresh failed" }, { status: 500 }),
  );
  expect(screen.getByTestId("publication-preview")).not.toHaveAttribute(
    "data-refresh-token",
    initialToken,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "detail refresh failed",
  );
});

it("replaces the release-detail publication summary with refreshed position state", async () => {
  let currentPublication = publication(true);
  currentPublication = {
    ...currentPublication,
    current_generation: "generation-2",
    current_path: "Artist/Album/rewritten.flac",
    current_validated_at: "2026-10-21T12:34:56Z",
  };
  server.use(
    http.get("*/api/library/releases/:releaseId", () =>
      HttpResponse.json(release(false)),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/metadata",
      () => HttpResponse.json(metadata),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/publication",
      () => HttpResponse.json(currentPublication),
    ),
  );

  renderScreen(releaseID);

  const summary = await screen.findByRole("region", {
    name: "Текущая публикация",
  });
  await waitFor(() => expect(summary).toHaveTextContent("Опубликовано"));
  expect(summary).toHaveTextContent("Artist/Album/rewritten.flac");
  expect(summary).not.toHaveTextContent("Не опубликовано");
});

it("updates generation and path after a manual position refresh", async () => {
  let publicationRead = 0;
  server.use(
    http.get("*/api/library/releases/:releaseId", () =>
      HttpResponse.json(release(true)),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/metadata",
      () => HttpResponse.json(metadata),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/publication",
      () => {
        publicationRead += 1;
        if (publicationRead === 1) return HttpResponse.error();
        return HttpResponse.json({
          ...publication(true),
          current_generation: "generation-2",
          current_path: "Artist/Album/rewritten.flac",
          current_validated_at: "2026-10-21T12:34:56Z",
        });
      },
    ),
  );

  renderScreen(releaseID);

  const summary = await screen.findByRole("region", {
    name: "Текущая публикация",
  });
  expect(summary).toHaveTextContent("Artist/Album/01 - Position One.flac");
  expect(
    screen
      .getByTestId("publication-preview")
      .getAttribute("data-refresh-token"),
  ).not.toContain("generation-2");
  fireEvent.click(
    await screen.findByRole("button", { name: "Обновить данные" }),
  );
  await waitFor(() =>
    expect(summary).toHaveTextContent("Artist/Album/rewritten.flac"),
  );
  expect(summary).not.toHaveTextContent("Artist/Album/01 - Position One.flac");
  await waitFor(() =>
    expect(
      screen
        .getByTestId("publication-preview")
        .getAttribute("data-refresh-token"),
    ).toContain("generation-2"),
  );
});
