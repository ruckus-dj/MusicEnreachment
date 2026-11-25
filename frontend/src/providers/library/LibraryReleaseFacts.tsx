import type {
  LibraryReleaseDetailResponse,
  LibraryReleaseTrackResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import type { LibraryArtistReleaseTrackRow } from "../../ui/features/library/LibraryArtistReleaseTracksView";
import type { LibraryPositionSource } from "../../ui/features/library/LibraryPositionSourcesView";
import type {
  ReleaseOverviewCredit,
  ReleaseOverviewMedia,
} from "../../ui/features/library/ReleaseOverviewView";
import { sourceInventoryStatusLabel } from "../../ui/features/sources/SourceInventoryView";
import { statusLabel } from "../sources/sourcesApi";

export function ReleaseCreditList({
  credits,
}: {
  credits: LibraryReleaseDetailResponse["artists"];
}) {
  const { locale } = useI18n();
  if (!credits?.length)
    return (
      <span>
        {locale === "ru" ? "Исполнители не указаны" : "No artists specified"}
      </span>
    );
  const creditKeyCounts = new Map<string, number>();
  return (
    <ul className="my-1 flex list-none flex-wrap gap-x-4 gap-y-2 p-0">
      {credits.map((credit) => {
        const signature = `${credit.artist_id}-${credit.role}-${credit.join_phrase}-${credit.credit_name}`;
        const occurrence = creditKeyCounts.get(signature) ?? 0;
        creditKeyCounts.set(signature, occurrence + 1);
        const displayName = credit.credit_name || credit.artist_name;
        const canonicalName =
          credit.artist_name && credit.artist_name !== displayName
            ? credit.artist_name
            : null;
        return (
          <li key={`${signature}-${occurrence}`}>
            <a
              href={`#/library/artists/${encodeURIComponent(credit.artist_id)}`}
            >
              {displayName}
            </a>
            {canonicalName ? (
              <>
                {" ("}
                <span className="text-[.85em] text-[color-mix(in_srgb,currentColor_68%,transparent)]">
                  {canonicalName}
                </span>
                {")"}
              </>
            ) : null}
            {credit.join_phrase}
            {credit.role ? (
              <span className="text-[color-mix(in_srgb,currentColor_68%,transparent)]">
                {" "}
                · {credit.role}
              </span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

export function releaseOverviewCredits(
  credits: LibraryReleaseDetailResponse["artists"],
): ReleaseOverviewCredit[] {
  return (credits ?? []).map((credit) => ({
    name: credit.credit_name || credit.artist_name || "",
    canonicalName: credit.artist_name,
    href: `#/library/artists/${encodeURIComponent(credit.artist_id)}`,
    joinPhrase: credit.join_phrase,
    role: credit.role || null,
  }));
}

export function releaseOverviewMedia(release: LibraryReleaseDetailResponse): {
  media: ReleaseOverviewMedia[];
  fallbackTracks?: LibraryArtistReleaseTrackRow[];
  unassignedTracks?: LibraryArtistReleaseTrackRow[];
} {
  const mediaRecords = release.media ?? [];
  const tracks = release.tracks ?? [];
  const trackRow = (
    track: LibraryReleaseTrackResponse,
  ): LibraryArtistReleaseTrackRow => ({
    id: track.id,
    numberLabel:
      track.displayed_number.trim() || String(track.position).padStart(2, "0"),
    title: track.title,
    href: `#/library/releases/${encodeURIComponent(release.id)}/positions/${encodeURIComponent(track.id)}`,
    durationMs: track.duration_ms,
    sourceCount: track.sources?.length ?? 0,
    isNewRecording: !track.recording_id,
    publication: track.publication
      ? {
          state: "published",
          codec: track.publication.codec,
          container: track.publication.container,
        }
      : { state: "not-published" },
  });
  const tracksForMedium = (medium: (typeof mediaRecords)[number]) =>
    tracks.filter((track) =>
      track.medium_id
        ? track.medium_id === medium.id
        : track.medium === medium.medium,
    );
  const media = mediaRecords.map((medium) => ({
    key: medium.id,
    number: medium.medium,
    title: medium.title,
    format: medium.format,
    tracks: tracksForMedium(medium).map(trackRow),
  }));
  const ungroupedTracks = tracks.filter(
    (track) =>
      !mediaRecords.some((medium) => tracksForMedium(medium).includes(track)),
  );
  return {
    media,
    fallbackTracks: media.length ? undefined : tracks.map(trackRow),
    unassignedTracks: media.length ? ungroupedTracks.map(trackRow) : undefined,
  };
}

function rootStatusLabel(status: string, locale: "en" | "ru") {
  switch (status) {
    case "available":
    case "unavailable":
    case "unknown":
      return statusLabel(status, locale);
    default:
      return status;
  }
}

function probeStatusLabel(status: string, locale: "en" | "ru") {
  switch (status) {
    case "audio":
    case "no_audio":
    case "probe_error":
      return sourceInventoryStatusLabel(locale, status);
    case "not_analyzed":
      return locale === "ru" ? "Не проанализировано" : "Not analyzed";
    default:
      return status;
  }
}

export function positionSourceItems(
  track: LibraryReleaseTrackResponse,
  locale: "en" | "ru",
): readonly LibraryPositionSource[] {
  const copy =
    locale === "ru"
      ? {
          unspecified: "Не указано",
          root: "корень",
          enabled: "включён",
          disabled: "выключен",
          probe: "проверка",
          modified: "изменение",
          size: "размер",
          bytes: "байт",
        }
      : {
          unspecified: "Not specified",
          root: "root",
          enabled: "enabled",
          disabled: "disabled",
          probe: "probe",
          modified: "modified",
          size: "size",
          bytes: "bytes",
        };
  return (track.sources ?? []).map((source, index) => ({
    id: source.media_variant_id,
    label: String(index + 1),
    selected: source.selected,
    authority: {
      decision: source.decision_method,
      compatible: source.compatible,
      policy: source.policy_version ?? "",
      locations: (source.locations ?? []).map((location) => ({
        id: location.location_id,
        path: location.relative_path,
        href: `#/sources/${encodeURIComponent(location.root_id)}`,
        details: [
          `${copy.root} ${rootStatusLabel(location.root_status, locale)} (${location.root_enabled ? copy.enabled : copy.disabled})`,
          `${copy.probe} ${probeStatusLabel(location.probe_status, locale)}`,
          `${copy.modified} ${location.mtime || copy.unspecified}`,
          `${copy.size} ${location.size_bytes} ${copy.bytes}`,
        ].join(" · "),
      })),
    },
  }));
}
