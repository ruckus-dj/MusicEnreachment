import type {
  GenreCatalogItem,
  LocalProviderEditionInput,
  LocalProviderItem,
  LocalProviderWrite,
} from "../../api/generated/client.schemas";
import type { OwnCatalogArtistDraft } from "../../ui/features/local-catalog/OwnCatalogArtistEditor";
import type { OwnCatalogRecordingDraft } from "../../ui/features/local-catalog/OwnCatalogRecordingEditor";
import type { OwnCatalogMessage } from "../../ui/features/local-catalog/ownCatalogMessages";
import {
  formatLocalDuration,
  type LocalRecordingCreditSelection,
  type LocalReleaseDraft,
  type LocalReleaseRecording,
  parseLocalDuration,
} from "../../ui/features/local-release/localReleaseFormModel";
import type { LocalReleaseFormInitialValues } from "../../ui/features/local-release/useLocalReleaseForm";

export const OWN_CATALOG_PAGE_SIZE = 50;

export const ownCatalogTypes = ["releases", "recordings", "artists"] as const;

export type OwnCatalogType = (typeof ownCatalogTypes)[number];

export const ownCatalogPageCount = (total: number) =>
  Math.max(1, Math.ceil(total / OWN_CATALOG_PAGE_SIZE));

export function ownCatalogProviderCodeLabel(code: string) {
  if (code === "local") return "Local";
  if (code === "musicbrainz") return "MusicBrainz";
  return code;
}

export function ownCatalogReleaseTitle(item: LocalProviderItem) {
  return item.title || item.release_group_title || "";
}

export function ownCatalogRecordingTitle(item: LocalProviderItem) {
  return item.title || "";
}

export function ownCatalogTitle(type: OwnCatalogType, item: LocalProviderItem) {
  if (type === "artists") return item.name || item.sort_name || "";
  if (type === "recordings") return ownCatalogRecordingTitle(item);
  return ownCatalogReleaseTitle(item);
}

export function ownCatalogGenreNames(item: LocalProviderItem) {
  return (item.genres ?? []).map((genre) => genre.name).filter(Boolean);
}

export function ownCatalogArtistCredit(item: LocalProviderItem) {
  if (item.artist_credit) return item.artist_credit;
  const credits = (item.credits ?? [])
    .map(
      (credit) =>
        `${credit.credit_name || credit.artist_name}${credit.join_phrase}`,
    )
    .join("")
    .trim();
  return credits || item.edition?.artist_credit || "";
}

export function ownCatalogRowDetails(
  type: OwnCatalogType,
  item: LocalProviderItem,
  m: (
    key: OwnCatalogMessage,
    values?: Record<string, string | number>,
  ) => string,
) {
  if (type === "artists") {
    return [ownCatalogGenreNames(item).join(", "), item.sort_name]
      .filter(Boolean)
      .join(" · ");
  }
  const artistCredit = ownCatalogArtistCredit(item);
  if (type === "recordings") {
    return [
      artistCredit ? m("artistCredit", { artists: artistCredit }) : "",
      formatLocalDuration(item.duration_ms),
      ownCatalogGenreNames(item).join(", "),
    ]
      .filter(Boolean)
      .join(" · ");
  }
  const edition = item.edition;
  const label = edition
    ? [edition.label, edition.catalog_number].filter(Boolean).join(" / ")
    : "";
  return [
    artistCredit ? m("artistCredit", { artists: artistCredit }) : "",
    edition ? m("trackCount", { count: edition.track_count }) : "",
    edition?.disambiguation,
    edition?.release_date,
    edition?.country,
    edition?.format,
    label,
  ]
    .filter(Boolean)
    .join(" · ");
}

export function releaseRecordToFormValues(
  item: LocalProviderItem,
): LocalReleaseFormInitialValues {
  const groupTitle = item.release_group_title || item.title || "";
  const editionTitle =
    item.title && item.title !== groupTitle ? item.title : "";
  return {
    title: groupTitle,
    edition: editionTitle,
    artistIds: (item.credits ?? []).map((credit) => credit.artist_id),
    primaryType: item.release_group_primary_type ?? "",
    secondaryTypes: item.release_group_secondary_types ?? [],
    language: item.language ?? "",
    script: item.script ?? "",
    editionDetails: {
      release_date: item.edition?.release_date,
      country: item.edition?.country,
      format: item.edition?.format,
      label: item.edition?.label,
      catalog_number: item.edition?.catalog_number,
      disambiguation: item.edition?.disambiguation,
    },
    genres: (item.genres ?? []).map((genre) => ({
      id: genre.id,
      name: genre.name,
      source_name: genre.source_name,
    })),
    tracks: (item.tracks ?? []).map((track) => ({
      id: track.id,
      recordingId: track.recording_id,
      titleOverride: track.title_override ?? "",
      durationOverrideMs: track.duration_override_ms,
      durationInput: formatLocalDuration(track.duration_override_ms),
    })),
  };
}

/**
 * Track rows only carry the linked recording summary. Hydrate the shared release
 * form with one unique roster entry per linked recording so existing positions
 * keep their title, duration and provider instead of rendering placeholders.
 */
export function releaseRecordRecordings(
  item: LocalProviderItem,
): LocalReleaseRecording[] {
  const seen = new Set<string>();
  const recordings: LocalReleaseRecording[] = [];
  for (const track of item.tracks ?? []) {
    if (!track.recording_id || seen.has(track.recording_id)) continue;
    seen.add(track.recording_id);
    recordings.push({
      id: track.recording_id,
      key: track.provider_id,
      provider: ownCatalogProviderCodeLabel(track.provider_code),
      title: track.recording_title,
      duration_ms: track.recording_duration_ms,
    });
  }
  return recordings;
}

export function splitCatalogList(value: string) {
  return value
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

type GenreInput = { id?: string; name: string; source_name?: string };

function genreWrite(genres: readonly GenreInput[] | undefined) {
  return (genres ?? []).map<GenreCatalogItem>((genre) => ({
    id: genre.id ?? "",
    name: genre.name,
    source_name: genre.source_name || genre.name,
  }));
}

function creditName(
  existing: LocalProviderItem | undefined,
  artistId: string,
  known: ReadonlyMap<string, string>,
) {
  return (
    existing?.credits?.find((credit) => credit.artist_id === artistId)
      ?.credit_name ||
    known.get(artistId) ||
    ""
  );
}

function releaseCredits(
  existing: LocalProviderItem | undefined,
  artistIds: readonly string[],
  known: ReadonlyMap<string, string>,
) {
  const unchanged =
    (existing?.credits?.length ?? 0) === artistIds.length &&
    (existing?.credits ?? []).every(
      (credit, index) => credit.artist_id === artistIds[index],
    );
  if (unchanged) {
    return (existing?.credits ?? []).map((credit) => ({
      artist_id: credit.artist_id,
      credit_name: credit.credit_name,
      join_phrase: credit.join_phrase,
    }));
  }
  return artistIds.map((artistId, index) => ({
    artist_id: artistId,
    credit_name: creditName(existing, artistId, known),
    join_phrase: retainedJoinPhrase(existing, artistId, artistIds[index + 1]),
  }));
}

function retainedJoinPhrase(
  existing: LocalProviderItem | undefined,
  artistId: string,
  nextArtistId: string | undefined,
) {
  const credits = existing?.credits ?? [];
  const position = credits.findIndex(
    (credit, index) =>
      credit.artist_id === artistId &&
      credits[index + 1]?.artist_id === nextArtistId,
  );
  return position >= 0
    ? credits[position].join_phrase
    : nextArtistId
      ? ", "
      : "";
}

function releaseEdition(
  draft: LocalReleaseDraft,
): LocalProviderEditionInput | undefined {
  const source = draft.edition;
  if (!source) return undefined;
  const edition: LocalProviderEditionInput = {
    release_date: source.release_date?.trim() || undefined,
    country: source.country?.trim() || undefined,
    format: source.format?.trim() || undefined,
    label: source.label?.trim() || undefined,
    catalog_number: source.catalog_number?.trim() || undefined,
    disambiguation: source.disambiguation?.trim() || undefined,
  };
  return Object.values(edition).some(Boolean) ? edition : undefined;
}

export function releaseDraftToWrite(
  existing: LocalProviderItem | undefined,
  draft: LocalReleaseDraft,
  knownArtists: ReadonlyMap<string, string>,
): LocalProviderWrite {
  return {
    title: draft.title,
    release_group_title: draft.release_group_title,
    release_group_primary_type: draft.primaryType?.trim() || undefined,
    release_group_secondary_types: draft.secondaryTypes ?? [],
    language: draft.language?.trim() || undefined,
    script: draft.script?.trim() || undefined,
    genres: genreWrite(draft.genres),
    credits: releaseCredits(existing, draft.artistIds, knownArtists),
    edition: releaseEdition(draft),
    tracks: draft.tracks.map((track) => ({
      ...(track.id ? { id: track.id } : {}),
      recording_id: track.recordingId,
      ...(track.titleOverride.trim()
        ? { title_override: track.titleOverride.trim() }
        : {}),
      ...(typeof track.durationOverrideMs === "number"
        ? { duration_override_ms: track.durationOverrideMs }
        : {}),
    })),
  };
}

export type ArtistFormDraft = OwnCatalogArtistDraft;

export function artistRecordToDraft(item: LocalProviderItem): ArtistFormDraft {
  return {
    name: item.name ?? "",
    sortName: item.sort_name ?? "",
    tags: (item.tags ?? []).join(", "),
    genres: (item.genres ?? []).map((genre) => ({
      id: genre.id,
      name: genre.name,
      source_name: genre.source_name,
    })),
  };
}

export function artistDraftToWrite(draft: ArtistFormDraft): LocalProviderWrite {
  return {
    name: draft.name.trim(),
    sort_name: draft.sortName.trim() || undefined,
    genres: genreWrite(draft.genres),
    tags: splitCatalogList(draft.tags),
  };
}

export type RecordingFormDraft = OwnCatalogRecordingDraft;

export function recordingRecordToDraft(
  item: LocalProviderItem,
): RecordingFormDraft {
  return {
    title: item.title ?? "",
    durationInput: formatLocalDuration(item.duration_ms),
    tags: (item.tags ?? []).join(", "),
    genres: (item.genres ?? []).map((genre) => ({
      id: genre.id,
      name: genre.name,
      source_name: genre.source_name,
    })),
    credits: recordingCreditsFromItem(item),
  };
}

export function recordingDuration(
  existing: LocalProviderItem | undefined,
  draft: RecordingFormDraft,
): number | undefined {
  if (
    existing &&
    draft.durationInput === formatLocalDuration(existing.duration_ms)
  ) {
    return existing.duration_ms;
  }
  const parsed = parseLocalDuration(draft.durationInput);
  return parsed === null ? undefined : parsed;
}

export function recordingDraftToWrite(
  existing: LocalProviderItem | undefined,
  draft: RecordingFormDraft,
): LocalProviderWrite {
  const savedCredits = existing?.credits ?? [];
  const sameOrder =
    savedCredits.length === draft.credits.length &&
    savedCredits.every(
      (saved, position) => saved.artist_id === draft.credits[position].artistId,
    );
  return {
    title: draft.title.trim(),
    duration_ms: recordingDuration(existing, draft),
    genres: genreWrite(draft.genres),
    tags: splitCatalogList(draft.tags),
    credits: draft.credits.map((credit, index) => ({
      artist_id: credit.artistId,
      credit_name: credit.creditName,
      join_phrase: sameOrder
        ? savedCredits[index].join_phrase
        : retainedJoinPhrase(
            existing,
            credit.artistId,
            draft.credits[index + 1]?.artistId,
          ),
    })),
  };
}

export function recordingCreditsFromItem(item: LocalProviderItem) {
  return (item.credits ?? []).map<LocalRecordingCreditSelection>((credit) => ({
    artistId: credit.artist_id,
    creditName: credit.credit_name,
  }));
}
