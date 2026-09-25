import type { DragEvent } from "react";
import type {
  AlbumRemapContext,
  AlbumRemapFile,
  AlbumRemapPreview,
  AlbumRemapTrack,
} from "../api/client";
import type { AlbumRemapMappingState } from "./albumRemapState";

type MappingWorkspaceProps = {
  readonly context: AlbumRemapContext;
  readonly preview: AlbumRemapPreview;
  readonly mapping: AlbumRemapMappingState;
  readonly applying: boolean;
  readonly onAssign: (sourceId: string, trackMbid: string) => void;
  readonly onApply: () => void;
  readonly onDragStart: (event: DragEvent<HTMLButtonElement>, sourceId: string) => void;
  readonly onSelect: (sourceId: string) => void;
};

function fileName(path: string): string {
  return path.split("/").at(-1) || path;
}

function formatDuration(seconds: number | null): string {
  if (seconds === null) return "длительность не определена";
  const rounded = Math.round(seconds);
  return `${Math.floor(rounded / 60)}:${String(rounded % 60).padStart(2, "0")}`;
}

function trackLabel(track: AlbumRemapTrack): string {
  return `${String(track.medium_position).padStart(2, "0")}.${String(track.track_position).padStart(2, "0")}`;
}

function FileTile({
  file,
  assignedTrackMbid,
  selected,
  disabled,
  tracks,
  onAssign,
  onDragStart,
  onSelect,
}: {
  readonly file: AlbumRemapFile;
  readonly assignedTrackMbid: string | undefined;
  readonly selected: boolean;
  readonly disabled: boolean;
  readonly tracks: readonly AlbumRemapTrack[];
  readonly onAssign: (sourceId: string, trackMbid: string) => void;
  readonly onDragStart: (event: DragEvent<HTMLButtonElement>, sourceId: string) => void;
  readonly onSelect: (sourceId: string) => void;
}) {
  const name = fileName(file.path);
  return (
    <div className={`remap-file ${selected ? "selected" : ""}`}>
      <button
        type="button"
        className="remap-file-tile"
        draggable={!disabled}
        disabled={disabled}
        aria-label={`Выбрать файл ${name}`}
        aria-pressed={selected}
        onClick={() => onSelect(file.source_id)}
        onDragStart={(event) => onDragStart(event, file.source_id)}
      >
        <strong>{name}</strong>
        <small title={file.path}>{file.path}</small>
        <small className="remap-file-meta">
          {formatDuration(file.duration_seconds)} · ревизия {file.source_metadata_revision}
        </small>
        {selected ? <small className="remap-file-state">Выбран для назначения</small> : null}
      </button>
      <label className="remap-file-select">
        <span className="visually-hidden">Назначение {name}</span>
        <select
          aria-label={`Назначение ${name}`}
          disabled={disabled}
          value={assignedTrackMbid ?? ""}
          onChange={(event) => onAssign(file.source_id, event.target.value)}
        >
          <option value="">Не назначать</option>
          {tracks.map((track) => (
            <option key={track.track_mbid} value={track.track_mbid}>
              {trackLabel(track)} {track.title}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function TrackSlot({
  track,
  file,
  mapping,
  sourceIds,
  tracks,
  disabled,
  onAssign,
  onDragStart,
  onSelect,
}: {
  readonly track: AlbumRemapTrack;
  readonly file: AlbumRemapFile | undefined;
  readonly mapping: AlbumRemapMappingState;
  readonly sourceIds: ReadonlySet<string>;
  readonly tracks: readonly AlbumRemapTrack[];
  readonly disabled: boolean;
  readonly onAssign: (sourceId: string, trackMbid: string) => void;
  readonly onDragStart: (event: DragEvent<HTMLButtonElement>, sourceId: string) => void;
  readonly onSelect: (sourceId: string) => void;
}) {
  const assignable = track.assignable && !track.data_track;

  function handleDrop(event: DragEvent<HTMLElement>): void {
    event.preventDefault();
    const sourceId = event.dataTransfer.getData("text/plain");
    if (!disabled && sourceIds.has(sourceId)) onAssign(sourceId, track.track_mbid);
  }

  return (
    <li
      className={`remap-track-slot ${assignable ? "" : "unavailable"}`}
      aria-label={`Сопоставление с треком ${track.title}`}
      onDragOver={assignable && !disabled ? (event) => event.preventDefault() : undefined}
      onDrop={assignable && !disabled ? handleDrop : undefined}
    >
      <div className="remap-track-heading">
        <span>{trackLabel(track)}</span>
        <div>
          <h3>{track.title}</h3>
          <p>
            {track.recording_title} · {track.artist_credit}
          </p>
        </div>
      </div>
      {assignable ? (
        <>
          {file ? (
            <FileTile
              file={file}
              assignedTrackMbid={track.track_mbid}
              selected={mapping.selectedSourceId === file.source_id}
              disabled={disabled}
              tracks={tracks}
              onAssign={onAssign}
              onDragStart={onDragStart}
              onSelect={onSelect}
            />
          ) : (
            <p className="remap-empty-slot">Перетащите файл или выберите его слева.</p>
          )}
          <button
            type="button"
            className="secondary"
            aria-label={`Назначить файл треку ${trackLabel(track)} ${track.title}`}
            disabled={disabled || !mapping.selectedSourceId}
            onClick={() => {
              if (mapping.selectedSourceId) onAssign(mapping.selectedSourceId, track.track_mbid);
            }}
          >
            Назначить
          </button>
        </>
      ) : (
        <>
          <p className="remap-slot-state">Служебный трек: не назначается</p>
          <button
            type="button"
            className="secondary"
            aria-label={`Назначить файл треку ${trackLabel(track)} ${track.title}`}
            disabled
          >
            Назначить
          </button>
        </>
      )}
    </li>
  );
}

export function AlbumRemapMappingWorkspace({
  context,
  preview,
  mapping,
  applying,
  onAssign,
  onApply,
  onDragStart,
  onSelect,
}: MappingWorkspaceProps) {
  const tracks = preview.tracks.filter((track) => track.assignable && !track.data_track);
  const filesById = new Map(context.files.map((file) => [file.source_id, file]));
  const sourceIds = new Set(context.files.map((file) => file.source_id));
  const unassignedFiles = context.files.filter(
    (file) => mapping.assignments[file.source_id] === undefined,
  );
  const assignmentCount = Object.keys(mapping.assignments).length;
  const hasFiles = context.files.length > 0;
  const applySummary = !hasFiles
    ? "В контексте альбома нет исходных файлов для сопоставления."
    : assignmentCount === 0
      ? "Назначьте хотя бы один исходный файл, чтобы применить сопоставление."
      : `Назначено ${assignmentCount}; не назначено ${unassignedFiles.length}.`;
  return (
    <section className="remap-mapping" aria-labelledby="remap-mapping-heading">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Целевой релиз · {preview.release.title}</p>
          <h2 id="remap-mapping-heading">Сопоставление треков</h2>
        </div>
        <div className="remap-heading-actions">
          <span className="badge">Назначено: {assignmentCount}</span>
          <p role="status">{applySummary}</p>
          <button
            type="button"
            className="primary"
            disabled={applying || !hasFiles || assignmentCount === 0}
            onClick={onApply}
          >
            {applying ? "Применяем…" : `Применить сопоставление (${assignmentCount})`}
          </button>
        </div>
      </div>
      <div className="remap-workspace">
        <aside className="remap-source-pane" aria-labelledby="remap-source-heading">
          <h3 id="remap-source-heading">Исходные файлы</h3>
          <div className="remap-unassigned-tray">
            <div className="remap-tray-header">
              <strong>Не назначено</strong>
              <span>{unassignedFiles.length}</span>
            </div>
            {hasFiles && unassignedFiles.length === 0 ? <p>Все файлы назначены.</p> : null}
            {unassignedFiles.map((file) => (
              <FileTile
                key={file.source_id}
                file={file}
                assignedTrackMbid={mapping.assignments[file.source_id]}
                selected={mapping.selectedSourceId === file.source_id}
                disabled={applying}
                tracks={tracks}
                onAssign={onAssign}
                onDragStart={onDragStart}
                onSelect={onSelect}
              />
            ))}
          </div>
        </aside>
        <ol className="remap-track-slots" aria-label={`Треки релиза ${preview.release.title}`}>
          {preview.tracks.map((track) => {
            const sourceId = Object.entries(mapping.assignments).find(
              ([, assignedTrackMbid]) => assignedTrackMbid === track.track_mbid,
            )?.[0];
            const assignedFile = sourceId ? filesById.get(sourceId) : undefined;
            return (
              <TrackSlot
                key={track.track_mbid}
                track={track}
                file={assignedFile}
                mapping={mapping}
                sourceIds={sourceIds}
                tracks={tracks}
                disabled={applying}
                onAssign={onAssign}
                onDragStart={onDragStart}
                onSelect={onSelect}
              />
            );
          })}
        </ol>
      </div>
    </section>
  );
}
