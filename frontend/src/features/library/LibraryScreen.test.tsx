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
