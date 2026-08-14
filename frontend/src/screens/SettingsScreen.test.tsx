// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeSettingsDraft } from "../types";
import { SettingsScreen } from "./SettingsScreen";

afterEach(cleanup);

const draft: RuntimeSettingsDraft = {
  confidence_threshold: 0.8,
  timeout_seconds: 30,
  retry_delay_seconds: 10,
  max_attempts: 3,
  worker_concurrency: 1,
  musicbrainz_enabled: true,
  musicbrainz_user_agent: "Music Ingest",
  musicbrainz_host: "https://musicbrainz.org",
  musicbrainz_request_delay_seconds: 1.5,
  acoustid_enabled: false,
  acoustid_request_delay_seconds: 1 / 3,
  acoustid_client_key: "",
  artwork_enabled: true,
};

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

    fireEvent.change(screen.getByLabelText("Параллельные воркеры"), { target: { value: "4" } });

    expect(onChange).toHaveBeenCalledWith({ ...draft, worker_concurrency: 4 });
  });
});
