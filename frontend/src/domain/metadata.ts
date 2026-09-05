import type { Detail, Layer, Revision, Summary, Tags, WorkflowStatus } from "../types";

export const TAG_FIELDS = [
  "TITLE",
  "ARTIST",
  "ALBUM",
  "ALBUMARTIST",
  "DATE",
  "ORIGINALDATE",
  "GENRE",
  "TRACKNUMBER",
  "TRACKTOTAL",
  "DISCNUMBER",
  "DISCTOTAL",
  "MUSICBRAINZ_TRACKID",
  "MUSICBRAINZ_ALBUMID",
  "MUSICBRAINZ_RELEASEGROUPID",
  "ISRC",
] as const;

export const UNKNOWN_ARTIST_LABEL = "Неизвестный исполнитель";
export const UNKNOWN_ALBUM_LABEL = "Без альбома";

const catalogCollator = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: "base",
});

export function tagsFor(item: Summary, sourceId: string, layer: Layer): Tags {
  const revision = item.metadata_revisions
    ?.slice()
    .reverse()
    .find((entry) => entry.source_id === sourceId && entry.layer === layer);
  if (revision) return revision.tags;
  if (layer !== "original") return {};
  return Object.fromEntries(
    (item.sources.find((source) => source.source_id === sourceId)?.tag_observations ?? []).map(
      (tag) => [tag.name, tag.value],
    ),
  );
}

export function latestRevision(
  item: Summary,
  sourceId: string,
  layer: Layer,
): Revision | undefined {
  return item.metadata_revisions
    ?.slice()
    .reverse()
    .find((entry) => entry.source_id === sourceId && entry.layer === layer);
}

export function titleFor(item: Summary, sourceId: string): string {
  return (
    tagsFor(item, sourceId, "final").TITLE ||
    tagsFor(item, sourceId, "original").TITLE ||
    item.sources
      .find((source) => source.source_id === sourceId)
      ?.path.split("/")
      .at(-1) ||
    "Без названия"
  );
}

export function artistFor(item: Summary, sourceId: string): string {
  return (
    tagsFor(item, sourceId, "final").ARTIST ||
    tagsFor(item, sourceId, "original").ARTIST ||
    UNKNOWN_ARTIST_LABEL
  );
}

export function albumArtistsFor(item: Summary, sourceId: string): string[] {
  const albumArtist =
    tagsFor(item, sourceId, "final").ALBUMARTIST ||
    tagsFor(item, sourceId, "original").ALBUMARTIST ||
    "";
  const artists = [
    ...new Set(
      albumArtist
        .split(";")
        .map((name) => name.trim())
        .filter(Boolean),
    ),
  ];
  return artists.length > 0 ? artists : [artistFor(item, sourceId)];
}

export function albumFor(item: Summary, sourceId: string): string {
  return (
    tagsFor(item, sourceId, "final").ALBUM ||
    tagsFor(item, sourceId, "original").ALBUM ||
    UNKNOWN_ALBUM_LABEL
  );
}

export function releaseMbidFor(item: Summary): string | null {
  const releaseMbid = item.musicbrainz_release_id?.trim();
  return releaseMbid ? releaseMbid : null;
}

export function albumKeyFor(item: Summary, sourceId: string): string {
  return releaseMbidFor(item) ?? `album:${albumFor(item, sourceId).trim()}`;
}

export function trackNumberFor(item: Summary, sourceId: string): number | null {
  const value =
    tagsFor(item, sourceId, "final").TRACKNUMBER || tagsFor(item, sourceId, "original").TRACKNUMBER;
  const parsed = Number.parseInt(value?.split("/", 1)[0]?.trim() ?? "", 10);
  return Number.isNaN(parsed) ? null : parsed;
}

export function trackNumberLabelFor(item: Summary, sourceId: string): string {
  const trackNumber = trackNumberFor(item, sourceId);
  return trackNumber === null ? "—" : String(trackNumber).padStart(2, "0");
}

export function compareNames(left: string, right: string): number {
  return catalogCollator.compare(left, right) || left.localeCompare(right);
}

export function workflowStatus(detail: Detail): WorkflowStatus {
  const latest = detail.events.at(-1);
  if (detail.states.source === "disappeared")
    return {
      tone: "error",
      label: "Исходный файл не найден",
      detail: "Исходные теги и анализ сохранены, но повторная обработка недоступна.",
    };
  if (detail.destination_conflict)
    return {
      tone: "error",
      label: "Конфликт destination",
      detail: `Папка уже существует: ${detail.destination_conflict.path}`,
    };
  if (
    detail.states.processing === "retrying" ||
    detail.states.processing === "blocked_infrastructure" ||
    detail.states.processing === "quarantined" ||
    detail.states.source === "invalid_audio" ||
    detail.states.publication === "failed"
  )
    return {
      tone: "error",
      label: "Требуется внимание",
      detail: latest?.reason ?? "Последняя операция не завершилась успешно",
    };
  if (detail.states.processing === "analyzing")
    return {
      tone: "pending",
      label: "Идёт анализ провайдеров",
      detail:
        "Исходные и текущие опубликованные теги сохранены. Финальная ревизия появится после анализа.",
    };
  if (detail.states.processing === "publishing" || detail.states.publication === "stale")
    return {
      tone: "pending",
      label: "Публикация ожидает",
      detail:
        "Финальная ревизия создана и будет записана в управляемую медиакопию очередью публикации.",
    };
  const hasCurrentPublication = detail.publications.some(
    (item) => item.state === "current" && isPublicationHash(item.sha256),
  );
  if (hasCurrentPublication)
    return {
      tone: "ready",
      label: "Публикация актуальна",
      detail: "Текущий аудиофайл соответствует опубликованной финальной ревизии.",
    };
  return {
    tone: "pending",
    label: "Готово к публикации",
    detail: "Изменения сохранены как история и ожидают публикации.",
  };
}

export function isPublicationHash(value: string | undefined): value is string {
  return /^[0-9a-f]{64}$/i.test(value ?? "") && !/^0+$/i.test(value ?? "");
}

export function detailIsPending(detail: Detail): boolean {
  return (
    detail.states.processing === "analyzing" ||
    detail.states.processing === "publishing" ||
    detail.states.processing === "retrying" ||
    detail.states.publication === "stale" ||
    ["queued", "analyzing", "publishing"].includes(detail.events.at(-1)?.state ?? "")
  );
}
