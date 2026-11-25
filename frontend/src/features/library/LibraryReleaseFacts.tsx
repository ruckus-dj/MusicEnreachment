import type {
  LibraryReleaseDetailResponse,
  LibraryReleaseTrackResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { sourceInventoryStatusLabel } from "../sources/SourceInventoryView";
import { statusLabel } from "../sources/sourcesApi";
import {
  type LibraryArtistReleaseTrackRow,
  LibraryArtistReleaseTracksView,
} from "./LibraryArtistReleaseTracksView";
import type { LibraryPositionSource } from "./LibraryPositionSourcesView";

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

export function ReleaseMediaSummary({
  release,
}: {
  release: LibraryReleaseDetailResponse;
}) {
  const { locale } = useI18n();
  const copy =
    locale === "ru"
      ? {
          heading: "Носители и позиции",
          medium: "Носитель",
          untitled: "Без названия",
          unspecified: "Не указан",
          positionsUnspecified: "Позиции не указаны",
          mediaUnrecorded: "Сведения о носителях не сохранены.",
          releasePositions: "Позиции релиза:",
          positionsWithoutMedia: "Позиции без сведений о носителе:",
        }
      : {
          heading: "Media and positions",
          medium: "Medium",
          untitled: "Untitled",
          unspecified: "Not specified",
          positionsUnspecified: "No positions specified",
          mediaUnrecorded: "Media information has not been saved.",
          releasePositions: "Release positions:",
          positionsWithoutMedia: "Positions without saved media information:",
        };
  const media = release.media ?? [];
  const tracks = release.tracks ?? [];
  const tracksForMedium = (medium: (typeof media)[number]) =>
    tracks.filter((track) =>
      track.medium_id
        ? track.medium_id === medium.id
        : track.medium === medium.medium,
    );
  const ungroupedTracks = tracks.filter(
    (track) => !media.some((medium) => tracksForMedium(medium).includes(track)),
  );
  const trackRows = (
    items: LibraryReleaseTrackResponse[],
  ): LibraryArtistReleaseTrackRow[] =>
    items.map((track) => ({
      id: track.id,
      numberLabel:
        track.displayed_number.trim() ||
        String(track.position).padStart(2, "0"),
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
    }));
  const mediumLabel = (medium: (typeof media)[number]) =>
    `${copy.medium} ${medium.medium} · ${medium.title || copy.untitled} · ${
      medium.format || copy.unspecified
    }`;
  return (
    <section
      className="grid min-w-0 gap-3"
      aria-labelledby="release-media-heading"
    >
      <h2
        id="release-media-heading"
        className="my-2 text-base leading-[22px] font-[650]"
      >
        {copy.heading}
      </h2>
      {media.length ? (
        <div className="grid gap-4">
          {media.map((medium) => {
            const items = tracksForMedium(medium);
            const label = mediumLabel(medium);
            return (
              <section className="min-w-0" key={medium.id} aria-label={label}>
                <h3 className="mb-1.5 text-sm font-semibold">{label}</h3>
                {items.length ? (
                  <LibraryArtistReleaseTracksView
                    locale={locale}
                    releaseTitle={label}
                    tracks={trackRows(items)}
                  />
                ) : (
                  <p>{copy.positionsUnspecified}</p>
                )}
              </section>
            );
          })}
        </div>
      ) : (
        <p>{copy.mediaUnrecorded}</p>
      )}
      {!media.length && tracks.length > 0 ? (
        <div className="min-w-0">
          <p>{copy.releasePositions}</p>
          <LibraryArtistReleaseTracksView
            locale={locale}
            releaseTitle={release.title}
            tracks={trackRows(tracks)}
          />
        </div>
      ) : null}
      {media.length > 0 && ungroupedTracks.length > 0 ? (
        <div className="min-w-0">
          <p>{copy.positionsWithoutMedia}</p>
          <LibraryArtistReleaseTracksView
            locale={locale}
            releaseTitle={release.title}
            tracks={trackRows(ungroupedTracks)}
          />
        </div>
      ) : null}
    </section>
  );
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
