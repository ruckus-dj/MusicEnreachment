import { Fragment } from "react";

/**
 * Saved tags of the current publication only. There is no fallback to source
 * files or pending metadata, and the view never writes them back.
 */
export type PublicationAudioMeasurementTags = Record<
  string,
  readonly string[] | null | undefined
>;

export interface PublicationAudioMeasurementsViewProps {
  locale: "en" | "ru";
  /** Tags of the current publication; an absent tag stays explicitly empty. */
  tags: PublicationAudioMeasurementTags;
}

/** ReplayGain/R128 display order; a partial snapshot never reorders the list. */
export const publicationAudioMeasurementFields = [
  "REPLAYGAIN_TRACK_GAIN",
  "REPLAYGAIN_TRACK_PEAK",
  "REPLAYGAIN_ALBUM_GAIN",
  "REPLAYGAIN_ALBUM_PEAK",
  "R128_TRACK_GAIN",
  "R128_ALBUM_GAIN",
] as const;

/** The same fields for hosts that only filter measurement tags out of a list. */
export const publicationAudioMeasurementTags: ReadonlySet<string> = new Set(
  publicationAudioMeasurementFields,
);

const copy = {
  en: {
    title: "Current publication audio measurements",
    notCalculated: "Not calculated",
    fields: {
      REPLAYGAIN_TRACK_GAIN: "Track ReplayGain",
      REPLAYGAIN_TRACK_PEAK: "Track ReplayGain peak",
      REPLAYGAIN_ALBUM_GAIN: "Album ReplayGain",
      REPLAYGAIN_ALBUM_PEAK: "Album ReplayGain peak",
      R128_TRACK_GAIN: "Track R128",
      R128_ALBUM_GAIN: "Album R128",
    },
  },
  ru: {
    title: "Измерения громкости текущей публикации",
    notCalculated: "Не рассчитано",
    fields: {
      REPLAYGAIN_TRACK_GAIN: "ReplayGain трека",
      REPLAYGAIN_TRACK_PEAK: "Пик ReplayGain трека",
      REPLAYGAIN_ALBUM_GAIN: "ReplayGain альбома",
      REPLAYGAIN_ALBUM_PEAK: "Пик ReplayGain альбома",
      R128_TRACK_GAIN: "R128 трека",
      R128_ALBUM_GAIN: "R128 альбома",
    },
  },
} as const;

export function PublicationAudioMeasurementsView({
  locale,
  tags,
}: PublicationAudioMeasurementsViewProps) {
  const text = copy[locale];
  return (
    <section
      className="min-w-0 border-t border-[var(--line)] pt-2"
      aria-label={text.title}
    >
      <h3 className="font-semibold">{text.title}</h3>
      <dl className="grid min-w-0 gap-x-4 gap-y-1 sm:grid-cols-[max-content_minmax(0,1fr)]">
        {publicationAudioMeasurementFields.map((tag) => {
          const values = (tags[tag] ?? []).filter(
            (value) => value.trim() !== "",
          );
          return (
            <Fragment key={tag}>
              <dt className="text-[var(--muted)]">{text.fields[tag]}</dt>
              <dd className="[overflow-wrap:anywhere]">
                {values.length > 0 ? values.join(" · ") : text.notCalculated}
              </dd>
            </Fragment>
          );
        })}
      </dl>
    </section>
  );
}
