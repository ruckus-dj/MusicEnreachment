import type { ReactElement } from "react";
import {
	Button,
	Checkbox,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	SearchField,
} from "../../components/ui";
import type { Locale } from "../../i18n/types";
import type { CatalogFilter as CatalogFilterValue } from "./availabilityRows";
import { CatalogFilter } from "./CatalogFilter";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { catalogueStyles } from "./catalogueStyles";
import { LibraryArtistHeadingView } from "./LibraryArtistHeadingView";
import {
	type LibraryArtistReleaseTrackRow,
	LibraryArtistReleaseTracksView,
} from "./LibraryArtistReleaseTracksView";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";

export type ArtistHeadingModel = {
	name: string;
	disambiguation?: string | null;
	genres: readonly string[];
	palette?: string;
	count: {
		releases: number;
		positions: number;
		published: number;
		missing: number;
		local: number;
	};
	discographyComplete?: boolean;
	includesCachedGroups?: boolean;
	selection: {
		total: number;
		selected: number;
		onChange?: (value: boolean) => void;
	};
};

/**
 * Artist-level deletion control. A workflow actor (the deletion dialog) is
 * mounted through a typed slot; the inert button fallback stays local markup.
 */
export type ArtistDeletionAction =
	| { kind: "actor"; node: ReactElement }
	| { kind: "button"; isDisabled: boolean; onPress?: () => void };

type ReleaseSelection = {
	isSelected: boolean;
	isIndeterminate?: boolean;
	isDisabled?: boolean;
	onChange?: (selected: boolean) => void;
};

export type ArtistEditionModel = {
	key: string;
	title: string;
	year: string;
	catalog: string;
	/** Preformatted accessibility label: title · year · catalogue number. */
	ariaLabel: string;
	artwork: { palette?: string; title: string };
	/** Total duration in seconds, or `null` when any position is unknown. */
	durationSeconds: number | null;
	/** Provider attribution text, already resolved by the host. */
	providerText: string;
	positionCount: number;
	country: string;
	label: string;
	publishedCount: number;
	/** Declared provider total, present only when the provider cache is complete. */
	providerTrackCount: number | null;
	metadataChangedCount: number;
	selection: ReleaseSelection;
	trackSelection: ReleaseSelection;
	tracks: readonly LibraryArtistReleaseTrackRow[];
	/** Independent metadata editor actor, keyed by the host. */
	editor: ReactElement;
	/** Optional publication workflow actor for this edition. */
	publication?: ReactElement;
	/** Optional album deletion dialog actor for this edition. */
	deletion?: ReactElement;
};

export type ArtistMissingGroupModel = {
	key: string;
	title: string;
	artistName: string;
	/** Independent edition chooser actor for this absent group. */
	chooser: ReactElement;
};

export type ArtistTagFocus = {
	scope: "artist" | "release";
	releaseId?: string;
	ref: (node: HTMLDivElement | null) => void;
};

export type ArtistScreenViewContent = {
	deletion: ArtistDeletionAction;
	/** Independent artist metadata editor actor, keyed by the host. */
	metadataCard: ReactElement;
	query: string;
	filter: CatalogFilterValue;
	onQueryChange: (value: string) => void;
	onFilterChange: (value: CatalogFilterValue) => void;
	editionsKey: string;
	editions: readonly ArtistEditionModel[];
	expandedReleaseId?: string;
	missingGroups: readonly ArtistMissingGroupModel[];
	tagFocus?: ArtistTagFocus;
};

export type ArtistScreenViewProps =
	| { locale: Locale; libraryHref: string; heading: null }
	| ({
			locale: Locale;
			libraryHref: string;
			heading: ArtistHeadingModel;
	  } & ArtistScreenViewContent);

/**
 * Copy-ready artist screen composition.
 *
 * The provider owns fixture lookups, metadata and selection state, edition
 * derivation and all callbacks. This module only renders prepared models; the
 * stateful editors and workflow dialogs cross the boundary as typed element
 * slots, so the view does not depend on catalog entities, fixtures or demo
 * execution code.
 */
export function ArtistScreenView(props: ArtistScreenViewProps) {
	const { locale, libraryHref } = props;
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);

	if (!props.heading)
		return (
			<header className={catalogueStyles.objectHeader}>
				<div>
					<h1>{m("artistNotFound")}</h1>
					<p>{m("artistLinkMismatch")}</p>
				</div>
				<a href={libraryHref}>{m("allLibrary")}</a>
			</header>
		);

	const {
		heading,
		deletion,
		metadataCard,
		query,
		filter,
		onQueryChange,
		onFilterChange,
		editionsKey,
		editions,
		expandedReleaseId,
		missingGroups,
		tagFocus,
	} = props;

	return (
		<section className={catalogueStyles.object}>
			<nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
				<a href={libraryHref}>{m("backToLibrary")}</a>
			</nav>
			<LibraryArtistHeadingView
				locale={locale}
				name={heading.name}
				disambiguation={heading.disambiguation}
				genres={heading.genres}
				palette={heading.palette}
				count={heading.count}
				discographyComplete={heading.discographyComplete}
				includesCachedGroups={heading.includesCachedGroups}
				selection={heading.selection}
			/>
			<div
				className={`${catalogueStyles.actions} ${catalogueStyles.artistDanger}`}
			>
				{deletion.kind === "actor" ? (
					deletion.node
				) : (
					<Button
						className="border-[color-mix(in_srgb,var(--error)_45%,var(--control))]! bg-transparent! text-[var(--error)]!"
						isDisabled={deletion.isDisabled}
						onPress={deletion.onPress}
					>
						{m("deleteArtist")}
					</Button>
				)}
			</div>
			<div
				ref={tagFocus?.scope === "artist" ? tagFocus.ref : undefined}
				tabIndex={tagFocus?.scope === "artist" ? -1 : undefined}
			>
				{metadataCard}
			</div>
			<div className={catalogueStyles.controls}>
				<div className={catalogueStyles.panelControls}>
					<SearchField
						aria-label={m("searchLibrary")}
						value={query}
						onChange={onQueryChange}
						className={catalogueStyles.search}
					>
						<Input placeholder={m("searchPlaceholder")} />
					</SearchField>
					<CatalogFilter value={filter} onChange={onFilterChange} />
				</div>
			</div>
			<DisclosureGroup
				key={editionsKey}
				className={catalogueStyles.editions}
				allowsMultipleExpanded
				defaultExpandedKeys={
					expandedReleaseId ? new Set([expandedReleaseId]) : undefined
				}
			>
				{editions.map((edition) => (
					<div className={catalogueStyles.albumRow} key={edition.key}>
						<Disclosure className={catalogueStyles.albumCard} id={edition.key}>
							<div className={catalogueStyles.cardSelection}>
								<Checkbox
									aria-label={`${m("selectAllEditionPositions")}: ${edition.ariaLabel}`}
									className={catalogueStyles.cardCheckbox}
									isSelected={edition.selection.isSelected}
									isIndeterminate={edition.selection.isIndeterminate}
									isDisabled={edition.selection.isDisabled}
									onChange={edition.selection.onChange}
								>
									<span
										className={catalogueStyles.cardCheckboxBox}
										aria-hidden="true"
									>
										{edition.selection.isSelected
											? "✓"
											: edition.selection.isIndeterminate
												? "−"
												: ""}
									</span>
								</Checkbox>
							</div>
							<div className={catalogueStyles.albumHeader}>
								<Heading>
									<Button
										slot="trigger"
										className={`catalogue-edition-trigger ${catalogueStyles.albumTrigger}`}
										aria-label={edition.ariaLabel}
									>
										<LibraryEditionArtwork
											palette={edition.artwork.palette}
											title={edition.artwork.title}
										/>
										<span className={catalogueStyles.albumCopy}>
											<strong>{edition.title}</strong>
											<span className={catalogueStyles.albumMeta}>
												{edition.durationSeconds === null
													? m("durationUnknown")
													: `${Math.floor(edition.durationSeconds / 60)}:${String(Math.round(edition.durationSeconds % 60)).padStart(2, "0")} ${m("total")}`}
												· {edition.providerText}
											</span>
											<span className={catalogueStyles.albumMeta}>
												{edition.year} · {edition.positionCount}{" "}
												{m("positionsCount")} ·{" "}
												{edition.country || m("countryUnspecified")} ·{" "}
												{edition.label || m("local")} · {edition.catalog || "—"}
											</span>
											<span className={catalogueStyles.albumMeta}>
												{edition.providerTrackCount !== null
													? `${edition.publishedCount}/${edition.providerTrackCount} ${m("publishedCount")}`
													: `${edition.publishedCount} ${m("publishedCount")} · ${m("providerCompletenessUnknown")}`}
												{edition.metadataChangedCount
													? ` · ${m("metadataChangedFor", { count: edition.metadataChangedCount })}`
													: ""}
											</span>
										</span>
									</Button>
								</Heading>
								{edition.deletion}
							</div>
							<DisclosurePanel className={catalogueStyles.albumPanel}>
								<div
									ref={
										tagFocus?.scope === "release" &&
										tagFocus.releaseId === edition.key
											? tagFocus.ref
											: undefined
									}
									tabIndex={
										tagFocus?.scope === "release" &&
										tagFocus.releaseId === edition.key
											? -1
											: undefined
									}
									className="grid min-w-0 gap-3"
								>
									{edition.editor}
								</div>
								{edition.publication}
								<LibraryArtistReleaseTracksView
									locale={locale}
									releaseTitle={edition.title}
									releaseSelection={edition.trackSelection}
									tracks={edition.tracks}
								/>
							</DisclosurePanel>
						</Disclosure>
					</div>
				))}
			</DisclosureGroup>
			{missingGroups.length > 0 && (
				<section className={catalogueStyles.artistPanel}>
					<h2>{m("missingAlbums")}</h2>
					<p>{m("missingAlbumsDescription")}</p>
					<div className={catalogueStyles.previewTable}>
						<table>
							<thead>
								<tr>
									<th scope="col">{m("columnAlbum")}</th>
									<th scope="col">{m("columnChooseEdition")}</th>
								</tr>
							</thead>
							<tbody>
								{missingGroups.map((group) => (
									<tr key={group.key}>
										<th scope="row">
											{group.title}
											<span className={catalogueStyles.secondary}>
												{group.artistName} ·{m("albumNotInLibrary")}
											</span>
										</th>
										<td>{group.chooser}</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
				</section>
			)}
		</section>
	);
}
