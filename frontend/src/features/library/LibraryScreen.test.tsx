import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { LibraryArtistScreen } from "./LibraryArtistScreen";
import { LibraryScreen } from "./LibraryScreen";

const artistId = "123e4567-e89b-42d3-a456-426614174000";
const releaseId = "123e4567-e89b-42d3-a456-426614174001";
const summary = {
  artist_count: 1,
  release_count: 1,
  position_count: 2,
  published_position_count: 1,
};
const credit = {
  artist_id: artistId,
  credit_name: "A Artist",
  join_phrase: " & ",
};
const release = {
  id: releaseId,
  title: "Actual Album",
  artists: [credit],
  edition: {
    release_date: "2024-01-01",
    original_release_date: null,
    country: "US",
    labels: [],
    catalog_numbers: [],
    primary_type: "Album",
    secondary_types: [],
    disambiguation: "",
  },
  provider_links: [],
  cover: {
    release_id: releaseId,
    revision: 1,
    mode: "automatic",
    selected_generation: null,
  },
  counts: {
    position_count: 2,
    published_position_count: 1,
    available_position_count: 1,
    provider_position_count: null,
  },
};
const position = {
  id: "123e4567-e89b-42d3-a456-426614174002",
  local_track_id: "local-track",
  recording_id: null,
  release: { id: releaseId, title: "Actual Album", artists: [credit] },
  medium: 1,
  position: 1,
  displayed_number: "1",
  title: "Real Song",
  artists: [credit],
  provider_duration_ms: null,
  source_variant_count: 0,
  selected_media_variant_id: null,
  publication: null,
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
  server.use(
    http.get("*/api/library/artists", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("limit")).toBe("50");
      expect(url.searchParams.get("publication")).toBe("all");
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
  expect(await screen.findByRole("link", { name: /A Artist/ })).toHaveAttribute(
    "href",
    `#/library/artists/${artistId}?view=artists&publication=all`,
  );
  expect(
    screen.getByText("1 артистов · 1 релизов · 2 позиций · 1 опубликовано"),
  ).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "jazz" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Найти" }));
  await waitFor(() => expect(window.location.hash).toContain("q=jazz"));
  fireEvent.click(await screen.findByRole("button", { name: "Далее" }));
  await waitFor(() => expect(window.location.hash).toContain("offset=50"));
});

it("renders album and position tables and does not invent a provider denominator", async () => {
  window.location.hash = "#/library?view=albums";
  server.use(
    http.get("*/api/library/releases", () =>
      HttpResponse.json({
        items: [release],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
    http.get("*/api/library/positions", () =>
      HttpResponse.json({
        items: [position],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
  );
  const view = renderScreen(<LibraryScreen />);
  expect(
    await screen.findByRole("link", { name: "Actual Album" }),
  ).toHaveAttribute("href", `#/library/releases/${releaseId}`);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByText("—")).toBeInTheDocument();
  window.location.hash = "#/library?view=tracks";
  view.rerender(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <LibraryScreen />
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Real Song")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Real Song" })).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}/positions/${position.id}`,
  );
  expect(screen.getByRole("link", { name: "Actual Album" })).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}`,
  );
  expect(screen.getByText("Не опубликовано")).toBeInTheDocument();
});

it("loads an artist's releases and requests positions only when each album is expanded", async () => {
  window.location.hash = `#/library/artists/${artistId}?view=artists&publication=all`;
  const positionRequests: string[] = [];
  server.use(
    http.get(`*/api/library/artists/${artistId}`, () =>
      HttpResponse.json({
        id: artistId,
        name: "A Artist",
        disambiguation: null,
        genres: [],
        provider_links: [],
        release_count: 1,
        position_count: 2,
        published_position_count: 1,
      }),
    ),
    http.get("*/api/library/releases", () =>
      HttpResponse.json({
        items: [release],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
    http.get("*/api/library/positions", ({ request }) => {
      positionRequests.push(new URL(request.url).search);
      return HttpResponse.json({
        items: [position],
        total: 2,
        limit: 50,
        offset: 0,
        summary,
      });
    }),
  );
  renderScreen(<LibraryArtistScreen artistId={artistId} />);
  expect(
    await screen.findByRole("heading", { name: "A Artist" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "Actual Album" }),
  ).toBeInTheDocument();
  expect(positionRequests).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Показать позиции" }));
  expect(
    await screen.findByRole("link", { name: /Real Song/ }),
  ).toBeInTheDocument();
  expect(positionRequests).toHaveLength(1);
});
