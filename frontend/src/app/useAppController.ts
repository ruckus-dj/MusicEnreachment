import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
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
  GenreCatalog,
  Layer,
  ProviderName,
  Route,
  RuntimeSettings,
  RuntimeSettingsDraft,
  Screen,
  Source,
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
  settingsDraft: RuntimeSettingsDraft | null;
  settingsLoading: boolean;
  settingsSaving: boolean;
  genreCatalog: GenreCatalog | null;
  genreSearch: string;
  genreLoading: boolean;
  genreSyncing: boolean;
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
  saveMetadata: () => Promise<boolean>;
  retryProvider: (provider: ProviderName) => Promise<void>;
  overrideRelease: (releaseMbid: string) => Promise<void>;
  selectCandidate: (selection: string) => Promise<void>;
  saveSettings: () => Promise<void>;
  syncGenres: () => Promise<void>;
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
  const [saving, setSaving] = useState(false);
  const [reprocessing, setReprocessing] = useState(false);
  const [settingsDraft, setSettingsDraft] = useState<RuntimeSettingsDraft | null>(null);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [genreCatalog, setGenreCatalog] = useState<GenreCatalog | null>(null);
  const [genreSearch, setGenreSearch] = useState("");
  const [genreLoading, setGenreLoading] = useState(false);
  const [genreSyncing, setGenreSyncing] = useState(false);
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
    setNotice("Восстанавливаем недоимпортированные записи…");
    try {
      const result = await api<{
        readonly added: number;
        readonly changed: number;
        readonly moved: number;
        readonly removed: number;
        readonly unchanged: number;
        readonly queued_jobs: number;
      }>("/api/reconciliation/scan", { method: "POST" });
      setNotice(
        `Новых: ${result.added}; изменённых: ${result.changed}; перемещённых: ${result.moved}; ` +
          `удалённых: ${result.removed}; в очереди: ${result.queued_jobs}`,
      );
      await loadLibrary();
      if (result.queued_jobs > 0) watchLibrary();
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Восстановление не удалось");
    } finally {
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
  }, []);
  useEffect(() => {
    if (screen === "settings" && !settingsDraft && !settingsLoading) void loadSettings();
  }, [screen, settingsDraft, settingsLoading]);
  useEffect(() => {
    if (screen === "settings" && !genreCatalog && !genreLoading) void loadGenres();
  }, [screen, genreCatalog, genreLoading]);
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
    settingsDraft,
    settingsLoading,
    settingsSaving,
    genreCatalog,
    genreSearch,
    genreLoading,
    genreSyncing,
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
    saveMetadata,
    retryProvider,
    overrideRelease,
    selectCandidate,
    saveSettings,
    syncGenres,
    setQuery,
    setNotice,
    setLayer,
    setDraft,
    setSettingsDraft,
    setGenreSearch,
  };
}
