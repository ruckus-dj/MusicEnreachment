import type { Locale } from "../../i18n";
import {
	type LibraryEditionOption,
	type LibraryEditionsStatus,
	type LibraryEditionTracks,
	type LibraryEditionTracksStatus,
	LibraryMissingEditionView,
} from "./LibraryMissingEditionView";

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
	const selected = editions.find(
		(edition) => edition.id === selectedEditionKey,
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
		selectedEditionKey,
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
