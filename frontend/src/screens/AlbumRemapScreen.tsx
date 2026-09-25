import type { DragEvent } from "react";
import { useEffect, useMemo, useReducer, useState } from "react";
import {
  type AlbumRemapRelease,
  type AlbumRemapSelector,
  ApiError,
  applyAlbumRemap,
  loadAlbumRemapContext,
  previewAlbumRemap,
  searchAlbumRemapReleases,
} from "../api/client";
import type { Route } from "../types";
import { AlbumRemapMappingWorkspace } from "./AlbumRemapMappingWorkspace";
import { albumRemapMappingReducer, suggestedAssignments } from "./albumRemapState";

type AlbumRemapScreenProps = {
  readonly artist: string;
  readonly album: string;
  readonly artistMissing: boolean;
  readonly albumMissing: boolean;
  readonly onNavigate: (route: Route) => void;
  readonly onNotice: (message: string) => void;
};

const MUSICBRAINZ_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function selectorFor(
  artist: string,
  album: string,
  artistMissing: boolean,
  albumMissing: boolean,
): AlbumRemapSelector {
  const artistName = artistMissing || !artist.trim() ? null : artist;
  const releaseMbid = album.replace(/^id:/, "");
  if (album.startsWith("id:") || MUSICBRAINZ_ID.test(releaseMbid)) {
    return {
      release_mbid: releaseMbid,
      artist_name: null,
      album_name: null,
      artist_missing: false,
      album_missing: false,
    };
  }
  return {
    release_mbid: null,
    artist_name: artistName,
    album_name: albumMissing ? null : album.replace(/^album:/, ""),
    artist_missing: artistMissing,
    album_missing: albumMissing,
  };
}

function keepShortWordsWithNext(value: string): string {
  return value.replace(/(^|\s)([A-Za-zА-Яа-яЁё])\s+/g, "$1$2\u00a0");
}

export function AlbumRemapScreen({
  artist,
  album,
  artistMissing,
  albumMissing,
  onNavigate,
  onNotice,
}: AlbumRemapScreenProps) {
  const selector = useMemo(
    () => selectorFor(artist, album, artistMissing, albumMissing),
    [album, albumMissing, artist, artistMissing],
  );
  const [context, setContext] = useState<Awaited<ReturnType<typeof loadAlbumRemapContext>> | null>(
    null,
  );
  const [contextError, setContextError] = useState("");
  const [contextVersion, setContextVersion] = useState(0);
  const [query, setQuery] = useState("");
  const [releases, setReleases] = useState<readonly AlbumRemapRelease[]>([]);
  const [searching, setSearching] = useState(false);
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof previewAlbumRemap>> | null>(
    null,
  );
  const [previewing, setPreviewing] = useState(false);
  const [workspaceError, setWorkspaceError] = useState("");
  const [staleSnapshot, setStaleSnapshot] = useState(false);
  const [applying, setApplying] = useState(false);
  const [mapping, dispatch] = useReducer(albumRemapMappingReducer, {
    assignments: {},
    selectedSourceId: null,
  });

  useEffect(() => {
    let active = true;
    setContext(null);
    setContextError("");
    setReleases([]);
    setPreview(null);
    setWorkspaceError("");
    setStaleSnapshot(false);
    dispatch({ type: "initialize", assignments: {} });
    void loadAlbumRemapContext(selector)
      .then((nextContext) => {
        if (active) setContext(nextContext);
      })
      .catch((error: unknown) => {
        if (active)
          setContextError(
            error instanceof Error ? error.message : "Не удалось загрузить контекст альбома.",
          );
      });
    return () => {
      active = false;
    };
  }, [contextVersion, selector]);

  const assignments = Object.entries(mapping.assignments).map(([source_id, track_mbid]) => ({
    source_id,
    track_mbid,
  }));
  const unassignedFiles =
    context?.files.filter((file) => mapping.assignments[file.source_id] === undefined) ?? [];

  async function searchReleases(event: { preventDefault(): void }): Promise<void> {
    event.preventDefault();
    const trimmedQuery = query.trim();
    if (!trimmedQuery) return;
    setSearching(true);
    setWorkspaceError("");
    try {
      setReleases((await searchAlbumRemapReleases(trimmedQuery)).items);
    } catch (error) {
      setWorkspaceError(error instanceof Error ? error.message : "Не удалось найти релизы.");
    } finally {
      setSearching(false);
    }
  }

  async function selectRelease(release: AlbumRemapRelease): Promise<void> {
    if (!context) return;
    setPreviewing(true);
    setWorkspaceError("");
    setStaleSnapshot(false);
    try {
      const nextPreview = await previewAlbumRemap({
        selector,
        album_snapshot_token: context.album_snapshot_token,
        release_mbid: release.release_mbid,
      });
      setPreview(nextPreview);
      dispatch({
        type: "initialize",
        assignments: suggestedAssignments(
          nextPreview.suggestions,
          context.files.map((file) => file.source_id),
          nextPreview.tracks
            .filter((track) => track.assignable && !track.data_track)
            .map((track) => track.track_mbid),
        ),
      });
    } catch (error) {
      setWorkspaceError(
        error instanceof Error ? error.message : "Не удалось получить предпросмотр.",
      );
    } finally {
      setPreviewing(false);
    }
  }

  function setFileAssignment(sourceId: string, trackMbid: string): void {
    dispatch(trackMbid ? { type: "assign", sourceId, trackMbid } : { type: "unassign", sourceId });
  }

  async function apply(): Promise<void> {
    if (!context || !preview || context.files.length === 0 || assignments.length === 0) return;
    setApplying(true);
    setWorkspaceError("");
    setStaleSnapshot(false);
    try {
      const result = await applyAlbumRemap({
        selector,
        album_snapshot_token: context.album_snapshot_token,
        release_mbid: preview.release.release_mbid,
        release_snapshot_token: preview.release_snapshot_token,
        assignments,
        unmatched_source_ids: unassignedFiles.map((file) => file.source_id),
      });
      onNotice(
        result.publication_refresh_queued
          ? "Сопоставление применено; обновление публикации поставлено в очередь."
          : "Сопоставление релиза применено.",
      );
      onNavigate({
        screen: "tracks",
        album: `id:${preview.release.release_mbid}`,
        artist: "",
        artistMissing: false,
        albumMissing: false,
      });
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setWorkspaceError(
          "Контекст альбома устарел. Обновите контекст альбома и повторите сопоставление.",
        );
        setStaleSnapshot(true);
      } else {
        setWorkspaceError(
          error instanceof Error ? error.message : "Не удалось применить сопоставление.",
        );
      }
    } finally {
      setApplying(false);
    }
  }

  function beginDrag(event: DragEvent<HTMLButtonElement>, sourceId: string): void {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", sourceId);
  }

  if (contextError)
    return (
      <section className="album-remap-screen" aria-labelledby="album-remap-heading">
        <h2 id="album-remap-heading">Смена релиза</h2>
        <div className="remap-error" role="alert">
          {contextError}
          <button
            type="button"
            className="secondary"
            onClick={() => setContextVersion((value) => value + 1)}
          >
            Повторить загрузку
          </button>
        </div>
      </section>
    );
  if (!context)
    return (
      <div className="empty-state" role="status">
        Подготавливаем контекст альбома…
      </div>
    );

  const sourceTags = context.files[0]?.tags ?? {};
  const sourceAlbum =
    sourceTags.ALBUM ||
    selector.album_name ||
    (selector.release_mbid ? "Релиз MusicBrainz" : "Альбом без названия");
  const sourceArtist =
    sourceTags.ALBUMARTIST || sourceTags.ARTIST || selector.artist_name || "Исполнитель не указан";

  return (
    <section className="album-remap-screen" aria-labelledby="album-remap-heading">
      <div className="remap-search-pane">
        <p className="eyebrow">MusicBrainz</p>
        <h2 id="album-remap-heading">Поиск релиза</h2>
        <div className="remap-source-identity">
          <div className="remap-source-cover" aria-hidden="true">
            <span>LP</span>
          </div>
          <div>
            <span className="remap-source-label">Исходный альбом</span>
            <strong className="remap-source-title">{keepShortWordsWithNext(sourceAlbum)}</strong>
            <p className="remap-source-meta">
              {sourceArtist} · Файлов: {context.files.length}
            </p>
          </div>
        </div>
        <form className="remap-search-form" onSubmit={searchReleases}>
          <label htmlFor="album-remap-query">Поиск релиза MusicBrainz</label>
          <div>
            <input
              id="album-remap-query"
              value={query}
              disabled={applying}
              onChange={(event) => setQuery(event.target.value)}
            />
            <button type="submit" className="primary" disabled={searching || applying}>
              {searching ? "Ищем…" : "Найти релиз"}
            </button>
          </div>
        </form>
        {searching ? (
          <p className="remap-progress" role="status">
            Ищем релизы…
          </p>
        ) : null}
        {releases.length > 0 ? (
          <ul className="remap-release-results">
            {releases.map((release) => {
              const details = [release.artist_credit, release.date, release.country, release.status]
                .filter(Boolean)
                .join(" · ");
              const counts = [
                release.medium_count === undefined || release.medium_count === null
                  ? null
                  : `${release.medium_count} носителя`,
                release.track_count === undefined || release.track_count === null
                  ? null
                  : `${release.track_count} трека`,
              ]
                .filter(Boolean)
                .join(" · ");
              return (
                <li key={release.release_mbid}>
                  <button
                    type="button"
                    className="remap-release-card"
                    disabled={previewing || applying}
                    aria-label={`Выбрать релиз ${release.title}`}
                    onClick={() => void selectRelease(release)}
                  >
                    <strong>{release.title}</strong>
                    {details ? <span>{details}</span> : null}
                    {counts ? <small>{counts}</small> : null}
                  </button>
                </li>
              );
            })}
          </ul>
        ) : null}
      </div>
      <div className="remap-detail-pane">
        {workspaceError ? (
          <div className="remap-error" role="alert">
            <span>{workspaceError}</span>
            {staleSnapshot ? (
              <button
                type="button"
                className="secondary"
                onClick={() => setContextVersion((value) => value + 1)}
              >
                Обновить контекст альбома
              </button>
            ) : null}
          </div>
        ) : null}
        {preview ? (
          <AlbumRemapMappingWorkspace
            context={context}
            preview={preview}
            mapping={mapping}
            applying={applying}
            onAssign={setFileAssignment}
            onApply={() => void apply()}
            onDragStart={beginDrag}
            onSelect={(sourceId) => dispatch({ type: "select", sourceId })}
          />
        ) : (
          <div className="remap-preview-placeholder" role="status">
            {previewing
              ? "Формируем предпросмотр сопоставления…"
              : "Выберите релиз, чтобы открыть сопоставление исходных файлов."}
          </div>
        )}
      </div>
    </section>
  );
}
