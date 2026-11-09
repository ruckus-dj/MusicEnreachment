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

it("keeps the artist release filter but loads every position in an expanded release", async () => {
  window.location.hash = `#/library/artists/${artistId}?view=artists&availability=missing&publication=published`;
  server.use(
    http.get(`*/api/library/artists/${artistId}`, () =>
      HttpResponse.json({
        id: artistId,
        name: "Artist",
        disambiguation: null,
        genres: [],
        provider_links: [],
        release_count: 1,
        position_count: 1,
        available_position_count: 1,
        published_position_count: 1,
      }),
    ),
    http.get(`*/api/library/artists/${artistId}/metadata`, () =>
      HttpResponse.json({
        revision: 1,
        envelope: {
          version: 1,
          scope: "artist",
          desired: { sort_name: { state: "absent" } },
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
          changes: { sort_name: { state: string; value?: string } };
        };
        expect(body.expected_revision).toBe(1);
        expect(body.changes.sort_name).toEqual({
          state: "value",
          value: "Artist, Sort",
        });
        return HttpResponse.json({
          revision: 2,
          envelope: {
            version: 1,
            scope: "artist",
            desired: { sort_name: { state: "value", value: "Artist, Sort" } },
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
              position_count: 1,
              published_position_count: 1,
              available_position_count: 1,
              provider_position_count: 1,
            },
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary: {
          artist_count: 1,
          release_count: 1,
          position_count: 1,
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
            provider_duration_ms: null,
            publication: {},
            recording_id: null,
            release: { id: releaseId, title: "Filtered release", artists: [] },
            selected_media_variant_id: null,
            source_variant_count: 0,
            audio_available: true,
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary: {
          artist_count: 1,
          release_count: 1,
          position_count: 1,
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

  const sortNameEditor = await screen.findByRole("region", {
    name: "Artist sort name",
  });
  const editButton = await within(sortNameEditor).findByRole("button", {
    name: "Edit",
  });
  await waitFor(() => expect(editButton).toBeEnabled());
  fireEvent.click(editButton);
  const sortNameInput = await screen.findByRole("textbox", {
    name: "Artist sort name",
  });
  fireEvent.change(sortNameInput, { target: { value: "Artist, Sort" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("Artist, Sort")).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Artist", level: 1 }),
  ).toBeVisible();

  fireEvent.click(
    await screen.findByRole("button", { name: "Показать позиции" }),
  );
  expect(
    await screen.findByRole("link", { name: /Published track/ }),
  ).toBeVisible();
  expect(
    screen.getByText(/Аудио в наличии · 0 исходников · Опубликовано/),
  ).toBeVisible();
});
