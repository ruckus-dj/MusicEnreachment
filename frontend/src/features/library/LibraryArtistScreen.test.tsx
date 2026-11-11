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
  let detailsRevision = 1;
  let detailsName = "Artist";
  let detailsDisambiguation: string | null = "provider band";
  let sortNameSaveCount = 0;
  let failNextDetailsRead = false;
  server.use(
    http.get(`*/api/library/artists/${artistId}`, () =>
      HttpResponse.json({
        id: artistId,
        name: "Artist",
        disambiguation: "provider band",
        genres: [],
        provider_links: [],
        release_count: 1,
        position_count: 1,
        available_position_count: 1,
        published_position_count: 1,
      }),
    ),
    http.get(`*/api/library/artists/${artistId}/details`, () => {
      if (failNextDetailsRead) {
        failNextDetailsRead = false;
        return HttpResponse.json(
          { detail: "temporary read failure" },
          { status: 500 },
        );
      }
      return HttpResponse.json({
        id: artistId,
        name: detailsName,
        disambiguation: detailsDisambiguation,
        revision: detailsRevision,
        publication_applied: false,
      });
    }),
    http.get(`*/api/library/artists/${artistId}/metadata`, () =>
      HttpResponse.json({
        revision: detailsRevision,
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
        expect(body.expected_revision).toBe(detailsRevision);
        sortNameSaveCount += 1;
        expect(body.changes.sort_name).toEqual({
          state: "value",
          value: sortNameSaveCount === 1 ? "Artist, Sort" : "Artist, Sort 2",
        });
        detailsRevision += 1;
        if (sortNameSaveCount === 2) failNextDetailsRead = true;
        return HttpResponse.json({
          revision: detailsRevision,
          envelope: {
            version: 1,
            scope: "artist",
            desired: {
              sort_name: {
                state: "value",
                value:
                  sortNameSaveCount === 1 ? "Artist, Sort" : "Artist, Sort 2",
              },
            },
          },
          publication_applied: false,
        });
      },
    ),
    http.patch(
      `*/api/library/artists/${artistId}/details`,
      async ({ request }) => {
        const body = (await request.json()) as {
          expected_revision: number;
          name?: string;
          disambiguation?: string;
        };
        expect(body.expected_revision).toBe(detailsRevision);
        if (detailsRevision === 2) {
          expect(body).toEqual({
            expected_revision: 2,
            name: "  Local Artist  ",
          });
        } else {
          expect(body).toEqual({ expected_revision: 3, disambiguation: "" });
        }
        if (body.name !== undefined) detailsName = body.name;
        if (body.disambiguation !== undefined)
          detailsDisambiguation = body.disambiguation;
        detailsRevision += 1;
        return HttpResponse.json({
          id: artistId,
          name: detailsName,
          disambiguation: detailsDisambiguation,
          revision: detailsRevision,
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
                position_count: 1,
                published_position_count: 1,
                available_position_count: 1,
                provider_position_count: 1,
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

  const detailsEditor = await screen.findByRole("region", {
    name: "Artist name",
  });
  const detailsEdit = within(detailsEditor).getByRole("button", {
    name: "Edit",
  });
  await waitFor(() => expect(detailsEdit).toBeEnabled());
  fireEvent.click(detailsEdit);
  fireEvent.change(screen.getByRole("textbox", { name: "Artist name" }), {
    target: { value: "  Local Artist  " },
  });
  fireEvent.click(within(detailsEditor).getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("textbox", { name: "Artist name" }),
    ).not.toBeInTheDocument(),
  );
  const localArtistHeading = screen.getByRole("heading", {
    name: "Local Artist",
    level: 1,
  });
  expect(localArtistHeading).toBeVisible();
  expect(localArtistHeading.textContent).toBe("  Local Artist  ");
  expect(detailsRevision).toBe(3);

  fireEvent.click(within(detailsEditor).getByRole("button", { name: "Edit" }));
  expect(screen.getByRole("textbox", { name: "Disambiguation" })).toHaveValue(
    "provider band",
  );
  fireEvent.click(within(detailsEditor).getByRole("button", { name: "Clear" }));
  fireEvent.click(within(detailsEditor).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(detailsRevision).toBe(4));
  expect(detailsDisambiguation).toBe("");

  fireEvent.click(within(detailsEditor).getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Artist name" }), {
    target: { value: "Unrebased draft" },
  });
  fireEvent.click(within(sortNameEditor).getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Artist sort name" }), {
    target: { value: "Artist, Sort 2" },
  });
  fireEvent.click(within(sortNameEditor).getByRole("button", { name: "Save" }));
  const retryDetails = await within(detailsEditor).findByRole("button", {
    name: "Retry",
  });
  fireEvent.click(retryDetails);
  await waitFor(() =>
    expect(
      within(detailsEditor)
        .getAllByRole("alert")
        .some((alert) =>
          alert.textContent?.includes("changed while you were editing"),
        ),
    ).toBe(true),
  );
  expect(
    within(detailsEditor).queryByRole("button", { name: "Retry" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Artist name" })).toHaveValue(
    "Unrebased draft",
  );

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

it("preserves a conflicting artist-details draft and ignores a late save after artist navigation", async () => {
  const otherArtistId = "123e4567-e89b-42d3-a456-426614174001";
  let latestDetails = {
    id: artistId,
    name: "Artist",
    disambiguation: null as string | null,
    revision: 1,
    publication_applied: false,
  };
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
        name: params.id === artistId ? "Artist" : "Second Artist",
        disambiguation: null,
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
          ? latestDetails
          : {
              id: otherArtistId,
              name: "Second Artist",
              disambiguation: null,
              revision: 1,
              publication_applied: false,
            },
      ),
    ),
    http.patch("*/api/library/artists/:id/details", async () => {
      if (deferNextSave) {
        deferNextSave = false;
        signalLatePatchStarted();
        return new Promise<Response>((resolve) => {
          resolveLatePatch = resolve;
        });
      }
      latestDetails = {
        ...latestDetails,
        name: "Remote Artist",
        revision: 2,
      };
      return HttpResponse.json({ detail: "stale revision" }, { status: 409 });
    }),
    http.get("*/api/library/artists/:id/metadata", () =>
      HttpResponse.json({
        revision: latestDetails.revision,
        envelope: {
          version: 1,
          scope: "artist",
          desired: { sort_name: { state: "absent" } },
        },
        publication_applied: false,
        provider_genre_baseline: null,
      }),
    ),
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
  const editor = await screen.findByRole("region", { name: "Artist name" });
  const edit = within(editor).getByRole("button", { name: "Edit" });
  await waitFor(() => expect(edit).toBeEnabled());
  fireEvent.click(edit);
  fireEvent.change(screen.getByRole("textbox", { name: "Artist name" }), {
    target: { value: "My raw draft" },
  });
  fireEvent.click(within(editor).getByRole("button", { name: "Save" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Your draft is preserved",
  );
  expect(screen.getByRole("textbox", { name: "Artist name" })).toHaveValue(
    "My raw draft",
  );

  fireEvent.keyDown(screen.getByRole("textbox", { name: "Artist name" }), {
    key: "Escape",
  });
  expect(
    await screen.findByRole("heading", { name: "Remote Artist", level: 1 }),
  ).toBeVisible();

  deferNextSave = true;
  fireEvent.click(within(editor).getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Artist name" }), {
    target: { value: "Late stale save" },
  });
  fireEvent.click(within(editor).getByRole("button", { name: "Save" }));
  await latePatchStarted;

  window.location.hash = `#/library/artists/${otherArtistId}`;
  rendered.rerender(renderScreen(otherArtistId));
  expect(
    await screen.findByRole("heading", { name: "Second Artist", level: 1 }),
  ).toBeVisible();
  resolveLatePatch(
    HttpResponse.json({
      id: artistId,
      name: "Late stale save",
      disambiguation: null,
      revision: 3,
      publication_applied: false,
    }),
  );
  expect(
    screen.getByRole("heading", { name: "Second Artist", level: 1 }),
  ).toBeVisible();
  expect(screen.queryByText("Late stale save")).not.toBeInTheDocument();
});
