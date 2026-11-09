import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";

const api = vi.hoisted(() => ({
  createLocalProviderArtist: vi.fn(),
  getGenreCatalog: vi.fn(),
  listLocalProviderArtists: vi.fn(),
  searchGenreCatalog: vi.fn(),
}));

vi.mock("../../api/generated/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/generated/client")>()),
  ...api,
}));

import { OwnCatalogScreen } from "./OwnCatalogScreen";

const genre = {
  id: "mbid-genre",
  source_name: "indie rock",
  name: "Indie Rock",
};

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  api.listLocalProviderArtists.mockResolvedValue({
    status: 200,
    data: {
      items: [
        {
          id: "external-artist",
          kind: "artist",
          name: "Candidate",
          provider_code: "musicbrainz",
          provider_key: "candidate-key",
        },
      ],
    },
  });
  api.getGenreCatalog.mockResolvedValue({
    status: 200,
    data: {
      active_operation_id: null,
      count: 1,
      items: null,
      last_attempt_at: "2026-11-07T12:00:00Z",
      last_error: null,
      refresh_status: "succeeded",
      updated_at: "2026-11-07T12:00:00Z",
    },
  });
  api.searchGenreCatalog.mockResolvedValue({
    status: 200,
    data: { items: [genre] },
  });
  api.createLocalProviderArtist.mockResolvedValue({
    status: 200,
    data: {
      id: "local-artist",
      kind: "artist",
      name: "Local Artist",
      provider_code: "local",
      provider_key: "local-artist",
    },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("creates a Local artist despite an external candidate and assigns a catalog genre", async () => {
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );

  fireEvent.change(
    screen.getByRole("searchbox", { name: "Search provider records" }),
    {
      target: { value: "candidate" },
    },
  );
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await screen.findByText(
    "Existing external provider records are shown first; select one to inspect it instead of creating a duplicate.",
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: /None of these — create a Local artist/,
    }),
  );

  fireEvent.change(screen.getByLabelText(/Name/), {
    target: { value: "Local Artist" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Artist genres" }));
  await waitFor(() => expect(api.getGenreCatalog).toHaveBeenCalledTimes(1));

  const genreSearch = screen.getByRole("searchbox", {
    name: "Search the genre catalog",
  });
  await waitFor(() => expect(genreSearch).toBeEnabled());
  fireEvent.change(genreSearch, { target: { value: "indie" } });
  const option = await screen.findByRole("option", { name: /Indie Rock/ });
  fireEvent.click(option);
  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  fireEvent.click(screen.getByRole("button", { name: "Create Local artist" }));

  await waitFor(() =>
    expect(api.createLocalProviderArtist).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Local Artist",
        genres: [
          expect.objectContaining({ id: "mbid-genre", name: "Indie Rock" }),
        ],
      }),
    ),
  );
});
