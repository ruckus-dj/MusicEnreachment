import { Icon as icon } from "../components/Icon";
import { LibraryCatalog } from "../components/LibraryCatalog";
import { albumFor, titleFor } from "../domain/metadata";
import { ManualActionsScreen } from "../screens/ManualActionsScreen";
import { SettingsScreen } from "../screens/SettingsScreen";
import { TrackDetail } from "../screens/TrackDetail";
import { WorkerQueueScreen } from "../screens/WorkerQueueScreen";
import type { Summary } from "../types";
import type { AppControllerModel } from "./useAppController";

const emptySummary: Summary = {
  record_id: "",
  source_state: "",
  processing_state: "",
  match_state: "",
  publication_state: "",
  metadata_state: "",
  sources: [],
  publications: [],
};

export function AppShell({ controller }: { controller: AppControllerModel }) {
  const {
    screen,
    items,
    tracks,
    loading,
    artist,
    album,
    detail,
    sourceId,
    currentTrack,
    artists,
    albums,
    albumTracks,
    notice,
    watchedRecords,
    watchedLibraryUntil,
  } = controller;
  const manualActionCount = tracks.filter(
    ({ item, source }) =>
      item.processing_state === "retrying" ||
      item.processing_state === "blocked_infrastructure" ||
      item.processing_state === "quarantined" ||
      item.publication_state === "failed" ||
      source.state === "invalid_audio" ||
      item.processing_state === "needs_review" ||
      item.match_state === "needs_review",
  ).length;
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">
            {icon(
              "M9 18V6l10-2v12M9 18a3 3 0 1 1-3-3 3 3 0 1 1 3 3Zm10-2a3 3 0 1 1-3-3 3 3 0 1 1 3 3Z",
            )}
          </div>
          <div>
            <strong>Music Ingest</strong>
            <span>LIBRARY / REVIEW</span>
          </div>
        </div>
        <nav>
          <button
            type="button"
            className={
              screen !== "track" &&
              screen !== "settings" &&
              screen !== "manual-actions" &&
              screen !== "workers"
                ? "nav-item active"
                : "nav-item"
            }
            data-testid="nav-library"
            onClick={() => controller.navigate({ screen: "artists" })}
          >
            {icon(
              "M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v13A1.5 1.5 0 0 1 18.5 20h-13A1.5 1.5 0 0 1 4 18.5v-13ZM7 8h10M7 12h10M7 16h6",
            )}
            <span>Медиатека</span>
            <b>{tracks.length}</b>
          </button>
          <button
            type="button"
            className={screen === "manual-actions" ? "nav-item active" : "nav-item"}
            data-testid="nav-manual-actions"
            onClick={() => controller.navigate({ screen: "manual-actions" })}
          >
            {icon(
              "M12 8v4m0 4h.01M10.29 3.86 2.82 16.5A2 2 0 0 0 4.54 19.5h14.92a2 2 0 0 0 1.72-3L13.71 3.86a2 2 0 0 0-3.42 0Z",
            )}
            <span>Ручные действия</span>
          </button>
          <button
            type="button"
            className={screen === "settings" ? "nav-item active" : "nav-item"}
            data-testid="nav-settings"
            onClick={() => controller.navigate({ screen: "settings" })}
          >
            {icon(
              "M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Zm0-5v2M12 18.5v2M20.5 12h-2M5.5 12h-2M18 6l-1.4 1.4M7.4 16.6 6 18M18 18l-1.4-1.4M7.4 7.4 6 6",
            )}
            <span>Настройки</span>
          </button>
          <button
            type="button"
            className={screen === "workers" ? "nav-item active" : "nav-item"}
            data-testid="nav-workers"
            onClick={() => controller.navigate({ screen: "workers" })}
          >
            {icon("M5 5h14M5 12h14M5 19h14M8 3v4m8-4v4M8 10v4m8-4v4m-8 3v4m8-4v4")}
            <span>Очередь worker’ов</span>
          </button>
        </nav>
        <div className="sidebar-bottom">
          <div className="storage">
            <span>Состояние</span>
            <strong>
              {items.some((item) => item.processing_state !== "complete")
                ? "Есть анализ"
                : "Готово"}
            </strong>
            <small>Исходники не изменяются</small>
          </div>
          <button
            type="button"
            className="nav-item quiet"
            disabled={loading}
            onClick={() => void controller.loadLibrary()}
          >
            {icon("M4 12a8 8 0 1 0 2.34-5.66L4 8.68M4 4v4.68h4.68")}
            <span>{loading ? "Обновляем…" : "Обновить список"}</span>
          </button>
        </div>
      </aside>
      <main className="workspace">
        <header className="topbar">
          <div className="breadcrumbs">
            <span>Music Ingest</span>
            <i>/</i>
            <strong>
              {screen === "settings"
                ? "Настройки"
                : screen === "manual-actions"
                  ? "Ручные действия"
                  : screen === "workers"
                    ? "Очередь worker’ов"
                    : screen === "artists"
                      ? "Медиатека"
                      : screen === "albums"
                        ? artist
                        : screen === "tracks"
                          ? albumFor(
                              albumTracks[0]?.item ?? emptySummary,
                              albumTracks[0]?.source.source_id ?? "",
                            )
                          : titleFor(detail ?? currentTrack?.item ?? emptySummary, sourceId)}
            </strong>
          </div>
          <div className="topbar-actions">
            <span className="sync">
              {Object.keys(watchedRecords).length || watchedLibraryUntil > 0
                ? "Синхронизация…"
                : "Синхронизировано"}
            </span>
            <button type="button" className="avatar">
              MI
            </button>
          </div>
        </header>
        <div className="content">
          <section className="hero">
            <div>
              {screen !== "artists" &&
                screen !== "settings" &&
                screen !== "manual-actions" &&
                screen !== "workers" && (
                  <button type="button" className="back" onClick={controller.back}>
                    ← Назад
                  </button>
                )}
              <p className="eyebrow">
                {screen === "settings"
                  ? "Панель управления"
                  : screen === "manual-actions"
                    ? "Очередь оператора"
                    : screen === "workers"
                      ? "Состояние обработки"
                      : screen === "track"
                        ? "Инспектор трека"
                        : "Ваша медиатека"}
              </p>
              <h1>
                {screen === "settings"
                  ? "Настройки"
                  : screen === "manual-actions"
                    ? "Ручные действия"
                    : screen === "workers"
                      ? "Очередь worker’ов"
                      : screen === "artists"
                        ? "Исполнители"
                        : screen === "albums"
                          ? artist
                          : screen === "tracks"
                            ? albumFor(
                                albumTracks[0]?.item ?? emptySummary,
                                albumTracks[0]?.source.source_id ?? "",
                              )
                            : titleFor(detail ?? currentTrack?.item ?? emptySummary, sourceId)}
              </h1>
              <p className="hero-copy">
                {screen === "settings"
                  ? "Настройки runtime и провайдеров"
                  : screen === "manual-actions"
                    ? "Ошибки анализа и треки, для которых нельзя безопасно выбрать результат автоматически."
                    : screen === "workers"
                      ? "Сохранённые задачи анализа, публикации и восстановления. Состояние процесса отдельно не измеряется."
                      : screen === "artists"
                        ? "Отдельный каталог артистов. Откройте исполнителя, чтобы увидеть его альбомы."
                        : screen === "albums"
                          ? "Альбомы исполнителя и их состояние обработки."
                          : screen === "tracks"
                            ? "Треки альбома. Выберите файл, чтобы открыть проверку и Final."
                            : "Исходные данные, провайдеры, ручная проверка и Final одной записи."}
              </p>
            </div>
            <div className="hero-stat">
              <strong>
                {screen === "settings"
                  ? "DB"
                  : screen === "manual-actions"
                    ? manualActionCount
                    : screen === "workers"
                      ? (controller.workerQueue?.jobs.length ?? "—")
                      : screen === "artists"
                        ? artists.length
                        : screen === "albums"
                          ? albums.length
                          : screen === "tracks"
                            ? albumTracks.length
                            : "01"}
              </strong>
              <span>
                {screen === "settings"
                  ? "runtime параметров"
                  : screen === "manual-actions"
                    ? "треков"
                    : screen === "workers"
                      ? "активных задач"
                      : screen === "artists"
                        ? "артистов"
                        : screen === "albums"
                          ? "альбомов"
                          : screen === "tracks"
                            ? "треков"
                            : "трек"}
              </span>
            </div>
          </section>
          {notice && (
            <div className="notice-bar" role="status">
              <span>{notice}</span>
              <button type="button" onClick={() => controller.setNotice("")}>
                Скрыть
              </button>
            </div>
          )}
          {screen !== "track" &&
            screen !== "settings" &&
            screen !== "manual-actions" &&
            screen !== "workers" && (
              <div className="library-toolbar">
                <label className="search">
                  {icon("m20 20-4.5-4.5M10.75 17a6.25 6.25 0 1 0 0-12.5 6.25 6.25 0 0 0 0 12.5Z")}
                  <input
                    aria-label="Поиск"
                    value={controller.query}
                    onChange={(event) => controller.setQuery(event.target.value)}
                    placeholder="Поиск по артисту, альбому или треку"
                  />
                </label>
                <div className="provider-actions">
                  <button
                    type="button"
                    className="secondary"
                    disabled={controller.reprocessing}
                    onClick={() => void controller.reprocessAll()}
                  >
                    {controller.reprocessing ? "Ставим в очередь…" : "Переобработать всю медиатеку"}
                  </button>
                  <button
                    type="button"
                    className="primary scan"
                    disabled={controller.scanning || controller.reprocessing}
                    onClick={() => void controller.scan()}
                  >
                    {controller.scanning ? "Сканируем…" : "Сканировать новые и изменённые"}
                  </button>
                </div>
              </div>
            )}
          {screen === "settings" ? (
            <SettingsScreen
              draft={controller.settingsDraft}
              loading={controller.settingsLoading}
              saving={controller.settingsSaving}
              onChange={controller.setSettingsDraft}
              onSave={() => void controller.saveSettings()}
              genres={controller.genreCatalog}
              genreSearch={controller.genreSearch}
              onGenreSearch={controller.setGenreSearch}
              genreLoading={controller.genreLoading}
              genreSyncing={controller.genreSyncing}
              onSyncGenres={() => void controller.syncGenres()}
              sourceRoots={controller.sourceRoots}
              sourceRootsLoading={controller.sourceRootsLoading}
              sourceRootsError={controller.sourceRootsError}
              sourceRootCreating={controller.sourceRootCreating}
              sourceRootRemoving={controller.sourceRootRemoving}
              storageBrowser={controller.storageBrowser}
              storageConfig={controller.storageConfig}
              storageOutputPreview={controller.storageOutputPreview}
              storageLoading={controller.storageLoading}
              onCreateSourceRoot={(request) => void controller.createSourceRoot(request)}
              onRemoveSourceRoot={(rootId) => void controller.removeSourceRoot(rootId)}
              onBrowseStorage={(path) => void controller.browseStorage(path)}
              onPreviewStorageOutput={(path) => void controller.previewStorageOutput(path)}
              onMoveStorageOutput={(path) => void controller.moveStorageOutput(path)}
            />
          ) : screen === "manual-actions" ? (
            <ManualActionsScreen
              tracks={tracks}
              reprocessing={controller.reprocessing}
              onNavigate={controller.navigate}
              onRetry={(recordId, sourceId) => void controller.reprocessSource(recordId, sourceId)}
            />
          ) : screen === "workers" ? (
            <WorkerQueueScreen
              queue={controller.workerQueue}
              loading={controller.workerQueueLoading}
              error={controller.workerQueueError}
              onRefresh={() => void controller.loadWorkerQueue()}
            />
          ) : screen === "track" ? (
            <TrackDetail
              detail={detail}
              sourceId={sourceId}
              draft={controller.draft}
              setDraft={controller.setDraft}
              saving={controller.saving}
              reprocessing={controller.reprocessing}
              musicbrainzHost={controller.settingsDraft?.musicbrainz_host ?? null}
              onSave={controller.saveMetadata}
              onRetryAcoustId={() => void controller.retryProvider("acoustid")}
              onRetryMusicBrainz={() => void controller.retryProvider("musicbrainz")}
              onOverrideRelease={(value) => void controller.overrideRelease(value)}
              onOverrideRecording={(request) => void controller.overrideRecording(request)}
              onSelectCandidate={(key) => void controller.selectCandidate(key)}
              onSelectEffectiveSource={(value) => void controller.selectEffectiveSource(value)}
              effectiveSourceId={controller.effectiveSourceId}
              effectiveSourceError={controller.effectiveSourceError}
              effectiveSourceSuccess={controller.effectiveSourceSuccess}
              recordingCorrectionError={controller.recordingCorrectionError}
              recordingCorrectionReview={controller.recordingCorrectionReview}
            />
          ) : (
            <LibraryCatalog
              screen={screen}
              artist={artist}
              album={album}
              artists={artists}
              albums={albums}
              tracks={tracks}
              albumTracks={albumTracks}
              loading={loading}
              onNavigate={controller.navigate}
              onRefresh={() => void controller.loadLibrary()}
            />
          )}
        </div>
      </main>
    </div>
  );
}
