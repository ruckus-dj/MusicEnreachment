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
import { afterEach, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { LibraryArtistScreen } from "./LibraryArtistScreen";

const artistId = "123e4567-e89b-42d3-a456-426614174000";
const releaseId = "3f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.location.hash = "";
});

it("saves combined artist metadata in one patch while keeping the artist release filter", async () => {
  window.location.hash = `#/library/artists/${artistId}?view=artists&availability=missing&publication=published`;
  let detailsRevision = 1;
  let artistName = "Artist";
  let metadataPatchCount = 0;
  server.use(
    http.get(`*/api/library/artists/${artistId}`, () =>
      HttpResponse.json({
        id: artistId,
        name: artistName,
        disambiguation: "provider band",
        genres: [],
        provider_links: [],
        release_count: 1,
        position_count: 2,
        available_position_count: 1,
        published_position_count: 1,
      }),
    ),
    http.get(`*/api/library/artists/${artistId}/details`, () =>
      HttpResponse.json({
        id: artistId,
        name: artistName,
        disambiguation: "provider band",
        revision: detailsRevision,
        publication_applied: false,
      }),
    ),
    http.get(`*/api/library/artists/${artistId}/metadata`, () =>
      HttpResponse.json({
        revision: detailsRevision,
        envelope: {
          version: 1,
          scope: "artist",
          desired: {
            sort_name: { state: "absent" },
            genre: { state: "absent" },
          },
        },
        publication_applied: false,
        provider_genre_baseline: null,
      }),
    ),
    http.patch(
      `*/api/library/artists/${artistId}/metadata`,
      async ({ request }) => {
        const body = (await request.json()) as {
          expected_revision: number;
          name?: string;
          disambiguation?: string;
          changes: {
            sort_name: { state: string; value?: string };
            genre: { state: string };
          };
        };
        expect(body).toEqual({
          expected_revision: 1,
          name: "  Local Artist  ",
          disambiguation: "",
          changes: {
            sort_name: { state: "value", value: "Artist, Sort" },
            genre: { state: "explicit_empty" },
          },
        });
        metadataPatchCount += 1;
        detailsRevision += 1;
        artistName = body.name ?? artistName;
        return HttpResponse.json({
          revision: detailsRevision,
          envelope: {
            version: 1,
            scope: "artist",
            desired: {
              sort_name: { state: "value", value: "Artist, Sort" },
              genre: { state: "explicit_empty" },
            },
          },
          publication_applied: false,
        });
      },
    ),
    http.get("*/api/library/releases", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("artist_id")).toBe(artistId);
      expect(url.searchParams.get("availability")).toBe("missing");
      expect(url.searchParams.get("publication")).toBe("published");
      return HttpResponse.json({
        items: [
          {
            kind: "library_release",
            missing_release_group: null,
            library_release: {
              id: releaseId,
              title: "Filtered release",
              artists: [],
              edition: {
                release_date: null,
                original_release_date: null,
                country: null,
                labels: [],
                catalog_numbers: [],
              },
              provider_links: [],
              cover: null,
              counts: {
                position_count: 2,
                published_position_count: 1,
                available_position_count: 1,
                provider_position_count: 2,
              },
            },
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary: {
          artist_count: 1,
          release_count: 1,
          position_count: 2,
          published_position_count: 1,
        },
      });
    }),
    http.get("*/api/library/positions", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("release_id")).toBe(releaseId);
      expect(url.searchParams.get("availability")).toBe("all");
      expect(url.searchParams.get("publication")).toBe("all");
      return HttpResponse.json({
        items: [
          {
            id: "position-id",
            title: "Published track",
            artists: [],
            displayed_number: "1",
            local_track_id: "track-id",
            medium: 1,
            position: 1,
            provider_duration_ms: 183000,
            publication: {
              audio: {
                alac_capabilities_verified: false,
                codec: "FLAC",
                container: "flac",
                lossless: true,
                stream_index: 0,
                supported_source_container: true,
              },
              epoch: "test-epoch",
              generation: "test-generation",
              matching_confidence: null,
              relative_path: "artist/release/1.flac",
              validated_at: "2026-11-22T12:00:00Z",
            },
            recording_id: null,
            release: { id: releaseId, title: "Filtered release", artists: [] },
            selected_media_variant_id: null,
            source_variant_count: 0,
            audio_available: true,
          },
          {
            id: "second-position-id",
            title: "Other medium",
            artists: [],
            displayed_number: "1",
            local_track_id: "second-track-id",
            medium: 2,
            position: 1,
            provider_duration_ms: null,
            publication: null,
            recording_id: null,
            release: { id: releaseId, title: "Filtered release", artists: [] },
            selected_media_variant_id: null,
            source_variant_count: 0,
            audio_available: false,
          },
        ],
        total: 2,
        limit: 50,
        offset: 0,
        summary: {
          artist_count: 1,
          release_count: 1,
          position_count: 2,
          published_position_count: 1,
        },
      });
    }),
  );

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <LibraryArtistScreen artistId={artistId} />
    </QueryClientProvider>,
  );

  const metadataCard = await screen.findByRole("region", {
    name: "Artist metadata",
  });
  const editButton = within(metadataCard).getByRole("button", {
    name: "Edit",
  });
  await waitFor(() => expect(editButton).toBeEnabled());
  fireEvent.click(editButton);
  fireEvent.change(
    within(metadataCard).getByRole("textbox", { name: "Name" }),
    { target: { value: "  Local Artist  " } },
  );
  fireEvent.change(
    within(metadataCard).getByRole("textbox", { name: "Sort name" }),
    { target: { value: "Artist, Sort" } },
  );
  fireEvent.change(
    within(metadataCard).getByRole("textbox", { name: "Disambiguation" }),
    { target: { value: "" } },
  );
  fireEvent.click(
    within(metadataCard).getByRole("button", { name: "Clear genres" }),
  );
  fireEvent.click(within(metadataCard).getByRole("button", { name: "Save" }));

  const heading = await screen.findByRole("heading", {
    name: "Local Artist",
    level: 1,
  });
  expect(heading).toBeVisible();
  expect(heading.textContent).toBe("  Local Artist  ");
  expect(metadataPatchCount).toBe(1);
  await waitFor(() =>
    expect(
      within(metadataCard).getByRole("button", { name: "Edit" }),
    ).toBeEnabled(),
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Show positions" }),
  );
  const tracks = await screen.findByRole("table");
  const row = within(tracks).getByRole("row", { name: /Published track/ });
  expect(
    within(row).getByRole("link", { name: "Published track" }),
  ).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}/positions/position-id`,
  );
  expect(within(row).getByText("3:03", { exact: true })).toBeVisible();
  const cells = within(row).getAllByRole("cell");
  expect(cells[0]).toHaveTextContent(/^1\.1$/);
  expect(cells[2]).toHaveTextContent(/^—$/);
  expect(cells[3]).toHaveTextContent(/^Yes$/);
  expect(cells[4]).toHaveTextContent(/^0$/);
  expect(cells[5]).toHaveTextContent(/^flac · FLAC$/);
  expect(within(row).queryByRole("checkbox")).not.toBeInTheDocument();
  expect(
    within(tracks).getByRole("row", { name: /2\.1 Other medium/ }),
  ).toHaveTextContent("Not published");
});

it("preserves a conflicting draft and refreshes a reopened artist after a late save", async () => {
  const otherArtistId = "123e4567-e89b-42d3-a456-426614174001";
  let detailsRevision = 1;
  let detailsName = "Artist";
  const detailsDisambiguation: string | null = null;
  let deferNextSave = false;
  let resolveLatePatch!: (response: Response) => void;
  let signalLatePatchStarted!: () => void;
  const latePatchStarted = new Promise<void>((resolve) => {
    signalLatePatchStarted = resolve;
  });
  server.use(
    http.get("*/api/library/artists/:id", ({ params }) =>
      HttpResponse.json({
        id: params.id,
        name: params.id === artistId ? detailsName : "Second Artist",
        disambiguation: params.id === artistId ? detailsDisambiguation : null,
        genres: [],
        provider_links: [],
        release_count: 0,
        position_count: 0,
        available_position_count: 0,
        published_position_count: 0,
      }),
    ),
    http.get("*/api/library/artists/:id/details", ({ params }) =>
      HttpResponse.json(
        params.id === artistId
          ? {
              id: artistId,
              name: detailsName,
              disambiguation: detailsDisambiguation,
              revision: detailsRevision,
              publication_applied: false,
            }
          : {
              id: otherArtistId,
              name: "Second Artist",
              disambiguation: null,
              revision: 1,
              publication_applied: false,
            },
      ),
    ),
    http.get("*/api/library/artists/:id/metadata", ({ params }) =>
      HttpResponse.json({
        revision: params.id === artistId ? detailsRevision : 1,
        envelope: {
          version: 1,
          scope: "artist",
          desired: {
            sort_name: { state: "absent" },
            genre: { state: "absent" },
          },
        },
        publication_applied: false,
        provider_genre_baseline: null,
      }),
    ),
    http.patch("*/api/library/artists/:id/metadata", async ({ request }) => {
      const body = (await request.json()) as {
        expected_revision: number;
        name?: string;
        disambiguation?: string;
      };
      if (deferNextSave) {
        expect(body.expected_revision).toBe(2);
        deferNextSave = false;
        signalLatePatchStarted();
        return new Promise<Response>((resolve) => {
          resolveLatePatch = resolve;
        });
      }
      expect(body).toEqual({
        expected_revision: 1,
        name: "My raw draft",
        disambiguation: "My raw disambiguation",
      });
      detailsRevision = 2;
      detailsName = "Remote Artist";
      return HttpResponse.json({ detail: "stale revision" }, { status: 409 });
    }),
    http.get("*/api/library/releases", () =>
      HttpResponse.json({
        items: [],
        total: 0,
        limit: 50,
        offset: 0,
        summary: {
          artist_count: 0,
          release_count: 0,
          position_count: 0,
          published_position_count: 0,
        },
      }),
    ),
  );

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const renderScreen = (id: string) => (
    <QueryClientProvider client={client}>
      <LibraryArtistScreen artistId={id} />
    </QueryClientProvider>
  );
  const rendered = render(renderScreen(artistId));
  const card = await screen.findByRole("region", { name: "Artist metadata" });
  const edit = within(card).getByRole("button", { name: "Edit" });
  await waitFor(() => expect(edit).toBeEnabled());
  fireEvent.click(edit);
  fireEvent.change(within(card).getByRole("textbox", { name: "Name" }), {
    target: { value: "My raw draft" },
  });
  fireEvent.change(
    within(card).getByRole("textbox", { name: "Disambiguation" }),
    { target: { value: "My raw disambiguation" } },
  );
  fireEvent.click(within(card).getByRole("button", { name: "Save" }));
  expect(await within(card).findByRole("alert")).toHaveTextContent(
    "Your draft is preserved",
  );
  await waitFor(() =>
    expect(within(card).getByRole("button", { name: "Save" })).toBeEnabled(),
  );
  expect(within(card).getByRole("textbox", { name: "Name" })).toHaveValue(
    "My raw draft",
  );
  expect(
    within(card).getByRole("textbox", { name: "Disambiguation" }),
  ).toHaveValue("My raw disambiguation");

  fireEvent.keyDown(within(card).getByRole("textbox", { name: "Name" }), {
    key: "Escape",
  });
  expect(
    screen.queryByRole("textbox", { name: "Name" }),
  ).not.toBeInTheDocument();
  expect(within(card).getByText("Remote Artist")).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Artist", level: 1 }),
  ).toBeVisible();

  deferNextSave = true;
  fireEvent.click(within(card).getByRole("button", { name: "Edit" }));
  fireEvent.change(within(card).getByRole("textbox", { name: "Name" }), {
    target: { value: "Late stale save" },
  });
  fireEvent.click(within(card).getByRole("button", { name: "Save" }));
  await latePatchStarted;

  window.location.hash = `#/library/artists/${otherArtistId}`;
  rendered.rerender(renderScreen(otherArtistId));
  expect(
    await screen.findByRole("heading", { name: "Second Artist", level: 1 }),
  ).toBeVisible();

  window.location.hash = `#/library/artists/${artistId}`;
  rendered.rerender(renderScreen(artistId));
  const reopened = within(
    await screen.findByRole("region", { name: "Artist metadata" }),
  );
  expect(reopened.getByText("Remote Artist")).toBeVisible();
  detailsRevision = 3;
  detailsName = "Late stale save";
  resolveLatePatch(
    HttpResponse.json({
      revision: 3,
      envelope: {
        version: 1,
        scope: "artist",
        desired: {
          sort_name: { state: "absent" },
          genre: { state: "absent" },
        },
      },
      publication_applied: false,
    }),
  );
  expect(await reopened.findByText("Late stale save")).toBeVisible();
  expect(
    await screen.findByRole("heading", { name: "Late stale save", level: 1 }),
  ).toBeVisible();
});
