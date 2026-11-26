import type { OwnCatalogDetailsViewProps } from "../../../ui/features/local-catalog/OwnCatalogDetailsView";
import { formatLocalDuration } from "../../../ui/features/local-release/localReleaseFormModel";
import {
  type LocalCatalogMessage,
  localCatalogMessage,
} from "./localCatalogMessage";
import {
  type LocalCatalogType,
  localCatalogArtistNames,
  localCatalogGenreNames,
  localCatalogRecordingTitle,
  localCatalogReleaseTitle,
} from "./localCatalogModel";
import type {
  LocalProviderRecord,
  LocalProviderState,
} from "./localProviderModel";

type LocalCatalogDetailsInput = {
  locale: "ru" | "en";
  state: LocalProviderState;
  type: LocalCatalogType;
  record: LocalProviderRecord;
  onBack: () => void;
  onEdit: () => void;
};

/**
 * JSX-free projection of one Local catalog record into the shared details view.
 * The provider resolves the record and routing; the view actor stays portable.
 */
export function localCatalogDetailsViewProps({
  locale,
  state,
  type,
  record,
  onBack,
  onEdit,
}: LocalCatalogDetailsInput): OwnCatalogDetailsViewProps {
  const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
  const fact = (key: LocalCatalogMessage, value?: string | null) => ({
    label: m(key),
    value: value ?? "",
  });
  const title =
    (type === "artists"
      ? record.name
      : type === "recordings"
        ? localCatalogRecordingTitle(record)
        : localCatalogReleaseTitle(record)) || "—";
  const artists = localCatalogArtistNames(state, record.artistIds).join(", ");
  const facts = [fact("providerKey", record.key)];
  if (type === "artists") {
    const related = state.items.releases
      .filter((release) => release.artistIds?.includes(record.id))
      .map(localCatalogReleaseTitle)
      .join(", ");
    facts.push(
      fact("name", record.name),
      fact("sortName", record.sortName),
      fact("relatedReleases", related || m("noLinkedReleases")),
    );
  }
  if (type === "recordings") {
    const usedIn = state.items.releases
      .filter((release) =>
        release.tracks?.some((track) => track.recordingId === record.id),
      )
      .map(localCatalogReleaseTitle)
      .join(", ");
    facts.push(
      fact("recordingArtists", artists),
      fact("duration", formatLocalDuration(record.duration_ms)),
      fact("explicit", record.explicit),
      fact("isrc", record.isrcs),
      fact("recordingUsedIn", usedIn),
    );
  }
  if (type === "releases")
    facts.push(
      fact("releaseGroup", record.release_group_title),
      fact("releaseArtists", artists),
      fact("primaryType", record.primaryType),
      fact("language", record.language),
      fact("script", record.script),
      fact("releaseDate", record.release_date),
      fact("country", record.country_code),
      fact("format", record.format),
      fact("label", record.label),
      fact("catalogNumber", record.catalog_number),
    );
  facts.push(
    fact("disambiguation", record.disambiguation),
    fact("genres", localCatalogGenreNames(record).join(", ")),
    fact("tags", record.tags),
  );
  return {
    title,
    eyebrow: m("currentDataNoHistory"),
    backLabel: m("back"),
    editLabel: m("edit"),
    onBack,
    onEdit,
    body: {
      kind: "record",
      facts,
      tracklist:
        type === "releases"
          ? {
              title: m("tracklist"),
              positions: m("positions"),
              empty: m("noPositions"),
              tracks: (record.tracks ?? []).map((track, index) => {
                const recording = state.items.recordings.find(
                  (item) => item.id === track.recordingId,
                );
                const duration =
                  track.durationOverrideMs === "" ||
                  track.durationOverrideMs == null
                    ? recording?.duration_ms
                    : track.durationOverrideMs;
                return {
                  id: track.id || String(index),
                  number: String(index + 1),
                  title:
                    track.titleOverride ||
                    (recording
                      ? localCatalogRecordingTitle(recording)
                      : m("recordingNotSelected")),
                  description: [
                    recording?.provider,
                    formatLocalDuration(duration),
                  ]
                    .filter(Boolean)
                    .join(" · "),
                };
              }),
            }
          : undefined,
    },
  };
}
