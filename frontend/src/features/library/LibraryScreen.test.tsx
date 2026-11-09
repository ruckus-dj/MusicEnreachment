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
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { LibraryScreen } from "./LibraryScreen";

const artistId = "123e4567-e89b-42d3-a456-426614174000";
const summary = {
  artist_count: 1,
  release_count: 1,
  position_count: 2,
  published_position_count: 1,
};

function renderScreen(screen: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>{screen}</QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.location.hash = "";
});

it("renders server summary, artist cards and preserves search and pagination in the URL", async () => {
  window.location.hash = "#/library?view=artists";
  let artistRequests = 0;
  let expectedAvailability = "all";
  server.use(
    http.get("*/api/library/artists", ({ request }) => {
      const url = new URL(request.url);
      artistRequests += 1;
      expect(url.searchParams.get("limit")).toBe("50");
      expect(url.searchParams.get("publication")).toBe("all");
      expect(url.searchParams.get("availability")).toBe(expectedAvailability);
      return HttpResponse.json({
        items: [
          {
            id: artistId,
            name: "A Artist",
            disambiguation: null,
            genres: ["Jazz"],
            provider_links: [],
            release_count: 1,
            position_count: 2,
            available_position_count: 1,
            published_position_count: 1,
          },
        ],
        total: 51,
        limit: 50,
        offset: Number(url.searchParams.get("offset")),
        summary,
      });
    }),
  );
  renderScreen(<LibraryScreen />);
  expect(
    screen.getByRole("radiogroup", { name: "Наличие аудио" }),
  ).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: /A Artist/ })).toHaveAttribute(
    "href",
    `#/library/artists/${artistId}?view=artists&publication=all&availability=all`,
  );
  expect(
    screen.getByText("1 артистов · 1 релизов · 2 позиций · 1 опубликовано"),
  ).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /A Artist/ })).toHaveTextContent(
    "С аудио: 1 · 1 опубликовано",
  );
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "jazz" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Найти" }));
  await waitFor(() => expect(window.location.hash).toContain("q=jazz"));
  fireEvent.click(await screen.findByRole("button", { name: "Далее" }));
  await waitFor(() => expect(window.location.hash).toContain("offset=50"));
  await waitFor(() => expect(artistRequests).toBe(3));
  expectedAvailability = "missing";
  fireEvent.click(screen.getByRole("radio", { name: "Не хватает" }));
  await waitFor(() => {
    expect(window.location.hash).toContain("availability=missing");
    expect(window.location.hash).not.toContain("offset=");
  });
  await waitFor(() => expect(artistRequests).toBe(4));
});

it("shows audio availability separately from sources and publication", async () => {
  window.location.hash = "#/library?view=tracks";
  server.use(
    http.get("*/api/library/positions", () =>
      HttpResponse.json({
        items: [
          {
            ...position("source-only"),
            audio_available: true,
            publication: null,
            source_variant_count: 2,
          },
          {
            ...position("published-only"),
            audio_available: true,
            publication: {},
            source_variant_count: 0,
          },
          {
            ...position("published-missing-file"),
            audio_available: false,
            publication: {},
            source_variant_count: 0,
          },
        ],
        total: 3,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
  );

  renderScreen(<LibraryScreen />);

  expect(
    await screen.findByRole("row", { name: /source-only/ }),
  ).toHaveTextContent("В наличии");
  expect(screen.getByRole("row", { name: /source-only/ })).toHaveTextContent(
    "2 исходников",
  );
  expect(screen.getByRole("row", { name: /source-only/ })).toHaveTextContent(
    "Не опубликовано",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "В наличии",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "0 исходников",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "Опубликовано",
  );
  expect(
    screen.getByRole("row", { name: /published-missing-file/ }),
  ).toHaveTextContent("Не хватает");
  expect(
    screen.getByRole("row", { name: /published-missing-file/ }),
  ).toHaveTextContent("Опубликовано");
});

it("marks unknown provider completeness instead of rendering a zero denominator", async () => {
  window.location.hash = "#/library?view=albums";
  server.use(
    http.get("*/api/library/releases", () =>
      HttpResponse.json({
        items: [
          {
            kind: "library_release",
            missing_release_group: null,
            library_release: {
              id: "release-id",
              title: "Incomplete provider cache",
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
                published_position_count: 0,
                available_position_count: 0,
                provider_position_count: null,
              },
            },
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
  );

  renderScreen(<LibraryScreen />);

  const row = await screen.findByRole("row", {
    name: /Incomplete provider cache/,
  });
  expect(row).toHaveTextContent("0 с аудио · полнота неизвестна");
  expect(row).not.toHaveTextContent("0 / 0");
});

it("loads a missing group’s edition and tracks only after explicit selection", async () => {
  window.location.hash = "#/library?view=albums";
  const groupID = "group-mbid";
  const releaseID = "edition-mbid";
  let editionRequests = 0;
  let trackRequests = 0;
  let mutationRequests = 0;
  server.use(
    http.get("*/api/library/releases", () =>
      HttpResponse.json({
        items: [
          {
            kind: "missing_release_group",
            library_release: null,
            missing_release_group: {
              provider_code: "musicbrainz",
              provider_key: groupID,
              title: "Missing album",
              artists: [],
              original_release_date: null,
            },
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
    http.get(`*/api/library/release-groups/${groupID}/editions`, () => {
      editionRequests += 1;
      return HttpResponse.json({
        provider_code: "musicbrainz",
        provider_key: groupID,
        editions: [
          {
            provider_key: releaseID,
            title: "Missing album",
            artists: [],
            edition: {
              release_date: null,
              original_release_date: null,
              country: null,
              labels: [],
              catalog_numbers: [],
              primary_type: "Album",
              secondary_types: [],
              disambiguation: "Digital edition",
            },
            track_count: null,
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
      });
    }),
    http.get(
      `*/api/library/release-groups/${groupID}/editions/${releaseID}/tracks`,
      () => {
        trackRequests += 1;
        return HttpResponse.json({
          provider_code: "musicbrainz",
          group_provider_key: groupID,
          release_provider_key: releaseID,
          complete: false,
          track_count: 1,
          tracks: [
            {
              medium: 1,
              position: 1,
              number: "1",
              title: "Known track",
              recording_title: null,
              duration_ms: null,
            },
          ],
        });
      },
    ),
    http.all("*/api/library/release-groups/*", ({ request }) => {
      if (request.method !== "GET") mutationRequests += 1;
      return new HttpResponse(null, { status: 405 });
    }),
  );

  renderScreen(<LibraryScreen />);
  const row = await screen.findByRole("row", { name: /Missing album/ });
  expect(row).toHaveTextContent("—");
  expect(editionRequests).toBe(0);
  expect(trackRequests).toBe(0);
  expect(screen.queryByText("Tracks: —")).not.toBeInTheDocument();

  fireEvent.click(within(row).getByRole("button", { name: "Browse editions" }));
  const picker = await screen.findByRole("combobox", {
    name: "Choose an edition",
  });
  await waitFor(() => expect(editionRequests).toBe(1));
  expect(picker).toHaveValue("");
  expect(trackRequests).toBe(0);
  fireEvent.change(picker, { target: { value: releaseID } });

  expect(await screen.findByText(/Known track/)).toBeInTheDocument();
  expect(
    screen.getByText(
      "The cached track list is incomplete; it may not represent the full edition.",
    ),
  ).toBeInTheDocument();
  expect(trackRequests).toBe(1);
  expect(mutationRequests).toBe(0);
});

function position(title: string) {
  return {
    id: title,
    title,
    artists: [],
    displayed_number: "1",
    local_track_id: `track-${title}`,
    medium: 1,
    position: 1,
    provider_duration_ms: null,
    recording_id: null,
    release: { id: "release-id", title: "Release", artists: [] },
    selected_media_variant_id: null,
    source_variant_count: 0,
  };
}
