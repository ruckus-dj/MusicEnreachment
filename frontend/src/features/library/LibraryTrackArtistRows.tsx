import type { LibraryPositionPage } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import type { ArtistCardSelectionControl } from "./LibraryArtistCards";
import {
  LibraryTrackListView,
  type LibraryTrackRow,
} from "./LibraryTrackListView";
import type { LibrarySelectionScope } from "./useLibrarySelection";

type SelectionControl = (
  scope: LibrarySelectionScope,
) => ArtistCardSelectionControl;

type Position = LibraryPositionPage["items"][number];

const untitledTitle = { en: "Untitled", ru: "Без названия" } as const;

function positionHref(position: Position) {
  return `#/library/releases/${encodeURIComponent(position.release.id)}/positions/${encodeURIComponent(position.id)}`;
}

function releaseHref(position: Position) {
  return `#/library/releases/${encodeURIComponent(position.release.id)}`;
}

function positionCredits(position: Position): LibraryTrackRow["credits"] {
  return position.artists.map((credit) => ({
    name: credit.credit_name || credit.artist_name || "",
    href: credit.artist_id
      ? `#/library/artists/${encodeURIComponent(credit.artist_id)}`
      : null,
    joinPhrase: credit.join_phrase,
  }));
}

export function LibraryPositionRows({
  items,
  selectionControl,
}: {
  items: LibraryPositionPage["items"];
  selectionControl: SelectionControl;
}) {
  const { locale } = useI18n();
  const rows: LibraryTrackRow[] = items.map((position) => {
    const title = position.title || untitledTitle[locale];
    return {
      id: position.id,
      title,
      durationMs: position.provider_duration_ms,
      href: positionHref(position),
      releaseTitle: position.release.title,
      releaseHref: releaseHref(position),
      credits: positionCredits(position),
      positionLabel: `${position.medium} · ${position.displayed_number || position.position}`,
      hasAudio: position.audio_available,
      variantCount: position.source_variant_count,
      published: Boolean(position.publication),
      selection: selectionControl({
        kind: "position",
        position: {
          release_id: position.release.id,
          position_id: position.id,
          title,
          release_title: position.release.title,
        },
      }),
    };
  });
  return (
    <LibraryTrackListView
      locale={locale}
      rows={rows}
      pageSelection={selectionControl({ kind: "page" })}
    />
  );
}
