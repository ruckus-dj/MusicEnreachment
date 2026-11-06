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
  server.use(
    http.get("*/api/library/artists", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("limit")).toBe("50");
      expect(url.searchParams.get("publication")).toBe("all");
      expect(url.searchParams.get("availability")).toBe("all");
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
  fireEvent.click(screen.getByRole("radio", { name: "Не хватает" }));
  await waitFor(() => {
    expect(window.location.hash).toContain("availability=missing");
    expect(window.location.hash).not.toContain("offset=");
  });
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
