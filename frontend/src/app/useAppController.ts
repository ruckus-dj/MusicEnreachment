import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  api,
  browseStorage,
  createSourceRoot,
  getLibraryStatus,
  getPublicationReconciliation,
  getStorageConfig,
  getWorkerQueue,
  type LibraryStatus,
  type LibraryTrack,
  listLibraryAlbums,
  listLibraryArtists,
  listLibraryTracks,
  listManualActions,
  listSourceRootCandidates,
  listSourceRoots,
  loadMusicBrainzCandidates,
  moveStorageOutput,
  previewStorageOutput,
  refreshLibraryMetadata,
  removeSourceRoot,
  selectEffectiveSource,
  startPublicationReconciliation,
} from "../api/client";
import {
  albumArtistsFor,
  albumFor,
  albumKeyFor,
  compareNames,
  detailIsPending,
  tagsFor,
  titleFor,
  trackNumberFor,
  UNKNOWN_ALBUM_LABEL,
  UNKNOWN_ARTIST_LABEL,
} from "../domain/metadata";
import { runtimeSettingsDraftFrom, runtimeSettingsPayload } from "../domain/settings";
import { errorMessages } from "../errorMessages";
import { parseRoute, routePath } from "../routing";
import type {
  Detail,
  EffectiveSourceSelection,
  GenreCatalog,
  Layer,
  ManualActionFilter,
  MusicBrainzCandidateLookup,
  ProviderName,
  PublicationReconciliationJob,
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
  WorkerQueue,
} from "../types";

export type CatalogTrack = { item: Summary; source: Source };
export type LibraryCatalogTrack = LibraryTrack;

export type PublicationFilter = "all" | "published" | "unpublished";

const LIBRARY_WATCH_REFRESH_INTERVAL_MS = 5_000;

export type CatalogAlbum = {
  readonly key: string;
  readonly title: string;
  readonly trackCount: number;
  readonly artworkUrl: string | null;
};

export function catalogSource(item: Summary): Source | undefined {
  const currentPublication = item.publications.find(
    (publication) => publication.state === "current",
  );
  return (
    item.sources.find((source) => source.source_id === currentPublication?.source_id) ??
    item.sources.find((source) => source.state !== "disappeared") ??
    item.sources[0]
  );
}

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
  publicationFilter: PublicationFilter;
  manualActionFilter: ManualActionFilter;
  manualActionCounts: Readonly<Record<ManualActionFilter, number>>;
  libraryStatus: LibraryStatus;
  notice: string;
  loading: boolean;
  scanning: boolean;
  saving: boolean;
  reprocessing: boolean;
  refreshingMetadata: boolean;
  reconcilingPublications: boolean;
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
  workerQueue: WorkerQueue | null;
  workerQueueLoading: boolean;
  workerQueueError: string;
  watchedRecords: Record<string, WatchedRecord>;
  watchedLibraryUntil: number;
  tracks: CatalogTrack[];
  artists: string[];
  catalogArtistTrackCounts: Readonly<Record<string, number>>;
  catalogTrackCount: number;
  albums: CatalogAlbum[];
  albumTracks: LibraryCatalogTrack[];
  currentTrack: CatalogTrack | undefined;
  currentTags: Tags;
  navigate: (route: Route) => void;
  back: () => void;
  loadLibrary: (showLoader?: boolean) => Promise<void>;
  scan: () => Promise<void>;
  reprocessAll: () => Promise<void>;
  refreshMetadata: () => Promise<void>;
  reconcilePublications: () => Promise<void>;
  reprocessSource: (recordId: string, sourceId: string) => Promise<void>;
  saveMetadata: () => Promise<boolean>;
  encodingApplied: (queued: boolean) => Promise<void>;
  retryProvider: (provider: ProviderName) => Promise<void>;
  loadMusicBrainzCandidates: (request: MusicBrainzCandidateLookup) => Promise<void>;
  selectCandidate: (selection: string) => Promise<void>;
  selectEffectiveSource: (sourceId: string) => Promise<void>;
  saveSettings: () => Promise<void>;
  syncGenres: () => Promise<void>;
  createSourceRoot: (request: SourceRootCreate) => Promise<void>;
  removeSourceRoot: (rootId: string) => Promise<void>;
  browseStorage: (path?: string) => Promise<void>;
  previewStorageOutput: (path: string) => Promise<void>;
  moveStorageOutput: (path: string) => Promise<void>;
  loadWorkerQueue: () => Promise<void>;
  setQuery: (value: string) => void;
  setPublicationFilter: (value: PublicationFilter) => void;
  setManualActionFilter: (value: ManualActionFilter) => void;
  setNotice: (value: string) => void;
  setLayer: (value: Layer) => void;
  setDraft: (value: Tags) => void;
  setSettingsDraft: (value: RuntimeSettingsDraft | null) => void;
  setGenreSearch: (value: string) => void;
};

export function useAppController(): AppControllerModel {
  const [items, setItems] = useState<Summary[]>([]);
  const [catalogArtists, setCatalogArtists] = useState<string[]>([]);
  const [catalogArtistTrackCounts, setCatalogArtistTrackCounts] = useState<Record<string, number>>(
    {},
  );
  const [catalogTrackCount, setCatalogTrackCount] = useState(0);
  const [catalogAlbums, setCatalogAlbums] = useState<CatalogAlbum[]>([]);
  const [catalogAlbumTracks, setCatalogAlbumTracks] = useState<LibraryCatalogTrack[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [initialRoute] = useState(() =>
    parseRoute(window.location.pathname, window.location.search),
  );
  const [screen, setScreen] = useState<Screen>(initialRoute.screen);
  const [artist, setArtist] = useState(
    initialRoute.artistMissing ? UNKNOWN_ARTIST_LABEL : (initialRoute.artist ?? ""),
  );
  const [artistMissing, setArtistMissing] = useState(initialRoute.artistMissing ?? false);
  const [album, setAlbum] = useState(initialRoute.album ?? "");
  const [albumMissing, setAlbumMissing] = useState(initialRoute.albumMissing ?? false);
  const [recordId, setRecordId] = useState(initialRoute.recordId ?? "");
  const [sourceId, setSourceId] = useState(initialRoute.sourceId ?? "");
  const [layer, setLayer] = useState<Layer>("final");
  const [draft, setDraft] = useState<Tags>({});
  const [query, setQuery] = useState("");
  const [publicationFilter, setPublicationFilterState] = useState<PublicationFilter>(
    initialRoute.publicationFilter ?? "all",
  );
  const [manualActionFilter, setManualActionFilterState] = useState<ManualActionFilter>(
    initialRoute.manualActionFilter ?? "analysis-error",
  );
  const [manualActionCounts, setManualActionCounts] = useState<Record<ManualActionFilter, number>>({
    "analysis-error": 0,
    "needs-review": 0,
  });
  const [libraryStatus, setLibraryStatus] = useState<LibraryStatus>({
    total_track_count: 0,
    has_analysis: false,
  });
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(true);
  const [scanning, setScanning] = useState(false);
  const [scanJobId, setScanJobId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [reprocessing, setReprocessing] = useState(false);
  const [refreshingMetadata, setRefreshingMetadata] = useState(false);
  const [reconcilingPublications, setReconcilingPublications] = useState(false);
  const [publicationReconciliationJobId, setPublicationReconciliationJobId] = useState<
    string | null
  >(null);
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
  const [workerQueue, setWorkerQueue] = useState<WorkerQueue | null>(null);
  const [workerQueueLoading, setWorkerQueueLoading] = useState(false);
  const [workerQueueError, setWorkerQueueError] = useState("");
  const [watchedRecords, setWatchedRecords] = useState<Record<string, WatchedRecord>>({});
  const [watchedLibraryUntil, setWatchedLibraryUntil] = useState(0);
  const watchedRecordsRef = useRef(watchedRecords);
  const watchedLibraryUntilRef = useRef(watchedLibraryUntil);
  const nextLibraryRefreshAtRef = useRef(0);
  const routeRef = useRef({ recordId, sourceId });
  const encodingRefreshGeneration = useRef(0);
  watchedRecordsRef.current = watchedRecords;
  watchedLibraryUntilRef.current = watchedLibraryUntil;
  routeRef.current = { recordId, sourceId };

  async function loadLibrary(showLoader = true, preserveDraft = false, isActive = () => true) {
    const generation = encodingRefreshGeneration.current;
    const isCurrent = () => isActive() && generation === encodingRefreshGeneration.current;
    if (showLoader) setLoading(true);
    try {
      if (screen === "track" && recordId) {
        const loaded = await api<Detail>(`/api/library/records/${encodeURIComponent(recordId)}`);
        if (!isCurrent()) return;
        const source = sourceId
          ? (loaded.sources.find((entry) => entry.source_id === sourceId) ?? catalogSource(loaded))
          : catalogSource(loaded);
        setItems([loaded]);
        setDetail(loaded);
        if (source) {
          if (!sourceId) {
            setSourceId(source.source_id);
            setEffectiveSourceId(source.source_id);
          }
          if (!preserveDraft) setDraft({ ...tagsFor(loaded, source.source_id, "final") });
        }
        return;
      }
      const published = publicationFilter === "all" ? undefined : publicationFilter === "published";
      const catalogCountPromise =
        screen === "albums" || screen === "tracks" ? listLibraryArtists(published) : null;
      if (screen === "artists") {
        const payload = await listLibraryArtists(published);
        if (!isCurrent()) return;
        setCatalogArtists(payload.items.map((item) => item.name ?? UNKNOWN_ARTIST_LABEL));
        setCatalogArtistTrackCounts(
          Object.fromEntries(
            payload.items.map((item) => [item.name ?? UNKNOWN_ARTIST_LABEL, item.track_count]),
          ),
        );
        setCatalogTrackCount(payload.total_track_count);
        setCatalogAlbums([]);
        setItems([]);
        return;
      }
      if (screen === "albums") {
        const [payload, countPayload] = await Promise.all([
          listLibraryAlbums(artistMissing || !artist ? null : artist, published, artistMissing),
          catalogCountPromise,
        ]);
        if (!isCurrent()) return;
        if (countPayload !== null) setCatalogTrackCount(countPayload.total_track_count);
        setCatalogAlbums(
          payload.items.map((item) => ({
            key: item.album_id ?? `album:${item.album_name ?? ""}`,
            title: item.album_name ?? UNKNOWN_ALBUM_LABEL,
            trackCount: item.track_count,
            artworkUrl: item.artwork_url ?? null,
          })),
        );
        setItems([]);
        return;
      }
      if (screen === "manual-actions") {
        const payload = await listManualActions(manualActionFilter);
        if (!isCurrent()) return;
        setItems(payload.items);
        setManualActionCounts({
          "analysis-error": payload.counts.analysis_error,
          "needs-review": payload.counts.needs_review,
        });
        return;
      }
      if (screen !== "tracks") {
        const status = await getLibraryStatus();
        if (!isCurrent()) return;
        setLibraryStatus(status);
        setCatalogTrackCount(status.total_track_count);
        setItems([]);
        return;
      }
      const selectedAlbumId =
        album && !album.startsWith("album:") && !album.startsWith("record:")
          ? album.startsWith("id:")
            ? album.slice("id:".length)
            : album
          : undefined;
      const selectedAlbumName = album.startsWith("album:")
        ? album.slice("album:".length)
        : undefined;
      const [payload, countPayload] = await Promise.all([
        listLibraryTracks(
          artistMissing || !artist ? null : artist,
          selectedAlbumId,
          selectedAlbumName,
          published,
          artistMissing,
          albumMissing,
        ),
        catalogCountPromise,
      ]);
      if (!isCurrent()) return;
      if (countPayload !== null) setCatalogTrackCount(countPayload.total_track_count);
      setCatalogAlbumTracks(payload.items);
      setItems([]);
    } catch (error) {
      if (isCurrent())
        setNotice(error instanceof Error ? error.message : errorMessages.loadLibrary);
    } finally {
      if (showLoader && isCurrent()) setLoading(false);
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
    const now = Date.now();
    nextLibraryRefreshAtRef.current = now + LIBRARY_WATCH_REFRESH_INTERVAL_MS;
    setWatchedLibraryUntil(now + 30_000);
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
    encodingRefreshGeneration.current += 1;
    routeRef.current = { recordId: route.recordId ?? "", sourceId: route.sourceId ?? "" };
    setScreen(route.screen);
    setArtist(route.artistMissing ? UNKNOWN_ARTIST_LABEL : (route.artist ?? ""));
    setArtistMissing(route.artistMissing ?? false);
    setAlbum(route.album ?? "");
    setAlbumMissing(route.albumMissing ?? false);
    setRecordId(route.recordId ?? "");
    setSourceId(route.sourceId ?? "");
    setPublicationFilterState(route.publicationFilter ?? "all");
    setManualActionFilterState(route.manualActionFilter ?? "analysis-error");
    setEffectiveSourceId(route.sourceId ?? null);
    setEffectiveSourceError("");
    setEffectiveSourceSuccess("");
    setDetail(null);
    setLayer("final");
  }
  function navigate(route: Route): void {
    const nextRoute = { ...route, publicationFilter: route.publicationFilter ?? publicationFilter };
    window.history.pushState({}, "", routePath(nextRoute));
    applyRoute(nextRoute);
  }
  function setPublicationFilter(value: PublicationFilter): void {
    setPublicationFilterState(value);
    window.history.replaceState(
      {},
      "",
      routePath({
        screen,
        artist: artist || undefined,
        artistMissing,
        album: album || undefined,
        recordId: recordId || undefined,
        sourceId: sourceId || undefined,
        publicationFilter: value,
      }),
    );
  }
  function setManualActionFilter(value: ManualActionFilter): void {
    navigate({ screen: "manual-actions", manualActionFilter: value });
  }
  async function loadTrack(item: Summary, source: Source): Promise<void> {
    try {
      const loaded = await api<Detail>(`/api/library/records/${item.record_id}`);
      setDetail(loaded);
      setDraft({ ...tagsFor(loaded, source.source_id, "final") });
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.openTrack);
    }
  }
  async function scan() {
    setScanning(true);
    setNotice("Сканирование поставлено в очередь…");
    try {
      const job = await api<ScanJob>("/api/reconciliation/scan", { method: "POST" });
      setScanJobId(job.job_id);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.recoverLibrary);
      setScanning(false);
    }
  }
  async function reconcilePublications() {
    setReconcilingPublications(true);
    setNotice("Проверка папки публикаций поставлена в очередь…");
    try {
      const job = await startPublicationReconciliation();
      setPublicationReconciliationJobId(job.job_id);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.publicationReconciliation);
      setReconcilingPublications(false);
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
      setNotice(error instanceof Error ? error.message : errorMessages.queueProvider);
    } finally {
      setReprocessing(false);
    }
  }
  async function loadMusicBrainzCandidateOptions(request: MusicBrainzCandidateLookup) {
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    setRecordingCorrectionError("");
    setRecordingCorrectionReview("");
    try {
      const result = await loadMusicBrainzCandidates(recordId, sourceId, request);
      setNotice(
        result.release_mbid
          ? `Пара ${result.release_mbid}:${result.recording_mbid} добавлена для проверки`
          : `Для recording ${result.recording_mbid} найдено релизов: ${result.candidate_count}`,
      );
      await refreshRecord(recordId, sourceId);
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setRecordingCorrectionError(errorMessages.correctionConflict);
        setRecordingCorrectionReview(errorMessages.correctionReview);
        await refreshRecord(recordId, sourceId);
      } else {
        setRecordingCorrectionError(
          error instanceof ApiError
            ? error.status === 422
              ? errorMessages.invalidRecordingMbid
              : error.status === 503
                ? errorMessages.correctionProviderUnavailable
                : error.status === 404
                  ? errorMessages.correctionSourceUnavailable
                  : errorMessages.submitCorrection
            : error instanceof Error
              ? error.message
              : errorMessages.submitCorrection,
        );
      }
      setNotice(
        error instanceof ApiError && error.status === 409
          ? errorMessages.correctionConflictNotice
          : errorMessages.correctionFailedNotice,
      );
    } finally {
      setReprocessing(false);
    }
  }
  async function selectCandidate(selection: string) {
    const separator = selection.indexOf(":");
    const parts = selection.split(":");
    const selectedProvider: ProviderName = parts[0] === "acoustid" ? "acoustid" : "musicbrainz";
    const selectedEntity = parts[1] === "recording" ? "recording" : "recording_release";
    const selectedKey = separator > 0 ? parts.slice(2).join(":") : selection;
    if (!recordId || !sourceId) return;
    setReprocessing(true);
    try {
      const result = await api<{ record_id: string; revision: number | null; queued: boolean }>(
        `/api/library/records/${recordId}/sources/${sourceId}/candidates/select`,
        {
          method: "POST",
          body: JSON.stringify({
            candidate_key: selectedKey,
            provider: selectedProvider,
            entity: selectedEntity,
          }),
        },
      );
      setNotice(
        selectedProvider === "acoustid"
          ? "Запись AcousticID подтверждена; MusicBrainz поставлен в очередь"
          : result.queued
            ? `Релиз подтверждён. Final rev ${result.revision} поставлена в публикацию`
            : `Релиз подтверждён. Final rev ${result.revision} сохранена`,
      );
      if (result.record_id !== recordId) {
        if (result.queued) watchRecord(result.record_id, sourceId);
        setItems([]);
        navigate({ screen: "track", recordId: result.record_id, sourceId });
        return;
      }
      await refreshRecord(result.record_id, sourceId, true);
      if (result.queued) watchRecord(result.record_id, sourceId);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.confirmCandidate);
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
        error instanceof Error ? error.message : errorMessages.selectPublicationSource,
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
      setNotice(error instanceof Error ? error.message : errorMessages.queueProviders);
    } finally {
      setReprocessing(false);
    }
  }
  async function reprocessSource(recordId: string, sourceId: string) {
    setReprocessing(true);
    try {
      const result = await api<{
        readonly queued: boolean;
        readonly kind: string | null;
        readonly replacement_record_id: string | null;
        readonly replacement_source_id: string | null;
      }>(
        `/api/library/records/${encodeURIComponent(recordId)}/sources/${encodeURIComponent(sourceId)}/reprocess`,
        { method: "POST" },
      );
      if (result.replacement_record_id && result.replacement_source_id) {
        setNotice("Открыта актуальная версия файла с результатами MusicBrainz");
        navigate({
          screen: "track",
          recordId: result.replacement_record_id,
          sourceId: result.replacement_source_id,
        });
        return;
      }
      setNotice(
        result.queued
          ? "Повторный анализ поставлен в очередь"
          : "Этот источник уже обрабатывается или не требует повторного анализа",
      );
      await loadLibrary(false);
      if (result.queued) watchRecord(recordId, sourceId);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.retryAnalysis);
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
      setNotice(error instanceof Error ? error.message : errorMessages.saveMetadata);
      return false;
    } finally {
      setSaving(false);
    }
  }
  async function refreshMetadata() {
    setRefreshingMetadata(true);
    try {
      const result = await refreshLibraryMetadata();
      setNotice(`Обновление подтверждённых пар MusicBrainz поставлено в очередь: ${result.queued}`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.refreshMetadata);
    } finally {
      setRefreshingMetadata(false);
    }
  }
  async function loadSettings() {
    setSettingsLoading(true);
    try {
      const loaded = await api<RuntimeSettings>("/api/settings");
      setSettingsDraft(runtimeSettingsDraftFrom(loaded));
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.loadSettings);
    } finally {
      setSettingsLoading(false);
    }
  }
  async function loadGenres() {
    setGenreLoading(true);
    try {
      setGenreCatalog(await api<GenreCatalog>("/api/genres"));
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.loadGenres);
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
      setSourceRootsError(error instanceof Error ? error.message : errorMessages.loadSourceRoots);
    } finally {
      setSourceRootsLoading(false);
    }
  }
  async function loadSourceRootCandidates() {
    setSourceRootCandidatesLoading(true);
    try {
      setSourceRootCandidates((await listSourceRootCandidates()).items);
    } catch (error) {
      setSourceRootsError(
        error instanceof Error ? error.message : errorMessages.loadSourceRootCandidates,
      );
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
      setSourceRootsError(error instanceof Error ? error.message : errorMessages.browseStorage);
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
        error instanceof Error ? error.message : errorMessages.previewStorageOutput,
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
      setSourceRootsError(error instanceof Error ? error.message : errorMessages.moveStorageOutput);
    } finally {
      setStorageLoading(false);
    }
  }
  async function loadWorkerQueue(showLoader = true) {
    if (showLoader) setWorkerQueueLoading(true);
    setWorkerQueueError("");
    try {
      setWorkerQueue(await getWorkerQueue());
    } catch (error) {
      setWorkerQueueError(error instanceof Error ? error.message : errorMessages.loadWorkerQueue);
    } finally {
      if (showLoader) setWorkerQueueLoading(false);
    }
  }
  async function createConfiguredSourceRoot(request: SourceRootCreate) {
    setSourceRootCreating(true);
    setSourceRootsError("");
    try {
      const created = await createSourceRoot(request);
      setSourceRoots((current) => [...current, created]);
    } catch (error) {
      setSourceRootsError(error instanceof Error ? error.message : errorMessages.createSourceRoot);
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
      setSourceRootsError(error instanceof Error ? error.message : errorMessages.removeSourceRoot);
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
      setNotice(error instanceof Error ? error.message : errorMessages.refreshGenres);
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
        body: JSON.stringify(runtimeSettingsPayload(settingsDraft)),
      });
      setSettingsDraft(runtimeSettingsDraftFrom(result));
      setNotice("Настройки сохранены");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : errorMessages.saveSettings);
    } finally {
      setSettingsSaving(false);
    }
  }
  useEffect(() => {
    void loadLibrary();
  }, [album, artist, manualActionFilter, publicationFilter, recordId, screen]);
  useEffect(() => {
    const MIN_POLL_DELAY_MS = 1_000;
    const MAX_POLL_DELAY_MS = 15_000;
    let busy = false;
    let cancelled = false;
    let delay = MIN_POLL_DELAY_MS;
    let timer = 0;
    const scheduleNext = () => {
      if (!cancelled) timer = window.setTimeout(() => void poll(), delay);
    };
    const poll = async () => {
      if (busy || document.visibilityState === "hidden") {
        scheduleNext();
        return;
      }
      busy = true;
      let hadActiveWork = false;
      const generation = encodingRefreshGeneration.current;
      const isCurrent = () => !cancelled && generation === encodingRefreshGeneration.current;
      const isVisibleWatch = (watch: WatchedRecord) =>
        screen !== "track" ||
        (routeRef.current.recordId === watch.recordId &&
          routeRef.current.sourceId === watch.sourceId);
      const showPollError = (error: unknown) => {
        setNotice(
          error instanceof Error
            ? errorMessages.autoRefreshError(error.message)
            : errorMessages.autoRefreshUnavailable,
        );
      };
      try {
        const libraryWatchActive = watchedLibraryUntilRef.current > 0;
        hadActiveWork =
          scanJobId !== null ||
          publicationReconciliationJobId !== null ||
          libraryWatchActive ||
          Object.keys(watchedRecordsRef.current).length > 0;
        if (scanJobId !== null) {
          const job = await api<ScanJob>(
            `/api/reconciliation/scan/${encodeURIComponent(scanJobId)}`,
          );
          if (!isCurrent()) return;
          if (job.state === "completed" && job.result !== null) {
            const result = job.result;
            setNotice(
              `Новых: ${result.added}; изменённых: ${result.changed}; перемещённых: ${result.moved}; ` +
                `удалённых: ${result.removed}; в очереди: ${result.queued_jobs}`,
            );
            await loadLibrary(false, true, isCurrent);
            if (!isCurrent()) return;
            setScanJobId(null);
            setScanning(false);
            if (result.queued_jobs > 0) watchLibrary();
          } else if (job.state !== "queued" && job.state !== "running") {
            setNotice(errorMessages.scanFailed);
            setScanJobId(null);
            setScanning(false);
          } else {
            setNotice(
              job.state === "running" ? "Сканирование выполняется…" : "Сканирование в очереди…",
            );
          }
        }
        if (publicationReconciliationJobId !== null) {
          const job: PublicationReconciliationJob = await getPublicationReconciliation(
            publicationReconciliationJobId,
          );
          if (!isCurrent()) return;
          if (job.state === "completed" && job.result !== null) {
            const result = job.result;
            setNotice(
              `Удалено файлов: ${result.removed_files}; папок: ${result.removed_directories}; ` +
                `отсутствующих публикаций: ${result.missing_publications}; ` +
                `повторно поставлено: ${result.queued_jobs}; сохранено NFO: ${result.preserved_nfo}`,
            );
            await loadLibrary(false, true, isCurrent);
            if (!isCurrent()) return;
            setPublicationReconciliationJobId(null);
            setReconcilingPublications(false);
            if (result.queued_jobs > 0) watchLibrary();
          } else if (job.state !== "queued" && job.state !== "running") {
            setNotice(errorMessages.publicationReconciliationFailed);
            setPublicationReconciliationJobId(null);
            setReconcilingPublications(false);
          } else {
            setNotice(
              job.state === "running"
                ? "Проверка папки публикаций выполняется…"
                : "Проверка папки публикаций в очереди…",
            );
          }
        }
        if (libraryWatchActive && Date.now() >= watchedLibraryUntilRef.current) {
          nextLibraryRefreshAtRef.current = 0;
          setWatchedLibraryUntil(0);
        }
        const currentWatches = watchedRecordsRef.current;
        for (const [key, watch] of Object.entries(currentWatches)) {
          if (Date.now() - watch.startedAt >= 30_000) {
            setWatchedRecords((current) => {
              const next = { ...current };
              delete next[key];
              return next;
            });
            if (isVisibleWatch(watch)) setNotice(errorMessages.autoRefreshTimeout);
            continue;
          }
          let loaded: Detail;
          try {
            loaded = await api<Detail>(`/api/library/records/${watch.recordId}`);
          } catch (error) {
            if (!isCurrent()) return;
            if (isVisibleWatch(watch)) showPollError(error);
            continue;
          }
          if (!isCurrent()) return;
          const pending = detailIsPending(loaded);
          if (
            routeRef.current.recordId === watch.recordId &&
            routeRef.current.sourceId === watch.sourceId
          ) {
            setDetail(loaded);
            setItems([loaded]);
          }
          if (pending && !watch.sawPending) {
            setWatchedRecords((current) => ({ ...current, [key]: { ...watch, sawPending: true } }));
          } else if (!pending && (watch.sawPending || loaded.events.length > 0)) {
            setWatchedRecords((current) => {
              const next = { ...current };
              delete next[key];
              return next;
            });
            if (isVisibleWatch(watch)) setNotice("Данные обновлены после фоновой обработки");
          }
        }
        // Watched track readbacks already refresh detail/items without touching Final.
        const refreshLibraryWatch =
          libraryWatchActive && Date.now() >= nextLibraryRefreshAtRef.current;
        if (refreshLibraryWatch)
          nextLibraryRefreshAtRef.current = Date.now() + LIBRARY_WATCH_REFRESH_INTERVAL_MS;
        if (refreshLibraryWatch || (screen !== "track" && Object.keys(currentWatches).length > 0))
          await loadLibrary(false, true, isCurrent);
      } catch (error) {
        if (isCurrent()) showPollError(error);
      } finally {
        busy = false;
        delay = hadActiveWork ? MIN_POLL_DELAY_MS : Math.min(MAX_POLL_DELAY_MS, delay * 1.5);
        scheduleNext();
      }
    };
    scheduleNext();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [
    scanJobId,
    publicationReconciliationJobId,
    screen,
    recordId,
    sourceId,
    artist,
    album,
    manualActionFilter,
    publicationFilter,
  ]);
  useEffect(() => {
    if ((screen === "settings" || screen === "track") && !settingsDraft && !settingsLoading)
      void loadSettings();
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
    if (screen !== "settings" || storageConfig?.state !== "migrating") return;
    let active = true;
    let busy = false;
    const timer = window.setInterval(async () => {
      if (busy) return;
      busy = true;
      try {
        const config = await getStorageConfig();
        if (active) setStorageConfig(config);
      } catch (error) {
        if (active)
          setSourceRootsError(error instanceof Error ? error.message : errorMessages.browseStorage);
      } finally {
        busy = false;
      }
    }, 2000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [screen, storageConfig?.state]);
  useEffect(() => {
    if (screen !== "workers") return;
    let busy = false;
    const refresh = async () => {
      if (busy || document.visibilityState === "hidden") return;
      busy = true;
      try {
        await loadWorkerQueue(false);
      } finally {
        busy = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2_000);
    return () => window.clearInterval(timer);
  }, [screen]);
  useEffect(() => {
    const onPopState = () =>
      applyRoute(parseRoute(window.location.pathname, window.location.search));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);
  useEffect(() => {
    if (screen !== "track" || !recordId || !items.length || detail) return;
    const item = items.find((entry) => entry.record_id === recordId);
    const source = item
      ? sourceId
        ? (item.sources.find((entry) => entry.source_id === sourceId) ?? catalogSource(item))
        : catalogSource(item)
      : undefined;
    if (item && source) {
      if (sourceId !== source.source_id) {
        setSourceId(source.source_id);
        setEffectiveSourceId(source.source_id);
      }
      void loadTrack(item, source);
    }
    if (!item) {
      const reassignedItem = items.find((entry) =>
        entry.sources.some((entrySource) => entrySource.source_id === sourceId),
      );
      if (reassignedItem !== undefined && reassignedItem.record_id !== recordId) {
        navigate({
          screen: "track",
          recordId: reassignedItem.record_id,
          sourceId,
          artist: artist || albumArtistsFor(reassignedItem, sourceId)[0] || "",
          album: album || albumFor(reassignedItem, sourceId),
        });
      }
    }
  }, [album, artist, detail, items, recordId, screen, sourceId]);
  const tracks = useMemo(
    () =>
      items
        .flatMap((item) => {
          const source = catalogSource(item);
          return source ? [{ item, source }] : [];
        })
        .filter(({ item }) =>
          publicationFilter === "all"
            ? true
            : publicationFilter === "published"
              ? item.publication_state === "current"
              : item.publication_state !== "current",
        )
        .filter(({ item, source }) =>
          `${albumArtistsFor(item, source.source_id).join(" ")} ${albumFor(item, source.source_id)} ${titleFor(item, source.source_id)}`.includes(
            query,
          ),
        ),
    [items, publicationFilter, query],
  );
  const artists =
    screen === "artists"
      ? catalogArtists.filter((name) => name.includes(query))
      : [
          ...new Set(tracks.flatMap(({ item, source }) => albumArtistsFor(item, source.source_id))),
        ].sort(compareNames);
  const albums =
    screen === "albums"
      ? catalogAlbums.filter((entry) => entry.title.includes(query))
      : tracks
          .filter(({ item, source }) => albumArtistsFor(item, source.source_id).includes(artist))
          .reduce<CatalogAlbum[]>((groups, { item, source }) => {
            const key = albumKeyFor(item, source.source_id);
            if (groups.some((group) => group.key === key)) return groups;
            groups.push({
              key,
              title: albumFor(item, source.source_id),
              trackCount: tracks.filter(
                ({ item: candidateItem, source: candidateSource }) =>
                  albumArtistsFor(candidateItem, candidateSource.source_id).includes(artist) &&
                  albumKeyFor(candidateItem, candidateSource.source_id) === key,
              ).length,
              artworkUrl: null,
            });
            return groups;
          }, [])
          .sort(
            (left, right) =>
              compareNames(left.title, right.title) || left.key.localeCompare(right.key),
          );
  const legacyRecordId = album.startsWith("record:")
    ? album.slice("record:".length)
    : album.startsWith("id:record:")
      ? album.slice("id:record:".length)
      : "";
  const legacyAlbumTrack = legacyRecordId
    ? tracks.find(({ item }) => item.record_id === legacyRecordId)
    : undefined;
  const selectedAlbumKey = albumMissing
    ? `album:${UNKNOWN_ALBUM_LABEL}`
    : legacyAlbumTrack
      ? albumKeyFor(legacyAlbumTrack.item, legacyAlbumTrack.source.source_id)
      : album.startsWith("id:")
        ? album.slice("id:".length)
        : album.startsWith("album:")
          ? album
          : album;
  const legacyAlbumTracks = tracks
    .filter(
      ({ item, source }) =>
        albumArtistsFor(item, source.source_id).includes(artist) &&
        albumKeyFor(item, source.source_id) === selectedAlbumKey,
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
  const albumTracks = catalogAlbumTracks
    .filter((track) =>
      `${track.artist_name ?? ""} ${track.album_name ?? ""} ${track.title}`.includes(query),
    )
    .sort((left, right) => {
      const leftNumber = Number.parseInt(left.track_number?.split("/", 1)[0]?.trim() ?? "", 10);
      const rightNumber = Number.parseInt(right.track_number?.split("/", 1)[0]?.trim() ?? "", 10);
      if (!Number.isNaN(leftNumber) && !Number.isNaN(rightNumber) && leftNumber !== rightNumber)
        return leftNumber - rightNumber;
      if (!Number.isNaN(leftNumber) !== !Number.isNaN(rightNumber))
        return Number.isNaN(leftNumber) ? 1 : -1;
      return compareNames(left.title, right.title) || left.record_id.localeCompare(right.record_id);
    });
  const currentTrack =
    legacyAlbumTracks.find(
      ({ item, source }) => item.record_id === recordId && source.source_id === sourceId,
    ) ??
    tracks.find(({ item, source }) => item.record_id === recordId && source.source_id === sourceId);
  const currentTags =
    detail && sourceId ? (layer === "final" ? draft : tagsFor(detail, sourceId, layer)) : {};
  function back() {
    if (screen === "track")
      navigate({ screen: "tracks", artist, artistMissing, album, albumMissing });
    else if (screen === "tracks") navigate({ screen: "albums", artist, artistMissing, album });
    else if (screen === "albums") navigate({ screen: "artists" });
  }
  async function encodingApplied(queued: boolean): Promise<void> {
    if (routeRef.current.recordId !== recordId || routeRef.current.sourceId !== sourceId) return;
    const generation = ++encodingRefreshGeneration.current;
    if (queued) watchRecord(recordId, sourceId);
    const loaded = await api<Detail>(`/api/library/records/${encodeURIComponent(recordId)}`);
    if (
      generation !== encodingRefreshGeneration.current ||
      routeRef.current.recordId !== recordId ||
      routeRef.current.sourceId !== sourceId
    )
      return;
    // Encoding changes source interpretation/history, never the user's Final draft.
    setDetail(loaded);
    setItems([loaded]);
  }
  return {
    encodingApplied,
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
    publicationFilter,
    manualActionFilter,
    manualActionCounts,
    libraryStatus,
    notice,
    loading,
    scanning,
    saving,
    reprocessing,
    refreshingMetadata,
    reconcilingPublications,
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
    workerQueue,
    workerQueueLoading,
    workerQueueError,
    watchedRecords,
    watchedLibraryUntil,
    tracks,
    artists,
    catalogArtistTrackCounts,
    catalogTrackCount,
    albums,
    albumTracks,
    currentTrack,
    currentTags,
    navigate,
    back,
    loadLibrary,
    scan,
    reprocessAll,
    refreshMetadata,
    reconcilePublications,
    reprocessSource,
    saveMetadata,
    retryProvider,
    loadMusicBrainzCandidates: loadMusicBrainzCandidateOptions,
    selectCandidate,
    selectEffectiveSource: selectEffectiveSourceForRecord,
    saveSettings,
    syncGenres,
    createSourceRoot: createConfiguredSourceRoot,
    removeSourceRoot: removeConfiguredSourceRoot,
    browseStorage: loadStorage,
    previewStorageOutput: previewConfiguredStorageOutput,
    moveStorageOutput: moveConfiguredStorageOutput,
    loadWorkerQueue,
    setQuery,
    setPublicationFilter,
    setManualActionFilter,
    setNotice,
    setLayer,
    setDraft,
    setSettingsDraft,
    setGenreSearch,
  };
}
