import type { LocalProviderItem } from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";
import { formatLocalDuration } from "../local-release/localReleaseFormModel";
import type { OwnCatalogDetailsViewProps } from "./OwnCatalogDetailsView";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";
import {
  type OwnCatalogType,
  ownCatalogArtistCredit,
  ownCatalogGenreNames,
  ownCatalogReleaseTitle,
} from "./ownCatalogModel";

/** Projects a selected record snapshot into the detail view contract. */
export function ownCatalogDetailsViewProps({
  locale,
  type,
  item,
  loading,
  error,
  onRetry,
  onBack,
  onEdit,
}: {
  locale: Locale;
  type: OwnCatalogType;
  item?: LocalProviderItem;
  loading: boolean;
  error?: string;
  onRetry: () => void;
  onBack: () => void;
  onEdit?: () => void;
}): OwnCatalogDetailsViewProps {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const fact = (key: OwnCatalogMessage, value?: string | null) => ({
    label: m(key),
    value: value ?? "",
  });
  const title = item
    ? (type === "artists"
        ? item.name
        : type === "recordings"
          ? item.title
          : ownCatalogReleaseTitle(item)) || "—"
    : m("recordDetails");
  let body: OwnCatalogDetailsViewProps["body"] = null;
  if (loading) body = { kind: "loading", label: m("loading") };
  else if (error)
    body = { kind: "error", message: error, retryLabel: m("retry"), onRetry };
  else if (item) {
    const edition = item.edition;
    const facts = [fact("providerKey", item.provider_key)];
    if (type === "artists")
      facts.push(fact("name", item.name), fact("sortName", item.sort_name));
    if (type === "recordings")
      facts.push(
        fact("recordingArtists", ownCatalogArtistCredit(item)),
        fact("duration", formatLocalDuration(item.duration_ms)),
      );
    if (type === "releases")
      facts.push(
        fact("releaseGroup", item.release_group_title),
        fact("releaseTitle", item.title),
        fact("releaseArtists", ownCatalogArtistCredit(item)),
        fact("primaryType", item.release_group_primary_type),
        fact(
          "secondaryTypes",
          (item.release_group_secondary_types ?? []).join(", "),
        ),
        fact("language", item.language),
        fact("script", item.script),
        fact("releaseDate", edition?.release_date),
        fact("country", edition?.country),
        fact("format", edition?.format),
        fact("label", edition?.label),
        fact("catalogNumber", edition?.catalog_number),
        fact("disambiguation", edition?.disambiguation),
      );
    facts.push(fact("genres", ownCatalogGenreNames(item).join(", ")));
    if (type !== "releases")
      facts.push(fact("tags", (item.tags ?? []).join(", ")));
    body = {
      kind: "record",
      facts,
      tracklist:
        type === "releases"
          ? {
              title: m("tracklist"),
              positions: m("positions"),
              empty: m("noPositions"),
              tracks: (item.tracks ?? []).map((track, index) => ({
                id: track.id || String(index),
                number: String(track.displayed_number || index + 1),
                title:
                  track.title_override ||
                  track.recording_title ||
                  m("recordingNotSelected"),
                description: [
                  track.provider_code,
                  formatLocalDuration(
                    track.duration_override_ms ?? track.recording_duration_ms,
                  ),
                ]
                  .filter(Boolean)
                  .join(" · "),
              })),
            }
          : undefined,
    };
  }
  return {
    title,
    eyebrow: m("currentDataNoHistory"),
    backLabel: m("back"),
    editLabel: m("edit"),
    onBack,
    onEdit: item ? onEdit : undefined,
    body,
  };
}
