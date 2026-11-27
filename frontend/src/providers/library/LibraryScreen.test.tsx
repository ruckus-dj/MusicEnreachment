import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
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
import { I18nProvider, useI18n } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ApplicationRouteOutlet } from "../../ui/app/ApplicationRouteOutlet";
import { ProductionLibraryCatalogBinder } from "./LibraryScreen";

const artistId = "123e4567-e89b-42d3-a456-426614174000";
const secondArtistId = "223e4567-e89b-42d3-a456-426614174001";
const summary = {
  artist_count: 1,
  release_count: 1,
  position_count: 2,
  published_position_count: 1,
};

function renderScreen(screen: ReactNode, locale: "en" | "ru" = "ru") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", locale);
  vi.stubGlobal("localStorage", storage);
  return render(
    <I18nProvider>
      <QueryClientProvider client={client}>{screen}</QueryClientProvider>
    </I18nProvider>,
  );
}

function LibraryCatalogRoute() {
  return (
    <ApplicationRouteOutlet
      route="library-catalog"
      provider={ProductionLibraryCatalogBinder}
      providerProps={{}}
    />
  );
}

function LocaleControls() {
  const { setPreference } = useI18n();
  return (
    <button type="button" onClick={() => setPreference("ru")}>
      switch-to-ru
    </button>
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
          {
            id: secondArtistId,
            name: "B Artist",
            disambiguation: null,
            genres: [],
            provider_links: [],
            release_count: 1,
            position_count: 1,
            available_position_count: 1,
            published_position_count: 0,
          },
        ],
        total: 51,
        limit: 50,
        offset: Number(url.searchParams.get("offset")),
        summary,
      });
    }),
    http.post("*/api/library/selection/resolve", async ({ request }) => {
      expect(await request.json()).toEqual({
        artist_ids: [artistId, secondArtistId],
        release_ids: [],
      });
      return HttpResponse.json({
        positions: [
          {
            release_id: "release-a",
            position_id: "position-a",
            title: "A Track",
            release_title: "A Album",
            artist_ids: [artistId],
          },
          {
            release_id: "release-b",
            position_id: "position-b",
            title: "B Track",
            release_title: "B Album",
            artist_ids: [secondArtistId],
          },
        ],
      });
    }),
  );
  renderScreen(<LibraryCatalogRoute />);
  expect(screen.getByRole("tab", { name: "Артисты" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(
    screen.getByRole("radiogroup", { name: "Состояние" }),
  ).toBeInTheDocument();
  const artistLink = await screen.findByRole("link", { name: /A Artist/ });
  expect(artistLink).toHaveAttribute(
    "href",
    `#/library/artists/${artistId}?view=artists&publication=all&availability=all`,
  );
  expect(
    screen.getByText("1 артист · 1 релиз · 2 позиции · 1 опубликовано"),
  ).toBeInTheDocument();
  const artistCard = artistLink.closest('[role="row"]');
  expect(artistCard).not.toBeNull();
  const artistCardScope = within(artistCard as HTMLElement);
  expect(artistCardScope.getByText("Аудио")).toBeVisible();
  expect(artistCardScope.getAllByText("1 / 2")).toHaveLength(2);
  expect(
    screen.getByRole("navigation", { name: "Страницы медиатеки" }),
  ).toHaveTextContent("Страница 1 из 2 · Всего: 51");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();

  const artistCheckbox = screen.getByRole("checkbox", {
    name: "Выбрать позиции артиста A Artist",
  });
  const pageCheckbox = screen.getByRole("checkbox", {
    name: "Выбрать на этой странице",
  });
  await waitFor(() => expect(artistCheckbox).toBeEnabled());
  fireEvent.click(artistCheckbox);
  const dock = await screen.findByRole("region", {
    name: "Выбранные позиции",
  });
  expect(dock).toHaveTextContent("Выбрано позиций: 1");
  expect(artistCheckbox).toBeChecked();
  expect(pageCheckbox).not.toBeChecked();

  fireEvent.click(pageCheckbox);
  await waitFor(() => expect(dock).toHaveTextContent("Выбрано позиций: 2"));
  expect(artistCheckbox).toBeChecked();
  expect(pageCheckbox).toBeChecked();

  // The search field is debounced: several keystrokes coalesce into one request.
  const search = screen.getByRole("searchbox");
  fireEvent.change(search, { target: { value: "j" } });
  fireEvent.change(search, { target: { value: "ja" } });
  fireEvent.change(search, { target: { value: "jazz" } });
  await waitFor(() => expect(window.location.hash).toContain("q=jazz"));
  await waitFor(() => expect(artistRequests).toBe(2));

  fireEvent.click(screen.getByRole("button", { name: "Следующая страница" }));
  await waitFor(() => expect(window.location.hash).toContain("offset=50"));
  await waitFor(() => expect(artistRequests).toBe(3));
  await waitFor(() =>
    expect(
      screen.getByRole("navigation", { name: "Страницы медиатеки" }),
    ).toHaveTextContent("Страница 2 из 2 · Всего: 51"),
  );

  expectedAvailability = "missing";
  fireEvent.click(screen.getByRole("radio", { name: "Не хватает" }));
  await waitFor(() => {
    expect(window.location.hash).toContain("availability=missing");
    expect(window.location.hash).not.toContain("offset=");
  });
  await waitFor(() => expect(artistRequests).toBe(4));
});

it("re-adopts the accepted URL when an immediate Back precedes the filter's queued hash change", async () => {
  const all = "#/library?view=artists&publication=all&availability=all";
  const published =
    "#/library?view=artists&publication=published&availability=all";
  window.location.hash = all;
  server.use(
    http.get("*/api/library/artists", () =>
      HttpResponse.json({
        items: [
          {
            id: artistId,
            name: "A Artist",
            disambiguation: null,
            genres: [],
            provider_links: [],
            release_count: 1,
            position_count: 1,
            available_position_count: 1,
            published_position_count: 1,
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      }),
    ),
  );
  renderScreen(<LibraryCatalogRoute />, "en");
  expect(screen.getByRole("radio", { name: "All" })).toBeChecked();
  await screen.findByRole("link", { name: /A Artist/ });

  fireEvent.click(screen.getByRole("radio", { name: "Published" }));
  expect(screen.getByRole("radio", { name: "Published" })).toBeChecked();
  expect(window.location.hash).toBe(published);

  // Back restores the earlier entry before the filter write's queued native
  // event arrives; that stale event still reports the superseded destination.
  act(() => {
    window.history.replaceState(null, "", all);
    window.dispatchEvent(
      new HashChangeEvent("hashchange", {
        oldURL: new URL(all, window.location.href).href,
        newURL: new URL(published, window.location.href).href,
      }),
    );
  });

  expect(screen.getByRole("radio", { name: "All" })).toBeChecked();
  expect(screen.getByRole("radio", { name: "Published" })).not.toBeChecked();
  expect(window.location.hash).toBe(all);
});

it("keeps independent filter axes from a mixed deep link and removes only the touched axis", async () => {
  window.location.hash =
    "#/library?view=artists&publication=published&availability=missing";
  const requests: string[] = [];
  server.use(
    http.get("*/api/library/artists", ({ request }) => {
      const url = new URL(request.url);
      requests.push(
        `${url.searchParams.get("publication")}/${url.searchParams.get("availability")}`,
      );
      return HttpResponse.json({
        items: [
          {
            id: artistId,
            name: "A Artist",
            disambiguation: null,
            genres: [],
            provider_links: [],
            release_count: 1,
            position_count: 2,
            available_position_count: 1,
            published_position_count: 1,
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      });
    }),
  );
  renderScreen(<LibraryCatalogRoute />, "en");

  const filters = await screen.findByRole("grid", { name: "Active filters" });
  expect(within(filters).getByRole("row", { name: /Published/ })).toBeVisible();
  expect(
    within(filters).getByRole("row", { name: /Missing audio/ }),
  ).toBeVisible();
  await waitFor(() => expect(requests.at(-1)).toBe("published/missing"));

  fireEvent.click(
    within(within(filters).getByRole("row", { name: /Published/ })).getByRole(
      "button",
    ),
  );

  await waitFor(() =>
    expect(window.location.hash).toContain("publication=all"),
  );
  expect(window.location.hash).toContain("availability=missing");
  await waitFor(() => expect(requests.at(-1)).toBe("all/missing"));
  expect(
    within(filters).queryByRole("row", { name: /Published/ }),
  ).not.toBeInTheDocument();
  expect(
    within(filters).getByRole("row", { name: /Missing audio/ }),
  ).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "Reset all" }));
  await waitFor(() => {
    expect(window.location.hash).toContain("publication=all");
    expect(window.location.hash).toContain("availability=all");
  });
  await waitFor(() => expect(requests.at(-1)).toBe("all/all"));
});

it("retains the catalog route and search state without refetching when locale changes", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  window.location.hash =
    "#/library?view=artists&q=rock&publication=all&availability=available&offset=50";
  let requests = 0;
  server.use(
    http.get("*/api/library/artists", ({ request }) => {
      requests += 1;
      const url = new URL(request.url);
      expect(url.searchParams.get("q")).toBe("rock");
      expect(url.searchParams.get("publication")).toBe("all");
      expect(url.searchParams.get("availability")).toBe("available");
      expect(url.searchParams.get("offset")).toBe("50");
      return HttpResponse.json({
        items: [
          {
            id: artistId,
            name: "A Artist",
            disambiguation: null,
            genres: [],
            provider_links: [],
            release_count: 1,
            position_count: 2,
            available_position_count: 1,
            published_position_count: 1,
          },
        ],
        total: 51,
        limit: 50,
        offset: 50,
        summary,
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <I18nProvider>
      <QueryClientProvider client={client}>
        <LocaleControls />
        <LibraryCatalogRoute />
      </QueryClientProvider>
    </I18nProvider>,
  );

  expect(
    await screen.findByRole("link", { name: /A Artist/ }),
  ).toBeInTheDocument();
  expect(screen.getByRole("searchbox")).toHaveValue("rock");
  expect(screen.getByRole("tab", { name: "All releases" })).toBeInTheDocument();
  expect(screen.getByRole("radio", { name: "Has audio" })).toBeChecked();
  const route = window.location.hash;

  fireEvent.click(screen.getByRole("button", { name: "switch-to-ru" }));

  expect(
    await screen.findByRole("link", { name: /A Artist/ }),
  ).toBeInTheDocument();
  expect(screen.getByRole("searchbox")).toHaveValue("rock");
  expect(screen.getByRole("tab", { name: "Все релизы" })).toBeInTheDocument();
  expect(screen.getByRole("radio", { name: "В наличии" })).toBeChecked();
  expect(window.location.hash).toBe(route);
  expect(requests).toBe(1);
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

  renderScreen(<LibraryCatalogRoute />);

  expect(
    await screen.findByRole("row", { name: /source-only/ }),
  ).toHaveTextContent("В наличии");
  expect(screen.getByRole("row", { name: /source-only/ })).toHaveTextContent(
    "Исходники: 2",
  );
  expect(screen.getByRole("row", { name: /source-only/ })).toHaveTextContent(
    "Не опубликовано",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "В наличии",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "Исходники: 0",
  );
  expect(screen.getByRole("row", { name: /published-only/ })).toHaveTextContent(
    "Опубликовано",
  );
  expect(
    screen.getByRole("row", { name: /published-missing-file/ }),
  ).toHaveTextContent("Отсутствует");
  expect(
    screen.getByRole("row", { name: /published-missing-file/ }),
  ).toHaveTextContent("Опубликовано");
});

it("marks unknown provider completeness instead of rendering a zero denominator", async () => {
  window.location.hash = "#/library?view=albums";
  server.use(
    http.post("*/api/library/selection/resolve", () =>
      HttpResponse.json({ positions: [] }),
    ),
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

  renderScreen(<LibraryCatalogRoute />);

  const row = await screen.findByRole("row", {
    name: /Incomplete provider cache/,
  });
  expect(row).toHaveTextContent("Полнота неизвестна");
  expect(row).not.toHaveTextContent("0 / 0");
});

it.each([
  { total: 1, needsChoice: false },
  { total: 51, needsChoice: true },
])("requires an edition choice only when the group has multiple editions ($total)", async ({
  total,
  needsChoice,
}) => {
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
        total,
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

  renderScreen(<LibraryCatalogRoute />, "en");
  const row = await screen.findByRole("row", { name: /Missing album/ });
  expect(row).toHaveTextContent("—");
  expect(editionRequests).toBe(0);
  expect(trackRequests).toBe(0);
  expect(screen.queryByText("Tracks: —")).not.toBeInTheDocument();

  expect(within(row).queryByRole("checkbox")).not.toBeInTheDocument();
  fireEvent.click(within(row).getByRole("button", { name: "Missing album" }));
  const details = row.nextElementSibling as HTMLElement | null;
  expect(details).not.toBeNull();
  if (!details) throw new Error("Missing full-width edition row");
  expect(details).toHaveAttribute("role", "row");
  expect(details.firstElementChild).toHaveAttribute("colspan", "9");
  expect(details).not.toHaveTextContent(groupID);
  if (needsChoice) {
    const picker = await within(details).findByRole("combobox", {
      name: "Choose an edition",
    });
    expect(picker).toHaveValue("");
    expect(trackRequests).toBe(0);
    fireEvent.change(picker, { target: { value: releaseID } });
  }

  expect(await within(details).findByText(/Known track/)).toBeInTheDocument();
  expect(editionRequests).toBe(1);
  if (!needsChoice) {
    expect(within(details).queryByRole("combobox")).not.toBeInTheDocument();
  }
  expect(
    within(details).getByText(
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

it("keeps a page selection off-page and previews only the frozen position", async () => {
  window.location.hash = "#/library?view=tracks";
  const requests: unknown[] = [];
  server.use(
    http.get("*/api/library/positions", ({ request }) => {
      const query = new URL(request.url).searchParams.get("q");
      return HttpResponse.json({
        items: [
          {
            ...position(query ? "Other" : "Selected"),
            audio_available: true,
            publication: null,
          },
        ],
        total: 1,
        limit: 50,
        offset: 0,
        summary,
      });
    }),
    http.post("*/api/library/publication/preview", async ({ request }) => {
      requests.push(await request.json());
      return HttpResponse.json({
        mode: "ordinary",
        items: [
          {
            release_id: "release-id",
            position_id: "Selected",
            disposition: "skipped",
            reason: "missing_audio",
            path_clash: false,
          },
        ],
      });
    }),
  );
  renderScreen(<LibraryCatalogRoute />, "en");
  const grid = await screen.findByRole("grid", { name: "Library tracks" });
  await within(grid).findByRole("row", { name: /Selected/ });
  const page = within(grid).getByRole("checkbox", {
    name: "Select all tracks on this page",
  });
  fireEvent.click(page);
  expect(page).toBeChecked();
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "other" },
  });
  await screen.findByRole("row", { name: /Other/ });
  const dock = screen.getByRole("region", { name: "Selected positions" });
  expect(dock).toHaveTextContent("Selected positions: 1 · Not on this page: 1");
  fireEvent.click(
    within(dock).getByRole("button", { name: "Preview publication" }),
  );
  const preview = await screen.findByRole("dialog", {
    name: "Preview publication",
  });
  expect(
    await within(preview).findByText(/Audio is unavailable/),
  ).toBeInTheDocument();
  expect(requests).toEqual([
    { positions: [{ release_id: "release-id", position_id: "Selected" }] },
  ]);
  expect(within(preview).getAllByRole("button")).toHaveLength(1);
});
