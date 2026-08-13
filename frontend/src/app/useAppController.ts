import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  api,
  browseStorage,
  createSourceRoot,
  getStorageConfig,
  listSourceRootCandidates,
  listSourceRoots,
  moveStorageOutput,
  previewStorageOutput,
  removeSourceRoot,
  selectEffectiveSource,
  submitRecordingCorrection,
} from "../api/client";
import {
  albumFor,
  artistFor,
  compareNames,
  detailIsPending,
  tagsFor,
  titleFor,
  trackNumberFor,
} from "../domain/metadata";
import { parseRoute, routePath } from "../routing";
import type {
  Detail,
  EffectiveSourceSelection,
  GenreCatalog,
  Layer,
  ProviderName,
  RecordingCorrection,
  RecordingCorrectionResult,
  Route,
  RuntimeSettings,
  RuntimeSettingsDraft,
  ScanJob,
  Screen,
  Source,
  SourceRoot,
  SourceRootCandidate,
  SourceRootCreate,
  StorageBrowser,
  StorageConfig,
  StorageOutputPreview,
  Summary,
  Tags,
  WatchedRecord,
} from "../types";

export type CatalogTrack = { item: Summary; source: Source };

export type AppControllerModel = {
  items: Summary[];
  detail: Detail | null;
  screen: Screen;
  artist: string;
  album: string;
  recordId: string;
  sourceId: string;
  layer: Layer;
  draft: Tags;
  query: string;
  notice: string;
  loading: boolean;
  scanning: boolean;
  saving: boolean;
  reprocessing: boolean;
  effectiveSourceId: string | null;
  effectiveSourceError: string;
  effectiveSourceSuccess: string;
  recordingCorrectionError: string;
  recordingCorrectionReview: string;
  settingsDraft: RuntimeSettingsDraft | null;
  settingsLoading: boolean;
  settingsSaving: boolean;
  genreCatalog: GenreCatalog | null;
  genreSearch: string;
  genreLoading: boolean;
  genreSyncing: boolean;
  sourceRoots: readonly SourceRoot[];
  sourceRootsLoading: boolean;
  sourceRootsError: string;
  sourceRootCreating: boolean;
  sourceRootRemoving: boolean;
  sourceRootCandidates: readonly SourceRootCandidate[];
  sourceRootCandidatesLoading: boolean;
  storageBrowser: StorageBrowser | null;
  storageConfig: StorageConfig | null;
  storageOutputPreview: StorageOutputPreview | null;
  storageLoading: boolean;
  watchedRecords: Record<string, WatchedRecord>;
  watchedLibraryUntil: number;
  tracks: CatalogTrack[];
  artists: string[];
  albums: string[];
  albumTracks: CatalogTrack[];
  currentTrack: CatalogTrack | undefined;
  currentTags: Tags;
  navigate: (route: Route) => void;
  back: () => void;
  loadLibrary: (showLoader?: boolean) => Promise<void>;
  scan: () => Promise<void>;
  reprocessAll: () => Promise<void>;
  reprocessSource: (recordId: string, sourceId: string) => Promise<void>;
  saveMetadata: () => Promise<boolean>;
  retryProvider: (provider: ProviderName) => Promise<void>;
  overrideRelease: (releaseMbid: string) => Promise<void>;
  overrideRecording: (request: RecordingCorrection) => Promise<void>;
  selectCandidate: (selection: string) => Promise<void>;
  selectEffectiveSource: (sourceId: string) => Promise<void>;
  saveSettings: () => Promise<void>;
  syncGenres: () => Promise<void>;
  createSourceRoot: (request: SourceRootCreate) => Promise<void>;
  removeSourceRoot: (rootId: string) => Promise<void>;
  browseStorage: (path?: string) => Promise<void>;
  previewStorageOutput: (path: string) => Promise<void>;
  moveStorageOutput: (path: string) => Promise<void>;
  setQuery: (value: string) => void;
  setNotice: (value: string) => void;
  setLayer: (value: Layer) => void;
  setDraft: (value: Tags) => void;
  setSettingsDraft: (value: RuntimeSettingsDraft | null) => void;
  setGenreSearch: (value: string) => void;
};

export function useAppController(): AppControllerModel {
  const [items, setItems] = useState<Summary[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [initialRoute] = useState(() => parseRoute(window.location.pathname));
  const [screen, setScreen] = useState<Screen>(initialRoute.screen);
  const [artist, setArtist] = useState(initialRoute.artist ?? "");
  const [album, setAlbum] = useState(initialRoute.album ?? "");
  const [recordId, setRecordId] = useState(initialRoute.recordId ?? "");
  const [sourceId, setSourceId] = useState(initialRoute.sourceId ?? "");
  const [layer, setLayer] = useState<Layer>("final");
  const [draft, setDraft] = useState<Tags>({});
  const [query, setQuery] = useState("");
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(true);
  const [scanning, setScanning] = useState(false);
  const [scanJobId, setScanJobId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [reprocessing, setReprocessing] = useState(false);
  const [effectiveSourceId, setEffectiveSourceId] = useState<string | null>(
    initialRoute.sourceId ?? null,
  );
  const [effectiveSourceError, setEffectiveSourceError] = useState("");
  const [effectiveSourceSuccess, setEffectiveSourceSuccess] = useState("");
  const [recordingCorrectionError, setRecordingCorrectionError] = useState("");
  const [recordingCorrectionReview, setRecordingCorrectionReview] = useState("");
  const [settingsDraft, setSettingsDraft] = useState<RuntimeSettingsDraft | null>(null);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [genreCatalog, setGenreCatalog] = useState<GenreCatalog | null>(null);
  const [genreSearch, setGenreSearch] = useState("");
  const [genreLoading, setGenreLoading] = useState(false);
  const [genreSyncing, setGenreSyncing] = useState(false);
  const [sourceRoots, setSourceRoots] = useState<readonly SourceRoot[]>([]);
  const [sourceRootsLoaded, setSourceRootsLoaded] = useState(false);
  const [sourceRootsLoading, setSourceRootsLoading] = useState(false);
  const [sourceRootsError, setSourceRootsError] = useState("");
  const [sourceRootCreating, setSourceRootCreating] = useState(false);
  const [sourceRootRemoving, setSourceRootRemoving] = useState(false);
  const [sourceRootCandidates, setSourceRootCandidates] = useState<readonly SourceRootCandidate[]>(
    [],
  );
  const [sourceRootCandidatesLoading, setSourceRootCandidatesLoading] = useState(false);
  const [storageBrowser, setStorageBrowser] = useState<StorageBrowser | null>(null);
  const [storageConfig, setStorageConfig] = useState<StorageConfig | null>(null);
  const [storageOutputPreview, setStorageOutputPreview] = useState<StorageOutputPreview | null>(
    null,
  );
  const [storageLoading, setStorageLoading] = useState(false);
  const [watchedRecords, setWatchedRecords] = useState<Record<string, WatchedRecord>>({});
  const [watchedLibraryUntil, setWatchedLibraryUntil] = useState(0);
  const watchedRecordsRef = useRef(watchedRecords);
  const watchedLibraryUntilRef = useRef(watchedLibraryUntil);
  const routeRef = useRef({ recordId, sourceId });
  watchedRecordsRef.current = watchedRecords;
  watchedLibraryUntilRef.current = watchedLibraryUntil;
  routeRef.current = { recordId, sourceId };

  async function loadLibrary(showLoader = true) {
    if (showLoader) setLoading(true);
    try {
      const payload = await api<{ items?: Summary[] }>("/api/library/records");
      setItems(payload.items ?? []);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось загрузить медиатеку");
    } finally {
      if (showLoader) setLoading(false);
    }
  }
  function watchRecord(nextRecordId: string, nextSourceId: string): void {
    const key = `${nextRecordId}:${nextSourceId}`;
    setWatchedRecords((current) => ({
      ...current,
      [key]: current[key] ?? {
        recordId: nextRecordId,
        sourceId: nextSourceId,
        startedAt: Date.now(),
        sawPending: false,
      },
    }));
  }
  function watchLibrary(): void {
    setWatchedLibraryUntil(Date.now() + 30_000);
  }
  async function refreshRecord(
    nextRecordId: string,
    nextSourceId: string,
    updateDraft = false,
  ): Promise<Detail> {
    const loaded = await api<Detail>(`/api/library/records/${nextRecordId}`);
    if (recordId === nextRecordId && sourceId === nextSourceId) {
      setDetail(loaded);
      if (updateDraft) setDraft({ ...tagsFor(loaded, nextSourceId, "final") });
    }
    await loadLibrary(false);
    return loaded;
  }
  function applyRoute(route: Route): void {
    setScreen(route.screen);
    setArtist(route.artist ?? "");
    setAlbum(route.album ?? "");
    setRecordId(route.recordId ?? "");
    setSourceId(route.sourceId ?? "");
    setEffectiveSourceId(route.sourceId ?? null);
    setEffectiveSourceError("");
    setEffectiveSourceSuccess("");
    setDetail(null);
    setLayer("final");
  }
  function navigate(route: Route): void {
    window.history.pushState({}, "", routePath(route));
    applyRoute(route);
  }
  async function loadTrack(item: Summary, source: Source): Promise<void> {
    try {
      const loaded = await api<Detail>(`/api/library/records/${item.record_id}`);
      setDetail(loaded);
      setDraft({ ...tagsFor(loaded, source.source_id, "final") });
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось открыть трек");
    }
  }
  async function scan() {
    setScanning(true);
    setNotice("Сканирование поставлено в очередь…");
    try {
      const job = await api<ScanJob>("/api/reconciliation/scan", { method: "POST" });
      setScanJobId(job.job_id);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Восстановление не удалось");
      setScanning(false);
    }
  }
  async function retryProvider(provider: ProviderName) {
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    try {
      const result = await api<{ source_id: string; queued: boolean }>(
        `/api/library/records/${recordId}/sources/${sourceId}/provider-retry`,
        { method: "POST", body: JSON.stringify({ provider }) },
      );
      setNotice(
        result.queued
          ? `${provider === "acoustid" ? "AcousticID" : "MusicBrainz"} поставлен в очередь`
          : "Этот провайдер уже обрабатывается или находится в очереди",
      );
      await refreshRecord(recordId, sourceId);
      if (result.queued) watchRecord(recordId, sourceId);
    } catch (error) {
      setNotice(
        error instanceof Error ? error.message : "Не удалось поставить провайдер в очередь",
      );
    } finally {
      setReprocessing(false);
    }
  }
  async function overrideRelease(releaseMbid: string) {
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    try {
      const result = await api<{ release_mbid: string; queued: boolean }>(
        `/api/library/records/${recordId}/sources/${sourceId}/musicbrainz/override`,
        { method: "POST", body: JSON.stringify({ release_mbid: releaseMbid }) },
      );
      setNotice(
        result.queued
          ? `Релиз ${result.release_mbid} поставлен в очередь анализа`
          : `Релиз ${result.release_mbid} сохранён`,
      );
      await refreshRecord(recordId, sourceId);
      if (result.queued) watchRecord(recordId, sourceId);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось загрузить release");
    } finally {
      setReprocessing(false);
    }
  }
  async function overrideRecording(request: RecordingCorrection) {
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    setRecordingCorrectionError("");
    setRecordingCorrectionReview("");
    try {
      const result: RecordingCorrectionResult = await submitRecordingCorrection(
        recordId,
        sourceId,
        request,
      );
      setNotice(`Запись ${result.recording_mbid} исправлена для выбранного источника`);
      await refreshRecord(recordId, sourceId);
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setRecordingCorrectionError(
          "Исправление конфликтует с сохранёнными свидетельствами провайдера.",
        );
        setRecordingCorrectionReview(
          "Требуется проверка исправления записи. Сверьте свидетельства и повторите позже.",
        );
        await refreshRecord(recordId, sourceId);
      } else {
        setRecordingCorrectionError(
          error instanceof ApiError
            ? error.status === 422
              ? "Проверьте MBID записи."
              : error.status === 503
                ? "MusicBrainz временно недоступен. Повторите исправление позже."
                : error.status === 404
                  ? "Выбранный источник записи больше недоступен. Обновите данные трека."
                  : "Не удалось отправить исправление записи."
            : error instanceof Error
              ? error.message
              : "Не удалось отправить исправление записи.",
        );
      }
      setNotice(
        error instanceof ApiError && error.status === 409
          ? "Исправление не применено: требуется проверка конфликта."
          : "Исправление записи не применено.",
      );
    } finally {
      setReprocessing(false);
    }
  }
  async function selectCandidate(selection: string) {
    const separator = selection.indexOf(":");
    const selectedProvider: ProviderName =
      separator > 0 && selection.slice(0, separator) === "acoustid" ? "acoustid" : "musicbrainz";
    const selectedKey = separator > 0 ? selection.slice(separator + 1) : selection;
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    try {
      const result = await api<{ revision: number | null; queued: boolean }>(
        `/api/library/records/${recordId}/sources/${sourceId}/candidates/select`,
        {
          method: "POST",
          body: JSON.stringify({ candidate_key: selectedKey, provider: selectedProvider }),
        },
      );
      setNotice(
        selectedProvider === "acoustid"
          ? "Запись AcousticID подтверждена; MusicBrainz поставлен в очередь"
          : result.queued
            ? `Релиз подтверждён. Final rev ${result.revision} поставлена в публикацию`
            : `Релиз подтверждён. Final rev ${result.revision} сохранена`,
      );
      await refreshRecord(recordId, sourceId, true);
      if (result.queued) watchRecord(recordId, sourceId);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось подтвердить кандидата");
    } finally {
      setReprocessing(false);
    }
  }
  async function selectEffectiveSourceForRecord(selectedSourceId: string) {
    if (!recordId || !selectedSourceId) return;
    setReprocessing(true);
    setEffectiveSourceError("");
    setEffectiveSourceSuccess("");
    try {
      const result: EffectiveSourceSelection = await selectEffectiveSource(recordId, {
        source_id: selectedSourceId,
      });
      setEffectiveSourceId(result.source_id);
      setEffectiveSourceSuccess("Источник выбран для публикации; очередь обновления создана");
      await refreshRecord(recordId, sourceId);
    } catch (error) {
      setEffectiveSourceError(
        error instanceof Error ? error.message : "Не удалось выбрать источник публикации",
      );
    } finally {
      setReprocessing(false);
    }
  }
  async function reprocessAll() {
    setReprocessing(true);
    try {
      const result = await api<{ readonly queued: number }>("/api/library/reprocess-all", {
        method: "POST",
      });
      setNotice(`Полная переобработка поставлена в очередь: ${result.queued}`);
      await loadLibrary(false);
      if (result.queued > 0) watchLibrary();
    } catch (error) {
      setNotice(
        error instanceof Error ? error.message : "Не удалось поставить провайдеры в очередь",
      );
    } finally {
      setReprocessing(false);
    }
  }
  async function reprocessSource(recordId: string, sourceId: string) {
    setReprocessing(true);
    try {
      const result = await api<{ queued: boolean; kind: string | null }>(
        `/api/library/records/${encodeURIComponent(recordId)}/sources/${encodeURIComponent(sourceId)}/reprocess`,
        { method: "POST" },
      );
      setNotice(
        result.queued
          ? "Повторный анализ поставлен в очередь"
          : "Этот источник уже обрабатывается или не требует повторного анализа",
      );
      await loadLibrary(false);
      if (result.queued) watchRecord(recordId, sourceId);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось повторить анализ");
    } finally {
      setReprocessing(false);
    }
  }
  async function saveMetadata() {
    if (!detail || !sourceId) return false;
    setSaving(true);
    try {
      const result = await api<{ revision: number; queued: boolean; tags: Tags }>(
        `/api/library/records/${detail.record_id}/metadata`,
        { method: "PUT", body: JSON.stringify({ source_id: sourceId, tags: draft }) },
      );
      setNotice(
        result.queued
          ? `Финальная ревизия ${result.revision} сохранена и поставлена в публикацию`
          : `Финальная ревизия ${result.revision} сохранена`,
      );
      await refreshRecord(detail.record_id, sourceId);
      setDraft({ ...result.tags });
      if (result.queued) watchRecord(detail.record_id, sourceId);
      return true;
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось сохранить метаданные");
      return false;
    } finally {
      setSaving(false);
    }
  }
  async function loadSettings() {
    setSettingsLoading(true);
    try {
      const loaded = await api<RuntimeSettings>("/api/settings");
      setSettingsDraft({ ...loaded, acoustid_client_key: "" });
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось загрузить настройки");
    } finally {
      setSettingsLoading(false);
    }
  }
  async function loadGenres() {
    setGenreLoading(true);
    try {
      setGenreCatalog(await api<GenreCatalog>("/api/genres"));
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось загрузить жанры");
    } finally {
      setGenreLoading(false);
    }
  }
  async function loadSourceRoots() {
    setSourceRootsLoading(true);
    setSourceRootsError("");
    try {
      setSourceRoots((await listSourceRoots()).items);
      setSourceRootsLoaded(true);
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : "Не удалось загрузить корни");
    } finally {
      setSourceRootsLoading(false);
    }
  }
  async function loadSourceRootCandidates() {
    setSourceRootCandidatesLoading(true);
    try {
      setSourceRootCandidates((await listSourceRootCandidates()).items);
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : "Не удалось загрузить папки");
    } finally {
      setSourceRootCandidatesLoading(false);
    }
  }
  async function loadStorage(path?: string) {
    setStorageLoading(true);
    try {
      const [browser, config] = await Promise.all([browseStorage(path), getStorageConfig()]);
      setStorageBrowser(browser);
      setStorageConfig(config);
    } catch (error) {
      setSourceRootsError(
        error instanceof Error ? error.message : "Не удалось открыть папки контейнера",
      );
    } finally {
      setStorageLoading(false);
    }
  }
  async function previewConfiguredStorageOutput(path: string) {
    setStorageLoading(true);
    try {
      setStorageOutputPreview(await previewStorageOutput(path));
    } catch (error) {
      setSourceRootsError(
        error instanceof Error ? error.message : "Не удалось проверить папку output",
      );
    } finally {
      setStorageLoading(false);
    }
  }
  async function moveConfiguredStorageOutput(path: string) {
    setStorageLoading(true);
    try {
      setStorageConfig(await moveStorageOutput(path));
      setStorageOutputPreview(null);
      await loadStorage(path);
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : "Не удалось перенести output");
    } finally {
      setStorageLoading(false);
    }
  }
  async function createConfiguredSourceRoot(request: SourceRootCreate) {
    setSourceRootCreating(true);
    setSourceRootsError("");
    try {
      const created = await createSourceRoot(request);
      setSourceRoots((current) => [...current, created]);
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : "Не удалось добавить корень");
    } finally {
      setSourceRootCreating(false);
    }
  }
  async function removeConfiguredSourceRoot(rootId: string) {
    setSourceRootRemoving(true);
    setSourceRootsError("");
    try {
      await removeSourceRoot(rootId);
      setSourceRoots((current) => current.filter((item) => item.id !== rootId));
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : "Не удалось удалить корень");
    } finally {
      setSourceRootRemoving(false);
    }
  }
  async function syncGenres() {
    setGenreSyncing(true);
    try {
      setGenreCatalog(await api<GenreCatalog>("/api/genres/sync", { method: "POST" }));
      setNotice("Каталог жанров MusicBrainz обновлён");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось обновить жанры");
    } finally {
      setGenreSyncing(false);
    }
  }
  async function saveSettings() {
    if (!settingsDraft) return;
    setSettingsSaving(true);
    try {
      const result = await api<RuntimeSettings>("/api/settings", {
        method: "PUT",
        body: JSON.stringify({
          ...settingsDraft,
          acoustid_client_key: settingsDraft.acoustid_client_key || null,
        }),
      });
      setSettingsDraft({ ...result, acoustid_client_key: "" });
      setNotice("Настройки сохранены");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Не удалось сохранить настройки");
    } finally {
      setSettingsSaving(false);
    }
  }
  useEffect(() => {
    void loadLibrary();
  }, []);
  useEffect(() => {
    let busy = false;
    const poll = async () => {
      if (busy || document.visibilityState === "hidden") return;
      busy = true;
      try {
        const libraryWatchActive = watchedLibraryUntilRef.current > 0;
        if (scanJobId !== null) {
          const job = await api<ScanJob>(
            `/api/reconciliation/scan/${encodeURIComponent(scanJobId)}`,
          );
          if (job.state === "completed" && job.result !== null) {
            const result = job.result;
            setNotice(
              `Новых: ${result.added}; изменённых: ${result.changed}; перемещённых: ${result.moved}; ` +
                `удалённых: ${result.removed}; в очереди: ${result.queued_jobs}`,
            );
            setScanJobId(null);
            setScanning(false);
            await loadLibrary();
            if (result.queued_jobs > 0) watchLibrary();
          } else if (job.state !== "queued" && job.state !== "running") {
            setNotice("Сканирование завершилось с ошибкой. Повторите попытку позже.");
            setScanJobId(null);
            setScanning(false);
          } else {
            setNotice(
              job.state === "running" ? "Сканирование выполняется…" : "Сканирование в очереди…",
            );
          }
        }
        if (libraryWatchActive && Date.now() >= watchedLibraryUntilRef.current)
          setWatchedLibraryUntil(0);
        const currentWatches = watchedRecordsRef.current;
        for (const [key, watch] of Object.entries(currentWatches)) {
          if (Date.now() - watch.startedAt >= 30_000) {
            setWatchedRecords((current) => {
              const next = { ...current };
              delete next[key];
              return next;
            });
            setNotice("Автообновление остановлено по таймауту. Обновите данные вручную.");
            continue;
          }
          const loaded = await api<Detail>(`/api/library/records/${watch.recordId}`);
          const pending = detailIsPending(loaded);
          if (
            routeRef.current.recordId === watch.recordId &&
            routeRef.current.sourceId === watch.sourceId
          )
            setDetail(loaded);
          if (pending && !watch.sawPending) {
            setWatchedRecords((current) => ({ ...current, [key]: { ...watch, sawPending: true } }));
          } else if (!pending && (watch.sawPending || loaded.events.length > 0)) {
            setWatchedRecords((current) => {
              const next = { ...current };
              delete next[key];
              return next;
            });
            setNotice("Данные обновлены после фоновой обработки");
          }
        }
        if (libraryWatchActive || Object.keys(currentWatches).length > 0) await loadLibrary(false);
      } catch (error) {
        setNotice(
          error instanceof Error
            ? `Автообновление: ${error.message}`
            : "Автообновление временно недоступно",
        );
      } finally {
        busy = false;
      }
    };
    const timer = window.setInterval(() => void poll(), 1000);
    return () => window.clearInterval(timer);
  }, [scanJobId]);
  useEffect(() => {
    if (screen === "settings" && !settingsDraft && !settingsLoading) void loadSettings();
  }, [screen, settingsDraft, settingsLoading]);
  useEffect(() => {
    if (screen === "settings" && !genreCatalog && !genreLoading) void loadGenres();
  }, [screen, genreCatalog, genreLoading]);
  useEffect(() => {
    if (screen === "settings" && !sourceRootsLoaded && !sourceRootsLoading && !sourceRootsError)
      void loadSourceRoots();
  }, [screen, sourceRootsLoaded, sourceRootsLoading, sourceRootsError]);
  useEffect(() => {
    if (screen === "settings" && !sourceRootCandidatesLoading && sourceRootCandidates.length === 0)
      void loadSourceRootCandidates();
  }, [screen, sourceRootCandidates.length, sourceRootCandidatesLoading]);
  useEffect(() => {
    if (screen === "settings" && !storageBrowser && !storageLoading) void loadStorage();
  }, [screen, storageBrowser, storageLoading]);
  useEffect(() => {
    const onPopState = () => applyRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);
  useEffect(() => {
    if (screen !== "track" || !recordId || !sourceId || !items.length || detail) return;
    const item = items.find((entry) => entry.record_id === recordId);
    const source = item?.sources.find((entry) => entry.source_id === sourceId);
    if (item && source) void loadTrack(item, source);
  }, [items, screen, recordId, sourceId, detail]);
  const tracks = useMemo(
    () =>
      items
        .flatMap((item) => item.sources.map((source) => ({ item, source })))
        .filter(({ item, source }) =>
          `${artistFor(item, source.source_id)} ${albumFor(item, source.source_id)} ${titleFor(item, source.source_id)}`
            .toLowerCase()
            .includes(query.toLowerCase()),
        ),
    [items, query],
  );
  const artists = [
    ...new Set(tracks.map(({ item, source }) => artistFor(item, source.source_id))),
  ].sort(compareNames);
  const albums = [
    ...new Set(
      tracks
        .filter(({ item, source }) => artistFor(item, source.source_id) === artist)
        .map(({ item, source }) => albumFor(item, source.source_id)),
    ),
  ].sort(compareNames);
  const albumTracks = tracks
    .filter(
      ({ item, source }) =>
        artistFor(item, source.source_id) === artist && albumFor(item, source.source_id) === album,
    )
    .sort(({ item: leftItem, source: leftSource }, { item: rightItem, source: rightSource }) => {
      const leftNumber = trackNumberFor(leftItem, leftSource.source_id);
      const rightNumber = trackNumberFor(rightItem, rightSource.source_id);
      if (leftNumber !== rightNumber) {
        if (leftNumber === null) return 1;
        if (rightNumber === null) return -1;
        return leftNumber - rightNumber;
      }
      return (
        compareNames(
          titleFor(leftItem, leftSource.source_id),
          titleFor(rightItem, rightSource.source_id),
        ) || leftItem.record_id.localeCompare(rightItem.record_id)
      );
    });
  const currentTrack =
    albumTracks.find(
      ({ item, source }) => item.record_id === recordId && source.source_id === sourceId,
    ) ??
    tracks.find(({ item, source }) => item.record_id === recordId && source.source_id === sourceId);
  const currentTags =
    detail && sourceId ? (layer === "final" ? draft : tagsFor(detail, sourceId, layer)) : {};
  function back() {
    if (screen === "track") navigate({ screen: "tracks", artist, album });
    else if (screen === "tracks") navigate({ screen: "albums", artist, album });
    else if (screen === "albums") navigate({ screen: "artists" });
  }
  return {
    items,
    detail,
    screen,
    artist,
    album,
    recordId,
    sourceId,
    layer,
    draft,
    query,
    notice,
    loading,
    scanning,
    saving,
    reprocessing,
    effectiveSourceId,
    effectiveSourceError,
    effectiveSourceSuccess,
    recordingCorrectionError,
    recordingCorrectionReview,
    settingsDraft,
    settingsLoading,
    settingsSaving,
    genreCatalog,
    genreSearch,
    genreLoading,
    genreSyncing,
    sourceRoots,
    sourceRootsLoading,
    sourceRootsError,
    sourceRootCreating,
    sourceRootRemoving,
    sourceRootCandidates,
    sourceRootCandidatesLoading,
    storageBrowser,
    storageConfig,
    storageOutputPreview,
    storageLoading,
    watchedRecords,
    watchedLibraryUntil,
    tracks,
    artists,
    albums,
    albumTracks,
    currentTrack,
    currentTags,
    navigate,
    back,
    loadLibrary,
    scan,
    reprocessAll,
    reprocessSource,
    saveMetadata,
    retryProvider,
    overrideRelease,
    overrideRecording,
    selectCandidate,
    selectEffectiveSource: selectEffectiveSourceForRecord,
    saveSettings,
    syncGenres,
    createSourceRoot: createConfiguredSourceRoot,
    removeSourceRoot: removeConfiguredSourceRoot,
    browseStorage: loadStorage,
    previewStorageOutput: previewConfiguredStorageOutput,
    moveStorageOutput: moveConfiguredStorageOutput,
    setQuery,
    setNotice,
    setLayer,
    setDraft,
    setSettingsDraft,
    setGenreSearch,
  };
}
