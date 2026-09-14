// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeSettingsDraft } from "../types";
import { SettingsScreen } from "./SettingsScreen";

afterEach(cleanup);

const draft: RuntimeSettingsDraft = {
  confidence_threshold: 0.8,
  timeout_seconds: 30,
  retry_delay_seconds: 10,
  max_attempts: 3,
  worker_pools: {
    filesystem_scan: 4,
    acoustid_analysis: 1,
    musicbrainz_analysis: 4,
    candidate_selection: 4,
    folder_release_selection: 2,
    final_publish: 4,
    selection_refresh: 4,
    lrclib_fetch: 1,
    artwork_enrichment: 2,
    reconciliation_scan: 1,
    lidarr_intake: 1,
  },
  musicbrainz_enabled: true,
  musicbrainz_user_agent: "Music Ingest",
  musicbrainz_host: "https://musicbrainz.org",
  musicbrainz_request_delay_seconds: 1.5,
  acoustid_enabled: false,
  acoustid_request_delay_seconds: 1 / 3,
  acoustid_client_key: "",
  artwork_enabled: true,
  lrclib_enabled: true,
  lrclib_host: "https://lrclib.net",
  lrclib_user_agent: "Music Ingest",
  lrclib_timeout_seconds: 15,
  lrclib_max_attempts: 3,
  lrclib_request_delay_seconds: 0.3,
  lrclib_max_response_bytes: 4_194_304,
  lrclib_match_confidence_threshold: 0.7,
};

type SettingsScreenProps = Parameters<typeof SettingsScreen>[0];

const settingsScreenProps = {
  draft,
  loading: false,
  saving: false,
  onChange: vi.fn(),
  onSave: vi.fn(),
  genres: null,
  genreSearch: "",
  onGenreSearch: vi.fn(),
  genreLoading: false,
  genreSyncing: false,
  onSyncGenres: vi.fn(),
  sourceRoots: [],
  sourceRootsLoading: false,
  sourceRootsError: "",
  sourceRootCreating: false,
  sourceRootRemoving: false,
  storageBrowser: null,
  storageConfig: null,
  storageOutputPreview: null,
  storageLoading: false,
  onCreateSourceRoot: vi.fn(),
  onRemoveSourceRoot: vi.fn(),
  onBrowseStorage: vi.fn(),
  onPreviewStorageOutput: vi.fn(),
  onMoveStorageOutput: vi.fn(),
} satisfies SettingsScreenProps;

function renderSettings(overrides: Partial<SettingsScreenProps> = {}) {
  render(<SettingsScreen {...settingsScreenProps} {...overrides} />);
}

describe("SettingsScreen source roots", () => {
  it("lists a persisted root and removes it through the provided callback", () => {
    const onRemoveRoot = vi.fn();

    render(
      <SettingsScreen
        draft={draft}
        loading={false}
        saving={false}
        onChange={vi.fn()}
        onSave={vi.fn()}
        genres={null}
        genreSearch=""
        onGenreSearch={vi.fn()}
        genreLoading={false}
        genreSyncing={false}
        onSyncGenres={vi.fn()}
        sourceRoots={[
          {
            id: "root-archive",
            display_name: "Архив",
            canonical_path: "/data/sources/archive",
            enabled: true,
            scan_state: "never_scanned",
          },
        ]}
        sourceRootsLoading={false}
        sourceRootsError=""
        sourceRootCreating={false}
        sourceRootRemoving={false}
        storageBrowser={{
          path: "/data/sources/archive",
          parent_path: "/data/sources",
          items: [],
        }}
        storageConfig={null}
        storageOutputPreview={null}
        storageLoading={false}
        onCreateSourceRoot={vi.fn()}
        onRemoveSourceRoot={onRemoveRoot}
        onBrowseStorage={vi.fn()}
        onPreviewStorageOutput={vi.fn()}
        onMoveStorageOutput={vi.fn()}
      />,
    );

    expect(screen.getByTestId("source-root-list").textContent).toContain("Архив");
    expect(screen.getByTestId("source-root-list").textContent).toContain("/data/sources/archive");
    expect(
      screen.getByTestId("source-root-list").querySelector(".source-root-list"),
    ).not.toBeNull();

    fireEvent.click(screen.getByTestId("source-root-remove-root-archive"));

    expect(onRemoveRoot).toHaveBeenCalledWith("root-archive");
  });

  it("announces background storage migration and disables a second move", () => {
    const onRemoveRoot = vi.fn();

    render(
      <SettingsScreen
        draft={draft}
        loading={false}
        saving={false}
        onChange={vi.fn()}
        onSave={vi.fn()}
        genres={null}
        genreSearch=""
        onGenreSearch={vi.fn()}
        genreLoading={false}
        genreSyncing={false}
        onSyncGenres={vi.fn()}
        sourceRoots={[
          {
            id: "root-archive",
            display_name: "Архив",
            canonical_path: "/data/sources/archive",
            enabled: true,
            scan_state: "never_scanned",
          },
        ]}
        sourceRootsLoading={false}
        sourceRootsError=""
        sourceRootCreating={false}
        sourceRootRemoving={false}
        storageBrowser={{
          path: "/data/sources/archive",
          parent_path: "/data/sources",
          items: [],
        }}
        storageConfig={{ output_root: "/media/old", state: "migrating", generation: 1 }}
        storageOutputPreview={null}
        storageLoading={false}
        onCreateSourceRoot={vi.fn()}
        onRemoveSourceRoot={onRemoveRoot}
        onBrowseStorage={vi.fn()}
        onPreviewStorageOutput={vi.fn()}
        onMoveStorageOutput={vi.fn()}
      />,
    );

    expect(screen.getByRole("status").textContent).toContain("Перенос выполняется в фоне");
    expect(
      within(screen.getByRole("group", { name: "Хранилище контейнера" }))
        .getByRole("button", { name: "Выбрать папку" })
        .hasAttribute("disabled"),
    ).toBe(true);
  });

  it("submits a new root and exposes an accessible API error", () => {
    const onCreateSourceRoot = vi.fn();

    render(
      <SettingsScreen
        draft={draft}
        loading={false}
        saving={false}
        onChange={vi.fn()}
        onSave={vi.fn()}
        genres={null}
        genreSearch=""
        onGenreSearch={vi.fn()}
        genreLoading={false}
        genreSyncing={false}
        onSyncGenres={vi.fn()}
        sourceRoots={[]}
        sourceRootsLoading={false}
        sourceRootsError="Корень уже настроен"
        sourceRootCreating={false}
        sourceRootRemoving={false}
        storageBrowser={{
          path: "/data/sources/archive",
          parent_path: "/data/sources",
          items: [],
        }}
        storageConfig={null}
        storageOutputPreview={null}
        storageLoading={false}
        onCreateSourceRoot={onCreateSourceRoot}
        onRemoveSourceRoot={vi.fn()}
        onBrowseStorage={vi.fn()}
        onPreviewStorageOutput={vi.fn()}
        onMoveStorageOutput={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText("Название корня"), {
      target: { value: "Архив" },
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Выбрать папку" })[1]);
    fireEvent.click(screen.getByRole("button", { name: "Выбрать эту папку" }));
    fireEvent.click(screen.getByTestId("source-root-create"));

    expect(onCreateSourceRoot).toHaveBeenCalledWith({
      display_name: "Архив",
      path: "/data/sources/archive",
    });
    expect(screen.getByTestId("source-root-error").getAttribute("role")).toBe("alert");
    expect(screen.getByTestId("source-root-error").textContent).toContain("Корень уже настроен");
  });
});

describe("SettingsScreen MusicBrainz", () => {
  it("updates the host and request delay", () => {
    const onChange = vi.fn();

    render(
      <SettingsScreen
        draft={draft}
        loading={false}
        saving={false}
        onChange={onChange}
        onSave={vi.fn()}
        genres={null}
        genreSearch=""
        onGenreSearch={vi.fn()}
        genreLoading={false}
        genreSyncing={false}
        onSyncGenres={vi.fn()}
        sourceRoots={[]}
        sourceRootsLoading={false}
        sourceRootsError=""
        sourceRootCreating={false}
        sourceRootRemoving={false}
        storageBrowser={null}
        storageConfig={null}
        storageOutputPreview={null}
        storageLoading={false}
        onCreateSourceRoot={vi.fn()}
        onRemoveSourceRoot={vi.fn()}
        onBrowseStorage={vi.fn()}
        onPreviewStorageOutput={vi.fn()}
        onMoveStorageOutput={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText("Хост MusicBrainz"), {
      target: { value: "https://musicbrainz.internal" },
    });
    fireEvent.change(screen.getByLabelText("Задержка запросов MusicBrainz, секунд"), {
      target: { value: "0" },
    });

    expect(onChange).toHaveBeenNthCalledWith(1, {
      ...draft,
      musicbrainz_host: "https://musicbrainz.internal",
    });
    expect(onChange).toHaveBeenNthCalledWith(2, {
      ...draft,
      musicbrainz_request_delay_seconds: 0,
    });
  });
});

describe("SettingsScreen processing", () => {
  it("updates worker concurrency", () => {
    const onChange = vi.fn();

    render(
      <SettingsScreen
        draft={draft}
        loading={false}
        saving={false}
        onChange={onChange}
        onSave={vi.fn()}
        genres={null}
        genreSearch=""
        onGenreSearch={vi.fn()}
        genreLoading={false}
        genreSyncing={false}
        onSyncGenres={vi.fn()}
        sourceRoots={[]}
        sourceRootsLoading={false}
        sourceRootsError=""
        sourceRootCreating={false}
        sourceRootRemoving={false}
        storageBrowser={null}
        storageConfig={null}
        storageOutputPreview={null}
        storageLoading={false}
        onCreateSourceRoot={vi.fn()}
        onRemoveSourceRoot={vi.fn()}
        onBrowseStorage={vi.fn()}
        onPreviewStorageOutput={vi.fn()}
        onMoveStorageOutput={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText("Воркеры: final_publish"), { target: { value: "7" } });

    expect(onChange).toHaveBeenCalledWith({
      ...draft,
      worker_pools: { ...draft.worker_pools, final_publish: 7 },
    });
  });
});

describe("SettingsScreen LRCLIB", () => {
  it("loads every LRCLIB field from the persisted draft", () => {
    renderSettings();

    const group = within(screen.getByRole("group", { name: "Тексты LRCLIB" }));
    const value = (label: string) => (group.getByLabelText(label) as HTMLInputElement).value;

    expect((group.getByLabelText("LRCLIB включён") as HTMLInputElement).checked).toBe(true);
    expect(value("User-Agent LRCLIB")).toBe("Music Ingest");
    expect(value("Хост LRCLIB")).toBe("https://lrclib.net");
    expect(value("Таймаут LRCLIB, секунд")).toBe("15");
    expect(value("Максимум попыток LRCLIB")).toBe("3");
    expect(value("Задержка запросов LRCLIB, секунд")).toBe("0.3");
    expect(value("Максимальный размер ответа LRCLIB, байт")).toBe("4194304");
    expect(
      screen.getByRole("button", { name: "Сохранить настройки" }).hasAttribute("disabled"),
    ).toBe(false);
  });

  it("sends every edit as a complete draft", () => {
    const onChange = vi.fn();
    renderSettings({ onChange });

    fireEvent.change(screen.getByLabelText("Хост LRCLIB"), {
      target: { value: "https://lrclib.internal" },
    });
    fireEvent.change(screen.getByLabelText("Задержка запросов LRCLIB, секунд"), {
      target: { value: "0.5" },
    });
    fireEvent.change(screen.getByLabelText("Максимальный размер ответа LRCLIB, байт"), {
      target: { value: "8388608" },
    });
    fireEvent.click(screen.getByLabelText("LRCLIB включён"));

    expect(onChange).toHaveBeenNthCalledWith(1, {
      ...draft,
      lrclib_host: "https://lrclib.internal",
    });
    expect(onChange).toHaveBeenNthCalledWith(2, {
      ...draft,
      lrclib_request_delay_seconds: 0.5,
    });
    expect(onChange).toHaveBeenNthCalledWith(3, {
      ...draft,
      lrclib_max_response_bytes: 8_388_608,
    });
    expect(onChange).toHaveBeenNthCalledWith(4, { ...draft, lrclib_enabled: false });
  });

  it("announces a rejected host and blocks saving until the draft is valid", () => {
    renderSettings({
      draft: { ...draft, lrclib_host: "http://lrclib.net", lrclib_timeout_seconds: 0 },
    });

    const error = screen.getByTestId("lrclib-settings-error");
    expect(error.getAttribute("role")).toBe("alert");
    expect(error.textContent).toContain("https://lrclib.net");
    expect(error.textContent).toContain("Таймаут LRCLIB");
    expect(screen.getByLabelText("Хост LRCLIB").getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByLabelText("Хост LRCLIB").getAttribute("aria-describedby")).toBe(
      "lrclib-settings-error",
    );
    expect(screen.getByLabelText("Таймаут LRCLIB, секунд").getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect(screen.getByLabelText("User-Agent LRCLIB").hasAttribute("aria-invalid")).toBe(false);
    expect(
      screen.getByRole("button", { name: "Сохранить настройки" }).hasAttribute("disabled"),
    ).toBe(true);
  });
});
