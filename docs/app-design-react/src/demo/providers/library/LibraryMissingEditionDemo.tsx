import {
  type LibraryEditionOption,
  type LibraryEditionsStatus,
  type LibraryEditionTracks,
  type LibraryEditionTracksStatus,
  LibraryMissingEditionView,
} from "../../../ui/features/library/LibraryMissingEditionView";
import type { Locale } from "../../../ui/i18n/types";

/**
 * Fixture shape of an absent MusicBrainz release group edition. It carries
 * explicit titles only: medium, position numbers, track counts and cache
 * completeness are not present and must not be inferred from the titles.
 */
export type MissingEditionFixture = {
  id: string;
  year: string;
  country: string;
  format: string;
  disambiguation: string;
  titles: string[];
};

const editionLabel = (edition: MissingEditionFixture) =>
  [edition.year, edition.country, edition.format, edition.disambiguation]
    .filter(Boolean)
    .join(" · ");

/**
 * Data-only projection for the shared edition chooser. Hosts and shared list
 * views can consume these typed props without passing rendered controls.
 */
export interface MissingEditionChooserProps {
  locale: Locale;
  groupTitle: string;
  editions: LibraryEditionOption[];
  editionsStatus: LibraryEditionsStatus;
  soleEdition: boolean;
  selectedEditionKey: string;
  onSelectEdition: (editionKey: string) => void;
  tracksStatus: LibraryEditionTracksStatus;
  tracks: LibraryEditionTracks | null;
}

export interface MissingEditionChooserInput {
  locale: Locale;
  groupTitle: string;
  editions: readonly MissingEditionFixture[];
  selectedEditionKey: string;
  onSelectEdition: (editionKey: string) => void;
}

/**
 * Projects the prototype's absent-group editions into the shared chooser.
 * The fixture list order is stable, so index-based keys are unique even when
 * two positions share a title. Empty keys mean no edition is selected, and
 * selection is stored by the host under the group ID.
 */
export function missingEditionChooserProps({
  locale,
  groupTitle,
  editions,
  selectedEditionKey,
  onSelectEdition,
}: MissingEditionChooserInput): MissingEditionChooserProps {
  const options: LibraryEditionOption[] = editions.map((edition) => ({
    key: edition.id,
    label: editionLabel(edition),
    trackCount: null,
  }));
  // The production host selects a single known edition automatically. Mirror
  // that so the compact label and its tracks render without a picker.
  const effectiveEditionKey =
    selectedEditionKey || (editions.length === 1 ? editions[0]?.id : "") || "";
  const selected = editions.find(
    (edition) => edition.id === effectiveEditionKey,
  );
  const tracks: LibraryEditionTracks | null = selected
    ? {
        complete: null,
        trackCount: null,
        tracks: selected.titles.map((title, index) => ({
          key: `${selected.id}:${index}`,
          title,
        })),
      }
    : null;

  return {
    locale,
    groupTitle,
    editions: options,
    editionsStatus: editions.length ? "ready" : "empty",
    soleEdition: editions.length === 1,
    selectedEditionKey: effectiveEditionKey,
    onSelectEdition,
    tracksStatus: "ready",
    tracks,
  };
}

/**
 * Fixture-backed presentation of the chooser, retained for screens that own
 * their selection state directly (for example the artist screen).
 */
export function LibraryMissingEditionDemo(props: MissingEditionChooserInput) {
  return <LibraryMissingEditionView {...missingEditionChooserProps(props)} />;
}
