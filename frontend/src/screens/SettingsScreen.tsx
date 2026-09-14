import { useState } from "react";
import { runtimeSettingsFieldErrors } from "../domain/settings";
import type {
  GenreCatalog,
  RuntimeSettingsDraft,
  SourceRoot,
  SourceRootCreate,
  StorageBrowser,
  StorageConfig,
  StorageOutputPreview,
} from "../types";

export function SettingsScreen({
  draft,
  loading,
  saving,
  onChange,
  onSave,
  genres,
  genreSearch,
  onGenreSearch,
  genreLoading,
  genreSyncing,
  onSyncGenres,
  sourceRoots,
  sourceRootsLoading,
  sourceRootsError,
  sourceRootCreating,
  sourceRootRemoving,
  storageBrowser,
  storageConfig,
  storageOutputPreview,
  storageLoading,
  onCreateSourceRoot,
  onRemoveSourceRoot,
  onBrowseStorage,
  onPreviewStorageOutput,
  onMoveStorageOutput,
}: {
  readonly draft: RuntimeSettingsDraft | null;
  readonly loading: boolean;
  readonly saving: boolean;
  readonly onChange: (draft: RuntimeSettingsDraft) => void;
  readonly onSave: () => void;
  readonly genres: GenreCatalog | null;
  readonly genreSearch: string;
  readonly onGenreSearch: (value: string) => void;
  readonly genreLoading: boolean;
  readonly genreSyncing: boolean;
  readonly onSyncGenres: () => void;
  readonly sourceRoots: readonly SourceRoot[];
  readonly sourceRootsLoading: boolean;
  readonly sourceRootsError: string;
  readonly sourceRootCreating: boolean;
  readonly sourceRootRemoving: boolean;
  readonly storageBrowser: StorageBrowser | null;
  readonly storageConfig: StorageConfig | null;
  readonly storageOutputPreview: StorageOutputPreview | null;
  readonly storageLoading: boolean;
  readonly onCreateSourceRoot: (request: SourceRootCreate) => void;
  readonly onRemoveSourceRoot: (rootId: string) => void;
  readonly onBrowseStorage: (path?: string) => void;
  readonly onPreviewStorageOutput: (path: string) => void;
  readonly onMoveStorageOutput: (path: string) => void;
}) {
  const [newRoot, setNewRoot] = useState<SourceRootCreate>({ path: "", display_name: "" });
  const [pickerTarget, setPickerTarget] = useState<"input" | "output" | null>(null);
  if (loading || !draft) return <div className="empty-state">Загрузка настроек…</div>;
  const update = <K extends keyof RuntimeSettingsDraft>(key: K, value: RuntimeSettingsDraft[K]) =>
    onChange({ ...draft, [key]: value });
  const fieldErrors = runtimeSettingsFieldErrors(draft);
  const fieldErrorList = Object.values(fieldErrors);
  const invalidProps = (invalid: boolean) =>
    invalid ? { "aria-describedby": "lrclib-settings-error", "aria-invalid": true } : {};
  const visibleGenres = (genres?.items ?? []).filter((genre) =>
    `${genre.display_name} ${genre.source_name}`.toLowerCase().includes(genreSearch.toLowerCase()),
  );
  const submitSourceRoot = () => {
    const request = { path: newRoot.path.trim(), display_name: newRoot.display_name.trim() };
    if (!request.path || !request.display_name) return;
    onCreateSourceRoot(request);
    setNewRoot({ path: "", display_name: "" });
  };
  const selectStorageFolder = (path: string) => {
    if (pickerTarget === "input") setNewRoot({ ...newRoot, path });
    if (pickerTarget === "output") onPreviewStorageOutput(path);
    setPickerTarget(null);
  };
  return (
    <section className="settings-screen">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Runtime configuration</p>
          <h2>Настройки сервиса</h2>
        </div>
        <button
          type="button"
          className="primary"
          disabled={saving || fieldErrorList.length > 0}
          onClick={onSave}
        >
          {saving ? "Сохраняем…" : "Сохранить настройки"}
        </button>
      </div>
      <div className="settings-grid">
        <fieldset className="settings-card">
          <legend>Обработка и matching</legend>
          <label>
            Порог совпадения
            <input
              type="number"
              min="0"
              max="1"
              step="0.01"
              value={draft.confidence_threshold}
              onChange={(event) => update("confidence_threshold", Number(event.target.value))}
            />
          </label>
          <label>
            Таймаут, секунд
            <input
              type="number"
              min="1"
              max="120"
              value={draft.timeout_seconds}
              onChange={(event) => update("timeout_seconds", Number(event.target.value))}
            />
          </label>
          <label>
            Задержка retry, секунд
            <input
              type="number"
              min="0"
              max="3600"
              value={draft.retry_delay_seconds}
              onChange={(event) => update("retry_delay_seconds", Number(event.target.value))}
            />
          </label>
          <label>
            Максимум попыток
            <input
              type="number"
              min="1"
              max="10"
              value={draft.max_attempts}
              onChange={(event) => update("max_attempts", Number(event.target.value))}
            />
          </label>
          {Object.entries(draft.worker_pools).map(([pool, count]) => (
            <label key={pool}>
              Воркеры: {pool}
              <input
                type="number"
                min="1"
                max="8"
                step="1"
                value={count}
                onChange={(event) =>
                  update("worker_pools", {
                    ...draft.worker_pools,
                    [pool]: Number(event.target.value),
                  })
                }
              />
            </label>
          ))}
          <small className="settings-help">
            Независимые пулы по типу задачи, без заимствования воркеров. Изменения применяются перед
            следующей задачей; уже начатые задачи завершаются. LRCLIB выполняет запросы
            последовательно с задержкой 0,3 секунды по умолчанию.
          </small>
        </fieldset>
        <fieldset className="settings-card">
          <legend>Внешние провайдеры</legend>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.musicbrainz_enabled}
              onChange={(event) => update("musicbrainz_enabled", event.target.checked)}
            />{" "}
            MusicBrainz включён
          </label>
          <label>
            User-Agent MusicBrainz
            <input
              value={draft.musicbrainz_user_agent}
              onChange={(event) => update("musicbrainz_user_agent", event.target.value)}
            />
          </label>
          <label>
            Хост MusicBrainz
            <input
              type="url"
              pattern="https?://[^/?#]+"
              value={draft.musicbrainz_host}
              onChange={(event) => update("musicbrainz_host", event.target.value)}
            />
          </label>
          <label>
            Задержка запросов MusicBrainz, секунд
            <input
              type="number"
              min="0"
              max="3600"
              step="0.1"
              value={draft.musicbrainz_request_delay_seconds}
              onChange={(event) =>
                update("musicbrainz_request_delay_seconds", Number(event.target.value))
              }
            />
          </label>
          <small className="settings-help">
            По умолчанию используется официальный MusicBrainz с безопасной задержкой 1,5 секунды.
          </small>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.acoustid_enabled}
              onChange={(event) => update("acoustid_enabled", event.target.checked)}
            />{" "}
            AcousticID включён
          </label>
          <label>
            Client key AcousticID
            <input
              type="password"
              placeholder="Пусто — сохранить текущий"
              value={draft.acoustid_client_key}
              onChange={(event) => update("acoustid_client_key", event.target.value)}
            />
          </label>
          <label>
            Задержка запросов AcousticID, секунд
            <input
              type="number"
              min="0.01"
              max="3600"
              step="0.01"
              value={draft.acoustid_request_delay_seconds}
              onChange={(event) =>
                update("acoustid_request_delay_seconds", Number(event.target.value))
              }
            />
          </label>
          <small className="settings-help">
            Официальный AcousticID допускает не более трёх запросов в секунду; по умолчанию 0,333
            секунды.
          </small>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.artwork_enabled}
              onChange={(event) => update("artwork_enabled", event.target.checked)}
            />{" "}
            Artwork enrichment включён
          </label>
        </fieldset>
        <fieldset className="settings-card">
          <legend>Тексты LRCLIB</legend>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.lrclib_enabled}
              onChange={(event) => update("lrclib_enabled", event.target.checked)}
            />{" "}
            LRCLIB включён
          </label>
          <label>
            User-Agent LRCLIB
            <input
              {...invalidProps(Boolean(fieldErrors.lrclib_user_agent))}
              value={draft.lrclib_user_agent}
              onChange={(event) => update("lrclib_user_agent", event.target.value)}
            />
          </label>
          <label>
            Хост LRCLIB
            <input
              type="url"
              pattern="https://[^/?#]+"
              {...invalidProps(Boolean(fieldErrors.lrclib_host))}
              value={draft.lrclib_host}
              onChange={(event) => update("lrclib_host", event.target.value)}
            />
          </label>
          <label>
            Таймаут LRCLIB, секунд
            <input
              type="number"
              min="1"
              max="120"
              step="0.1"
              {...invalidProps(Boolean(fieldErrors.lrclib_timeout_seconds))}
              value={draft.lrclib_timeout_seconds}
              onChange={(event) => update("lrclib_timeout_seconds", Number(event.target.value))}
            />
          </label>
          <label>
            Максимум попыток LRCLIB
            <input
              type="number"
              min="1"
              max="10"
              {...invalidProps(Boolean(fieldErrors.lrclib_max_attempts))}
              value={draft.lrclib_max_attempts}
              onChange={(event) => update("lrclib_max_attempts", Number(event.target.value))}
            />
          </label>
          <label>
            Задержка запросов LRCLIB, секунд
            <input
              type="number"
              min="0"
              max="3600"
              step="0.1"
              {...invalidProps(Boolean(fieldErrors.lrclib_request_delay_seconds))}
              value={draft.lrclib_request_delay_seconds}
              onChange={(event) =>
                update("lrclib_request_delay_seconds", Number(event.target.value))
              }
            />
          </label>
          <label>
            Максимальный размер ответа LRCLIB, байт
            <input
              type="number"
              min="1024"
              max="16777216"
              step="1024"
              {...invalidProps(Boolean(fieldErrors.lrclib_max_response_bytes))}
              value={draft.lrclib_max_response_bytes}
              onChange={(event) => update("lrclib_max_response_bytes", Number(event.target.value))}
            />
          </label>
          <label>
            Порог совпадения LRCLIB
            <input
              type="number"
              min="0"
              max="1"
              step="0.01"
              {...invalidProps(Boolean(fieldErrors.lrclib_match_confidence_threshold))}
              value={draft.lrclib_match_confidence_threshold}
              onChange={(event) =>
                update("lrclib_match_confidence_threshold", Number(event.target.value))
              }
            />
          </label>
          {fieldErrorList.length > 0 && (
            <p
              className="settings-help"
              data-testid="lrclib-settings-error"
              id="lrclib-settings-error"
              role="alert"
            >
              {fieldErrorList.join(" ")}
            </p>
          )}
          <small className="settings-help">
            Поиск ранжирует синхронные варианты по title, artist и album после обязательной проверки
            длительности ±2 секунды. Порог по умолчанию — 0,70; задержка — 0,3 секунды, лимит ответа
            — 4 МиБ. Пока значения не исправлены, сохранение настроек недоступно.
          </small>
        </fieldset>
        <fieldset className="settings-card settings-card-wide">
          <legend>Жанры MusicBrainz</legend>
          <div className="genre-catalog-toolbar">
            <input
              aria-label="Поиск жанра"
              placeholder="Найти жанр по названию"
              value={genreSearch}
              onChange={(event) => onGenreSearch(event.target.value)}
            />
            <button
              type="button"
              className="secondary"
              disabled={genreSyncing}
              onClick={onSyncGenres}
            >
              {genreSyncing ? "Обновляем…" : "Обновить каталог"}
            </button>
          </div>
          {genreLoading ? (
            <p className="settings-help">Загрузка каталога…</p>
          ) : visibleGenres.length ? (
            <ul className="genre-catalog" aria-label="Каталог жанров">
              {visibleGenres.slice(0, 80).map((genre) => (
                <li className="genre-chip" key={genre.musicbrainz_id}>
                  <strong>{genre.display_name}</strong>
                  <small>{genre.source_name}</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="settings-help">Каталог пуст. Нажмите «Обновить каталог».</p>
          )}
          <small>
            Названия берутся из MusicBrainz; исходные значения сохраняются для точного маппинга.
          </small>
        </fieldset>
        <fieldset className="settings-card settings-card-wide">
          <legend>Хранилище контейнера</legend>
          <div className="storage-root-row">
            <div>
              <strong>Output</strong>
              <small>{storageConfig?.output_root ?? "Загрузка…"}</small>
            </div>
            <button
              type="button"
              className="secondary"
              disabled={storageConfig?.state === "migrating"}
              onClick={() => setPickerTarget("output")}
            >
              Выбрать папку
            </button>
          </div>
          {storageConfig?.state === "migrating" && (
            <p role="status">
              Перенос выполняется в фоне. Публикация приостановлена до завершения. При сбое перенос
              продолжится автоматически; подробности — на экране воркеров.
            </p>
          )}
          {storageOutputPreview && (
            <div className="storage-migration-notice" role="status">
              <strong>
                Перенести {storageOutputPreview.file_count} файлов в{" "}
                {storageOutputPreview.output_root}?
              </strong>
              <p>
                Файлы будут скопированы и проверены перед переключением папки. Потребуется место для
                полной копии. Файлы .nfo сохраняются и в прежней папке.
              </p>
              <button
                type="button"
                className="primary"
                disabled={storageLoading}
                onClick={() => onMoveStorageOutput(storageOutputPreview.output_root)}
              >
                {storageLoading ? "Переносим…" : "Перенести output"}
              </button>
            </div>
          )}
        </fieldset>
        <fieldset className="settings-card settings-card-wide">
          <legend>Корни исходников</legend>
          {sourceRootsError && (
            <p className="settings-help" data-testid="source-root-error" role="alert">
              {sourceRootsError}
            </p>
          )}
          {sourceRootsLoading ? (
            <p className="settings-help">Загрузка корней…</p>
          ) : (
            <div data-testid="source-root-list">
              {sourceRoots.length ? (
                <ul className="source-root-list" aria-label="Настроенные корни исходников">
                  {sourceRoots.map((root) => (
                    <li className="genre-chip" key={root.id}>
                      <strong>{root.display_name}</strong>
                      <small>{root.canonical_path}</small>
                      <small>
                        {root.enabled ? "Включён" : "Отключён"} · Сканирование: {root.scan_state}
                      </small>
                      <button
                        type="button"
                        className="secondary"
                        data-testid={`source-root-remove-${root.id}`}
                        disabled={sourceRootRemoving}
                        onClick={() => onRemoveSourceRoot(root.id)}
                      >
                        {sourceRootRemoving ? "Удаляем…" : "Удалить"}
                      </button>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="settings-help">Корни исходников ещё не настроены.</p>
              )}
            </div>
          )}
          <div className="source-root-form">
            <label>
              Название корня
              <input
                value={newRoot.display_name}
                onChange={(event) => setNewRoot({ ...newRoot, display_name: event.target.value })}
              />
            </label>
            <div className="folder-choice">
              <span>Папка исходников</span>
              <strong>{newRoot.path || "Не выбрана"}</strong>
              <button type="button" className="secondary" onClick={() => setPickerTarget("input")}>
                Выбрать папку
              </button>
            </div>
          </div>
          <small className="settings-help">
            Выбирайте только папки, доступные внутри контейнера. Output и его подпапки недоступны
            для input.
          </small>
          <button
            type="button"
            className="primary"
            data-testid="source-root-create"
            disabled={sourceRootCreating || !newRoot.path.trim() || !newRoot.display_name.trim()}
            onClick={submitSourceRoot}
          >
            {sourceRootCreating ? "Добавляем…" : "Добавить корень"}
          </button>
        </fieldset>
        {pickerTarget && storageBrowser && (
          <div className="folder-picker-backdrop" role="presentation">
            <section
              aria-label="Выбор папки контейнера"
              className="folder-picker"
              role="dialog"
              aria-modal="true"
            >
              <header>
                <div>
                  <p className="eyebrow">Container filesystem</p>
                  <h3>{pickerTarget === "input" ? "Выберите input" : "Выберите output"}</h3>
                </div>
                <button type="button" className="secondary" onClick={() => setPickerTarget(null)}>
                  Отмена
                </button>
              </header>
              <div className="folder-picker-path">{storageBrowser.path}</div>
              <div className="folder-picker-actions">
                <button
                  type="button"
                  className="secondary"
                  disabled={!storageBrowser.parent_path || storageLoading}
                  onClick={() => onBrowseStorage(storageBrowser.parent_path ?? undefined)}
                >
                  Назад
                </button>
                <button
                  type="button"
                  className="primary"
                  disabled={storageLoading}
                  onClick={() => selectStorageFolder(storageBrowser.path)}
                >
                  Выбрать эту папку
                </button>
              </div>
              <ul className="folder-picker-list" aria-label="Папки текущего каталога">
                {storageBrowser.items.map((item) => (
                  <li key={item.path}>
                    <button type="button" onClick={() => onBrowseStorage(item.path)}>
                      <span aria-hidden="true">▸</span>
                      <strong>{item.name}</strong>
                      <small>{item.path}</small>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          </div>
        )}
      </div>
    </section>
  );
}
