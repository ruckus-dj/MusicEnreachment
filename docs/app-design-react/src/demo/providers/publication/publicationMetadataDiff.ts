import type {
  MetadataChange,
  MetadataChangeGroup,
} from "../../../ui/features/publication/PublicationInspector";
import {
  type CatalogArtist,
  effectivePublicationTags,
  type OriginalRelease,
  type OriginalTrack,
} from "../library/catalogData";
import { navidromeFields } from "../library/metadata/registry";

export type { MetadataChange, MetadataChangeGroup };

const groupLabels: Record<string, string> = {
  "recording artists": "Исполнители композиции",
  "release artists": "Исполнители издания",
  roles: "Роли и участники",
  works: "Произведения",
  movement: "Части произведения",
  release: "Издание",
  recording: "Композиция",
  track: "Позиция",
  audio: "Аудио",
};

export function metadataTagDiff(
  before: Record<string, string[]>,
  after: Record<string, string[]>,
): MetadataChangeGroup[] {
  const knownTags = navidromeFields.map((field) => field.tag);
  const extraTags = [
    ...new Set([...Object.keys(before), ...Object.keys(after)]),
  ]
    .filter((tag) => !knownTags.includes(tag))
    .sort((left, right) => left.localeCompare(right));
  const grouped = new Map<string, MetadataChange[]>();
  for (const tag of [...knownTags, ...extraTags]) {
    if (JSON.stringify(before[tag] ?? []) === JSON.stringify(after[tag] ?? []))
      continue;
    const field = navidromeFields.find((candidate) => candidate.tag === tag);
    const group = field?.group ?? "additional";
    const changes = grouped.get(group) ?? [];
    changes.push({
      key: tag,
      label: field?.label ?? tag,
      before: before[tag] ?? [],
      after: after[tag] ?? [],
    });
    grouped.set(group, changes);
  }
  return [...grouped].map(([key, changes]) => ({
    key,
    label:
      groupLabels[key] ?? (key === "additional" ? "Дополнительные теги" : key),
    changes,
  }));
}

export function publicationMetadataDiff(
  release: OriginalRelease,
  track: OriginalTrack,
  artists?: CatalogArtist[],
): MetadataChangeGroup[] {
  if (!track.published) return [];
  const beforeTags = track.metadataBaseline ?? track.published.tags;
  const afterTags = effectivePublicationTags(release, track, artists);
  const beforeExtras = track.metadataBaselineExtras ?? {
    coverHash: track.published.coverHash,
    lyrics: track.published.lyrics,
  };
  return metadataSnapshotDiff(beforeTags, afterTags, beforeExtras, {
    coverHash: track.coverHash ?? null,
    lyrics: track.lyrics ?? null,
  });
}

export function metadataSnapshotDiff(
  beforeTags: Record<string, string[]>,
  afterTags: Record<string, string[]>,
  beforeExtras: { coverHash: string | null; lyrics: string | null },
  afterExtras: { coverHash: string | null; lyrics: string | null },
): MetadataChangeGroup[] {
  const groups = metadataTagDiff(beforeTags, afterTags);
  const extraChanges: MetadataChange[] = [];
  if ((beforeExtras.coverHash ?? null) !== (afterExtras.coverHash ?? null))
    extraChanges.push({
      key: "cover",
      label: "Встроенная обложка",
      before: beforeExtras.coverHash
        ? [afterExtras.coverHash ? "Есть · текущая" : "Есть"]
        : [],
      after: afterExtras.coverHash
        ? [beforeExtras.coverHash ? "Есть · новая" : "Есть"]
        : [],
    });
  if ((beforeExtras.lyrics ?? null) !== (afterExtras.lyrics ?? null))
    extraChanges.push({
      key: "lyrics",
      label: "Текст песни",
      before: beforeExtras.lyrics ? [beforeExtras.lyrics] : [],
      after: afterExtras.lyrics ? [afterExtras.lyrics] : [],
    });
  const cover = extraChanges.filter((change) => change.key === "cover");
  const lyrics = extraChanges.filter((change) => change.key === "lyrics");
  if (cover.length)
    groups.push({ key: "artwork", label: "Обложка", changes: cover });
  if (lyrics.length)
    groups.push({ key: "lyrics", label: "Текст песни", changes: lyrics });
  return groups;
}
