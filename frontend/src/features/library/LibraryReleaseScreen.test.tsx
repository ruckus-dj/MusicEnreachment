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
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
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
const mediaVariantID = "123e4567-e89b-42d3-a456-426614174001";
const validatedAt = "2026-10-20T12:34:56Z";

function release(
  published: boolean,
  generation = "generation-1",
  canRefreshMusicBrainz = false,
): LibraryReleaseDetailResponse {
  return {
    id: releaseID,
    title: "Contextual Release",
    artists: null,
    can_refresh_musicbrainz: canRefreshMusicBrainz,
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
        sources: [
          {
            compatible: true,
            decision_method: "manual",
            locations: [],
            media_variant_id: mediaVariantID,
            metadata: {},
            policy_version: "test-policy-v1",
            selected: true,
          },
        ],
        publication: published
          ? {
              container: "flac",
              codec: "FLAC",
              generation,
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

function publication(
  published: boolean,
  generation = "generation-1",
): LibraryPublicationResponse {
  return {
    active_epoch: "epoch-1",
    can_apply_manual_lyrics: false,
    metadata_revision: 1,
    position_id: positionID,
    release_id: releaseID,
    ...(published
      ? {
          current_generation: generation,
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
  vi.unstubAllGlobals();
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

it("shows publication and source details from release detail without source inventory", async () => {
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
  const sources = screen.getByRole("region", {
    name: "Audio sources for position 1",
  });
  expect(
    within(sources).getByRole("columnheader", { name: "Variant" }),
  ).toBeInTheDocument();
  const sourceRow = within(sources).getByRole("row", {
    name: /1 Selected · manual/,
  });
  expect(sourceRow).toHaveTextContent("test-policy-v1");
  expect(sourceRow).not.toHaveTextContent(mediaVariantID);
  expect(screen.queryByText(mediaVariantID)).not.toBeInTheDocument();
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

it("refreshes deletion from authoritative position state without remounting publication preview", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  let deleted = false;
  let generation = "generation-1";
  let previewReads = 0;
  let deletionCalls = 0;
  server.use(
    http.get("*/api/library/releases/:releaseId", () =>
      HttpResponse.json(release(!deleted, generation, true)),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/metadata",
      () => HttpResponse.json(metadata),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/publication",
      () => HttpResponse.json(publication(!deleted, generation)),
    ),
    http.get("*/api/library/releases/:releaseId/deletion/preview", () =>
      HttpResponse.json({
        blockers: [],
        confirmation_digest: "b".repeat(64),
        kind: "release",
        outputs: [],
        position_ids: [positionID],
        release_id: releaseID,
        title: "Contextual Release",
      }),
    ),
    http.get(
      "*/api/library/releases/:releaseId/positions/:positionId/deletion/preview",
      () => {
        previewReads += 1;
        return HttpResponse.json({
          blockers: [],
          confirmation_digest: "b".repeat(64),
          kind: "publication",
          outputs: [
            {
              capturable: true,
              position_id: positionID,
              relative_path: `Artist/Album/${generation}.flac`,
            },
          ],
          position_ids: [positionID],
          release_id: releaseID,
          title: "Contextual Release",
        });
      },
    ),
    http.post("*/api/library/deletion/confirm", () => {
      deletionCalls += 1;
      deleted = true;
      return HttpResponse.json(
        {
          id: "76092c63-0e0f-4dc1-af39-674dc1ce037c",
          kind: "delete_library_v1",
          state: "succeeded",
          stage: "succeeded",
          bytes_completed: 100,
          created_at: "2026-11-11T10:00:00Z",
          updated_at: "2026-11-11T10:00:01Z",
        },
        { status: 202 },
      );
    }),
    http.post("*/api/library/releases/:releaseId/musicbrainz-refresh", () => {
      deleted = false;
      generation = "generation-2";
      return HttpResponse.json(
        {
          id: "43119aa6-c611-4b4e-afbe-421d3e3d2809",
          kind: "refresh_library_release_musicbrainz",
          state: "succeeded",
          stage: "succeeded",
          bytes_completed: 0,
          created_at: "2026-11-11T10:00:00Z",
          updated_at: "2026-11-11T10:00:01Z",
        },
        { status: 202 },
      );
    }),
  );

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider>
      <QueryClientProvider client={client}>
        <LibraryReleaseScreen releaseId={releaseID} positionId={positionID} />
      </QueryClientProvider>
    </I18nProvider>,
  );
  const publicationPreview = await screen.findByTestId("publication-preview");
  const summary = await screen.findByRole("region", {
    name: "Текущая публикация",
  });
  const positionArticle = await screen.findByRole("article");
  expect(within(positionArticle).getByText("Композиция")).toBeVisible();
  expect(within(positionArticle).queryByText("Recording ID")).toBeNull();
  fireEvent.click(
    await within(positionArticle).findByRole("button", {
      name: "Удалить текущую публикацию",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );
  expect(
    await screen.findByText("Artist/Album/generation-1.flac"),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));

  await waitFor(() => expect(summary).toHaveTextContent("Не опубликовано"));
  expect(
    within(positionArticle).queryByRole("button", {
      name: "Удалить текущую публикацию",
    }),
  ).not.toBeInTheDocument();
  expect(screen.getByTestId("publication-preview")).toBe(publicationPreview);

  fireEvent.click(
    screen.getByRole("button", { name: "Обновить данные MusicBrainz" }),
  );
  await waitFor(() => expect(summary).toHaveTextContent("Опубликовано"));
  await waitFor(() =>
    expect(
      within(positionArticle).getByRole("button", {
        name: "Удалить текущую публикацию",
      }),
    ).toBeInTheDocument(),
  );
  fireEvent.click(
    within(positionArticle).getByRole("button", {
      name: "Удалить текущую публикацию",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );
  expect(
    await screen.findByText("Artist/Album/generation-2.flac"),
  ).toBeInTheDocument();
  expect(previewReads).toBe(2);
  expect(deletionCalls).toBe(1);
  expect(screen.getByTestId("publication-preview")).toBe(publicationPreview);
});
