import {
  PublicationAudioMeasurementsView,
  type PublicationAudioMeasurementTags,
  publicationAudioMeasurementTags,
} from "./PublicationAudioMeasurementsView";
import { TagSnapshotView } from "./TagSnapshotView";

const copy = {
  en: {
    title: "Tag snapshot",
    unavailable: "The tag snapshot is unavailable.",
    empty: "No tags.",
    onlyMeasurements: "The snapshot contains only audio measurements.",
  },
  ru: {
    title: "Снимок тегов",
    unavailable: "Снимок тегов недоступен.",
    empty: "Теги отсутствуют.",
    onlyMeasurements: "В снимке только измерения громкости.",
  },
} as const;

export function PublicationTagSnapshotView({
  locale,
  tags,
}: {
  locale: "en" | "ru";
  tags: PublicationAudioMeasurementTags | undefined;
}) {
  const text = copy[locale];
  if (!tags)
    return <p className="text-sm text-[var(--muted)]">{text.unavailable}</p>;

  const entries = Object.entries(tags);
  const rows = entries
    .filter(([key]) => !publicationAudioMeasurementTags.has(key))
    .map(([key, values]) => ({ key, values: values ?? [] }));
  return (
    <>
      <TagSnapshotView
        title={text.title}
        rows={rows}
        empty={entries.length ? text.onlyMeasurements : text.empty}
      />
      <PublicationAudioMeasurementsView locale={locale} tags={tags} />
    </>
  );
}
