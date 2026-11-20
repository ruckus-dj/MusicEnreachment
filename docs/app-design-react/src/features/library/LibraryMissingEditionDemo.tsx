import type { Locale } from "../../i18n";
import {
	type LibraryEditionOption,
	type LibraryEditionTracks,
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
 * Projects the prototype's absent-group editions into the shared chooser.
 * The fixture list order is stable, so index-based keys are unique even when
 * two positions share a title. Empty keys mean no edition is selected, and
 * selection is stored by the host under the group ID.
 */
export function LibraryMissingEditionDemo({
	locale,
	groupTitle,
	editions,
	selectedEditionKey,
	onSelectEdition,
}: {
	locale: Locale;
	groupTitle: string;
	editions: readonly MissingEditionFixture[];
	selectedEditionKey: string;
	onSelectEdition: (editionKey: string) => void;
}) {
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

	return (
		<LibraryMissingEditionView
			locale={locale}
			groupTitle={groupTitle}
			editions={options}
			editionsStatus={editions.length ? "ready" : "empty"}
			selectedEditionKey={selectedEditionKey}
			onSelectEdition={onSelectEdition}
			tracksStatus="ready"
			tracks={tracks}
		/>
	);
}
