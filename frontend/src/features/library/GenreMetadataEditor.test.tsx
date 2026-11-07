import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type {
  LibraryMetadataGetResponse,
  ProviderGenreBaseline,
} from "../../api/generated/client.schemas";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";

const api = vi.hoisted(() => ({
  getGenreCatalog: vi.fn(),
  getLibraryArtistMetadata: vi.fn(),
  getLibraryRecordingMetadata: vi.fn(),
  getLibraryReleaseMetadata: vi.fn(),
  patchLibraryRecordingMetadata: vi.fn(),
  patchLibraryReleaseMetadata: vi.fn(),
  searchGenreCatalog: vi.fn(),
  patchLibraryArtistMetadata: vi.fn(),
}));

vi.mock("../../api/generated/client", () => api);

import { GenreMetadataEditor } from "./GenreMetadataEditor";

const metadata = (
  revision = 4,
  genre?: unknown,
  providerGenreBaseline?: ProviderGenreBaseline,
): LibraryMetadataGetResponse => ({
  envelope: {
    desired: genre ? { genre: genre as never } : {},
    scope: "release",
    version: 1,
  },
  ...(providerGenreBaseline === undefined
    ? {}
    : { provider_genre_baseline: providerGenreBaseline }),
  publication_applied: false,
  revision,
});

const indie = { id: "genre-id", source_name: "indie rock", name: "Indie Rock" };
const providerGenreBaseline: ProviderGenreBaseline = {
  genres: [
    { id: "mb-genre-id", source_name: "alternative", name: "Alternative" },
  ],
  provider_code: "musicbrainz",
  provider_source_id: "source-1",
  provider_key: "release-key",
  digest: "digest-1",
  evidence: { authority: 1, fetch_order: 1 },
};

function catalogStatus(overrides: Record<string, unknown> = {}) {
  return {
    active_operation_id: null,
    count: 1,
    items: null,
    last_attempt_at: "2026-11-07T12:00:00Z",
    last_error: null,
    refresh_status: "succeeded",
    updated_at: "2026-11-07T12:00:00Z",
    ...overrides,
  };
}

function renderEditor(props: ComponentProps<typeof GenreMetadataEditor>) {
  return render(
    <I18nProvider>
      <GenreMetadataEditor {...props} />
    </I18nProvider>,
  );
}

function openPicker() {
  fireEvent.click(screen.getByRole("button", { name: /Жанры/ }));
}

async function searchFor(value: string) {
  const search = screen.getByRole("searchbox", {
    name: "Поиск по каталогу жанров",
  });
  await waitFor(() => expect(search).toBeEnabled());
  fireEvent.change(search, { target: { value } });
  return search;
}

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  api.getGenreCatalog.mockResolvedValue({
    status: 200,
    data: catalogStatus(),
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("searches catalog genres with keyboard-operable multiselect and saves at the loaded revision", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(),
  });
  api.searchGenreCatalog.mockResolvedValue({
    status: 200,
    data: { items: [indie] },
  });
  api.patchLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(5, { state: "value", value: [indie] }),
  });

  renderEditor({ owner: "release", ownerId: "r-1" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  openPicker();
  expect(api.getGenreCatalog).toHaveBeenCalledTimes(1);
  await searchFor("indie");
  const result = await screen.findByRole("option", { name: /Indie Rock/ });
  result.focus();
  fireEvent.keyDown(result, { key: " " });
  expect(result).toHaveAttribute("aria-selected", "true");
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));

  await waitFor(() =>
    expect(api.patchLibraryReleaseMetadata).toHaveBeenCalledWith("r-1", {
      expected_revision: 4,
      changes: { genre: { state: "value", value: [indie] } },
    }),
  );
  expect(await screen.findByText("Indie Rock")).toBeInTheDocument();
});

it("keeps saved references readable without catalog access and preserves clear versus inheritance actions", async () => {
  api.getLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(8, { state: "value", value: [indie] }),
  });
  api.patchLibraryRecordingMetadata
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(9, { state: "explicit_empty" }),
    })
    .mockResolvedValueOnce({ status: 200, data: metadata(10) });

  renderEditor({ owner: "recording", ownerId: "rec-1" });
  expect(await screen.findByText("Indie Rock")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  await screen.findByText("Жанры явно очищены.");
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryRecordingMetadata).toHaveBeenCalledTimes(1),
  );
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Использовать наследуемые жанры" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryRecordingMetadata).toHaveBeenCalledTimes(2),
  );

  expect(
    api.patchLibraryRecordingMetadata.mock.calls.map(([id, body]) => [
      id,
      body,
    ]),
  ).toEqual([
    [
      "rec-1",
      { expected_revision: 8, changes: { genre: { state: "explicit_empty" } } },
    ],
    [
      "rec-1",
      { expected_revision: 9, changes: { genre: { state: "absent" } } },
    ],
  ]);
});

it("retains the selected draft when a revision-conflict save fails", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(12),
  });
  api.searchGenreCatalog.mockResolvedValue({
    status: 200,
    data: { items: [indie] },
  });
  api.patchLibraryReleaseMetadata.mockResolvedValue({
    status: 409,
    data: { detail: "stale revision" },
  });

  renderEditor({ owner: "release", ownerId: "r-2" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  openPicker();
  await searchFor("indie");
  const option = await screen.findByRole("option", { name: /Indie Rock/ });
  fireEvent.click(option);
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Черновик сохранён",
  );
  openPicker();
  await searchFor("indie");
  expect(
    await screen.findByRole("option", { name: /Indie Rock/ }),
  ).toHaveAttribute("aria-selected", "true");
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeInTheDocument();
});

it("keeps clear and inheritance actions available when catalog search fails", async () => {
  api.getLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(2),
  });
  api.searchGenreCatalog.mockRejectedValue(new Error("catalog offline"));
  api.patchLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(3, { state: "explicit_empty" }),
  });

  renderEditor({ owner: "recording", ownerId: "rec-offline" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  openPicker();
  await searchFor("indie");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Каталог жанров недоступен",
  );
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("button", { name: "Очистить жанры" })).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "Использовать наследуемые жанры" }),
  ).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  await screen.findByText("Жанры явно очищены.");
});

it("allows removing a saved chip offline and saves the final removal as an explicit clear", async () => {
  api.getLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(8, { state: "value", value: [indie] }),
  });
  api.getGenreCatalog.mockResolvedValue({
    status: 200,
    data: catalogStatus({ last_error: "refresh failed" }),
  });
  api.searchGenreCatalog.mockRejectedValue(new Error("search offline"));
  api.patchLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(9, { state: "explicit_empty" }),
  });

  renderEditor({ owner: "recording", ownerId: "rec-offline-chip" });
  expect(await screen.findByText("Indie Rock")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  openPicker();
  await searchFor("indie");
  await screen.findByText(/последнее обновление каталога не удалось/i);
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Удалить Indie Rock" }));
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryRecordingMetadata).toHaveBeenCalledWith(
      "rec-offline-chip",
      { expected_revision: 8, changes: { genre: { state: "explicit_empty" } } },
    ),
  );
});

it("does not let a stale owner read replace the current owner's metadata", async () => {
  let resolveFirst!: (value: {
    status: number;
    data: LibraryMetadataGetResponse;
  }) => void;
  api.getLibraryReleaseMetadata.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveFirst = resolve;
      }),
  );
  api.getLibraryReleaseMetadata.mockResolvedValueOnce({
    status: 200,
    data: metadata(14, {
      state: "value",
      value: [{ ...indie, id: "current", name: "Current Genre" }],
    }),
  });

  const view = renderEditor({ owner: "release", ownerId: "old-owner" });
  view.rerender(
    <I18nProvider>
      <GenreMetadataEditor owner="release" ownerId="new-owner" />
    </I18nProvider>,
  );
  expect(await screen.findByText("Current Genre")).toBeInTheDocument();
  await act(async () =>
    resolveFirst({
      status: 200,
      data: metadata(13, { state: "value", value: [indie] }),
    }),
  );
  expect(screen.getByText("Current Genre")).toBeInTheDocument();
  expect(screen.queryByText("Indie Rock")).not.toBeInTheDocument();
});

it("deduplicates same-tick Save presses while a metadata write is pending", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(),
  });
  let resolveWrite!: (value: {
    status: number;
    data: LibraryMetadataGetResponse;
  }) => void;
  api.patchLibraryReleaseMetadata.mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveWrite = resolve;
      }),
  );

  renderEditor({ owner: "release", ownerId: "save-race" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  const save = screen.getByRole("button", { name: "Сохранить" });
  fireEvent.click(save);
  fireEvent.click(save);
  expect(api.patchLibraryReleaseMetadata).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Жанры релиза" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Очистить жанры" })).toBeDisabled();
  await act(async () =>
    resolveWrite({
      status: 200,
      data: metadata(5, { state: "explicit_empty" }),
    }),
  );
});

it("does not silently rebase a dirty draft when a refresh read finishes", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValueOnce({
    status: 200,
    data: metadata(4),
  });
  let resolveRefresh!: (value: {
    status: number;
    data: LibraryMetadataGetResponse;
  }) => void;
  api.getLibraryReleaseMetadata.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveRefresh = resolve;
      }),
  );

  const view = renderEditor({ owner: "release", ownerId: "refresh-race" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  view.rerender(
    <I18nProvider>
      <GenreMetadataEditor
        owner="release"
        ownerId="refresh-race"
        refreshToken="new-parent-revision"
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(api.getLibraryReleaseMetadata).toHaveBeenCalledTimes(2),
  );
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  await act(async () => resolveRefresh({ status: 200, data: metadata(5) }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Метаданные изменились во время редактирования",
  );
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
  expect(api.patchLibraryReleaseMetadata).not.toHaveBeenCalled();
});

it("patches artist genres through the artist metadata revision", async () => {
  api.getLibraryArtistMetadata.mockResolvedValue({
    status: 200,
    data: metadata(7),
  });
  api.patchLibraryArtistMetadata.mockResolvedValue({
    status: 200,
    data: metadata(8, { state: "explicit_empty" }),
  });

  renderEditor({ owner: "artist", ownerId: "artist-1" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryArtistMetadata).toHaveBeenCalledWith("artist-1", {
      expected_revision: 7,
      changes: { genre: { state: "explicit_empty" } },
    }),
  );
});

it("shows provider genres separately from local overrides and never patches them", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValueOnce({
    status: 200,
    data: metadata(
      7,
      { state: "value", value: [indie] },
      providerGenreBaseline,
    ),
  });
  api.getLibraryReleaseMetadata.mockRejectedValueOnce(
    new Error("refresh failed"),
  );
  api.patchLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(8, { state: "explicit_empty" }),
  });

  renderEditor({ owner: "release", ownerId: "genre-baseline" });
  expect(await screen.findByText("Alternative")).toBeInTheDocument();
  expect(screen.getByText("Indie Rock")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));

  await waitFor(() =>
    expect(api.patchLibraryReleaseMetadata).toHaveBeenCalledWith(
      "genre-baseline",
      { expected_revision: 7, changes: { genre: { state: "explicit_empty" } } },
    ),
  );
  await waitFor(() =>
    expect(api.getLibraryReleaseMetadata).toHaveBeenCalledTimes(2),
  );
  expect(screen.getByText("Alternative")).toBeInTheDocument();
  expect(screen.queryByText("Indie Rock")).not.toBeInTheDocument();
  expect(
    screen.queryByText("Данные о жанрах MusicBrainz недоступны."),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("distinguishes an unavailable provider baseline from an accepted empty list", async () => {
  api.getLibraryReleaseMetadata
    .mockResolvedValueOnce({ status: 200, data: metadata() })
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(4, undefined, { ...providerGenreBaseline, genres: [] }),
    });

  const view = renderEditor({
    owner: "release",
    ownerId: "baseline-availability",
  });
  expect(
    await screen.findByText("Данные о жанрах MusicBrainz недоступны."),
  ).toBeInTheDocument();
  view.rerender(
    <I18nProvider>
      <GenreMetadataEditor
        owner="release"
        ownerId="baseline-availability"
        refreshToken="provider-refreshed"
      />
    </I18nProvider>,
  );
  expect(
    await screen.findByText("MusicBrainz вернул пустой список жанров."),
  ).toBeInTheDocument();
});

it("reloads provider genres after refresh without discarding a same-revision draft", async () => {
  api.getLibraryReleaseMetadata
    .mockResolvedValueOnce({ status: 200, data: metadata(4) })
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(4, undefined, providerGenreBaseline),
    });

  const view = renderEditor({
    owner: "release",
    ownerId: "provider-refresh-draft",
  });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  view.rerender(
    <I18nProvider>
      <GenreMetadataEditor
        owner="release"
        ownerId="provider-refresh-draft"
        refreshToken="provider-refreshed"
      />
    </I18nProvider>,
  );

  expect(await screen.findByText("Alternative")).toBeInTheDocument();
  expect(screen.getByText("Жанры явно очищены.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  expect(
    screen.queryByText(/Метаданные изменились во время редактирования/),
  ).not.toBeInTheDocument();
});

it("lets a committed PATCH supersede a stale external read", async () => {
  type MetadataResult = {
    status: number;
    data: LibraryMetadataGetResponse;
  };
  let resolvePatch!: (value: MetadataResult) => void;
  let resolveExternal!: (value: MetadataResult) => void;
  const oldProviderBaseline: ProviderGenreBaseline = {
    ...providerGenreBaseline,
    genres: [{ id: "old-genre", source_name: "old", name: "Old Provider" }],
  };
  api.getLibraryReleaseMetadata
    .mockResolvedValueOnce({ status: 200, data: metadata(4) })
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveExternal = resolve;
        }),
    )
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(5, undefined, providerGenreBaseline),
    })
    .mockResolvedValue({
      status: 200,
      data: metadata(5, undefined, providerGenreBaseline),
    });
  api.patchLibraryReleaseMetadata
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePatch = resolve;
        }),
    )
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(6, { state: "explicit_empty" }),
    });

  const view = renderEditor({
    owner: "release",
    ownerId: "stale-patch-read",
  });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryReleaseMetadata).toHaveBeenCalledTimes(1),
  );

  view.rerender(
    <I18nProvider>
      <GenreMetadataEditor
        owner="release"
        ownerId="stale-patch-read"
        refreshToken="external-read"
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(api.getLibraryReleaseMetadata).toHaveBeenCalledTimes(2),
  );

  await act(async () =>
    resolvePatch({
      status: 200,
      data: metadata(5, { state: "explicit_empty" }),
    }),
  );
  expect(await screen.findByText("Жанры явно очищены.")).toBeInTheDocument();

  await act(async () =>
    resolveExternal({
      status: 200,
      data: metadata(
        6,
        { state: "value", value: [indie] },
        oldProviderBaseline,
      ),
    }),
  );

  expect(await screen.findByText("Alternative")).toBeInTheDocument();
  expect(screen.queryByText("Old Provider")).not.toBeInTheDocument();
  expect(screen.queryByText("Indie Rock")).not.toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Изменить" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() =>
    expect(api.patchLibraryReleaseMetadata).toHaveBeenLastCalledWith(
      "stale-patch-read",
      { expected_revision: 5, changes: { genre: { state: "explicit_empty" } } },
    ),
  );
});

it("rereads the provider baseline when the saved callback resolves without changing props", async () => {
  api.getLibraryArtistMetadata
    .mockResolvedValueOnce({ status: 200, data: metadata(4) })
    .mockResolvedValueOnce({
      status: 200,
      data: metadata(5, undefined, providerGenreBaseline),
    });
  api.patchLibraryArtistMetadata.mockResolvedValue({
    status: 200,
    data: metadata(5, { state: "explicit_empty" }),
  });
  const onSaved = vi.fn().mockResolvedValue(undefined);

  renderEditor({
    owner: "artist",
    ownerId: "callback-baseline",
    onSaved,
  });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));

  expect(await screen.findByText("Alternative")).toBeInTheDocument();
  expect(onSaved).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByText("Не удалось сохранить жанры."),
  ).not.toBeInTheDocument();
});

it("distinguishes an unloaded catalog from an empty search result", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(),
  });
  api.getGenreCatalog.mockResolvedValue({
    status: 200,
    data: catalogStatus({ count: 0, updated_at: null, items: [] }),
  });
  api.searchGenreCatalog.mockResolvedValue({
    status: 200,
    data: { items: [indie] },
  });

  renderEditor({ owner: "release", ownerId: "unloaded-catalog" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  openPicker();
  expect(
    await screen.findByText("Каталог жанров ещё не загружен."),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("searchbox", { name: "Поиск по каталогу жанров" }),
  ).toBeDisabled();
  expect(api.searchGenreCatalog).not.toHaveBeenCalled();
});

it("cancels Escape only in the focused editor and preserves another editor's draft", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(),
  });
  api.getLibraryRecordingMetadata.mockResolvedValue({
    status: 200,
    data: metadata(7),
  });

  render(
    <I18nProvider>
      <GenreMetadataEditor owner="release" ownerId="escape-release" />
      <GenreMetadataEditor owner="recording" ownerId="escape-recording" />
    </I18nProvider>,
  );
  const releaseEditor = await screen.findByRole("region", {
    name: "Жанры релиза",
  });
  const recordingEditor = screen.getByRole("region", {
    name: "Жанры записи",
  });
  const release = within(releaseEditor);
  const recording = within(recordingEditor);
  await waitFor(() => {
    expect(release.getByRole("button", { name: "Изменить" })).toBeEnabled();
    expect(recording.getByRole("button", { name: "Изменить" })).toBeEnabled();
  });

  fireEvent.click(release.getByRole("button", { name: "Изменить" }));
  fireEvent.click(release.getByRole("button", { name: "Очистить жанры" }));
  fireEvent.click(recording.getByRole("button", { name: "Изменить" }));
  fireEvent.click(recording.getByRole("button", { name: "Очистить жанры" }));
  const focusedReleaseAction = release.getByRole("button", {
    name: "Очистить жанры",
  });
  focusedReleaseAction.focus();
  fireEvent.keyDown(focusedReleaseAction, { key: "Escape" });

  expect(release.getByRole("button", { name: "Изменить" })).toBeInTheDocument();
  expect(recording.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  expect(recording.getByText("Жанры явно очищены.")).toBeInTheDocument();
});

it("closes the genre dialog on Escape without canceling the parent draft", async () => {
  api.getLibraryReleaseMetadata.mockResolvedValue({
    status: 200,
    data: metadata(),
  });

  renderEditor({ owner: "release", ownerId: "dialog-escape" });
  await screen.findByText(/Переопределение жанров не задано/);
  fireEvent.click(screen.getByRole("button", { name: "Изменить" }));
  fireEvent.click(screen.getByRole("button", { name: "Очистить жанры" }));
  openPicker();
  const search = screen.getByRole("searchbox", {
    name: "Поиск по каталогу жанров",
  });
  await waitFor(() => expect(search).toBeEnabled());
  search.focus();
  fireEvent.keyDown(search, { key: "Escape" });

  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  expect(screen.getByText("Жанры явно очищены.")).toBeInTheDocument();
});
