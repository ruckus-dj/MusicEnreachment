import type { ReactNode } from "react";
import {
	Button,
	Cell,
	Column,
	Row,
	Table,
	TableBody,
	TableHeader,
} from "../../components/ui";
import { useI18n } from "../../i18n/I18nProvider";
import { audioProperties } from "../../metadata/audioProperties";
import { artistReleaseHref } from "./artistReleaseRoute";
import { availabilityRowClass, type CatalogFilter } from "./availabilityRows";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
	SourceFiles,
} from "./catalogData";
import {
	originalArtistName,
	originalProviders,
	trackNumberLabel,
} from "./catalogData";
import { catalogueStyles } from "./catalogueStyles";
import {
	type LibraryAlbumPosition,
	LibraryAlbumPositionsView,
} from "./LibraryAlbumPositionsView";
import {
	type ArtistSelectionState,
	type LibraryArtistCard,
	LibraryArtistCards,
} from "./LibraryArtistCards";
import {
	type LibraryTrackCredit,
	LibraryTrackListView,
	type LibraryTrackRow,
} from "./LibraryTrackListView";
import {
	type LibraryRowsMessage,
	libraryRowsMessage,
} from "./libraryRowsMessages";

type View = "artists" | "albums" | "tracks";
type CatalogTrack = Omit<OriginalTrack, "published"> & {
	published: boolean | OriginalTrack["published"];
};
type Release = Omit<OriginalRelease, "tracks"> & { tracks: CatalogTrack[] };
interface Props {
	view: View;
	artists: CatalogArtist[];
	releases: Release[];
	missingCounts: Map<string, number>;
	hasAudio: (track: Release["tracks"][number]) => boolean;
	files: SourceFiles;
	filter?: CatalogFilter;
	groupCheckbox: (
		ids: string[],
		label: string,
		appearance?: "table" | "artwork",
	) => ReactNode;
	expanded: string | null;
	onToggleAlbum: (id: string) => void;
	/** Bulk selection covers only the rows currently passed for this page. */
	pageScopedSelection?: boolean;
	/** Raw page-scoped selection used to derive artist card state; tables keep using groupCheckbox. */
	pageSelection?: ArtistSelectionState;
}
const sourceIds = (track: Pick<OriginalTrack, "sourceId" | "sourceIds">) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];
const selectionControl = (ids: string[], selection?: ArtistSelectionState) => {
	const selectedCount = ids.filter((id) =>
		selection?.selectedIds.has(id),
	).length;
	return {
		isSelected: ids.length > 0 && selectedCount === ids.length,
		isIndeterminate: selectedCount > 0 && selectedCount < ids.length,
		isDisabled: !selection || ids.length === 0,
		onChange: (selected: boolean) => selection?.onChange(ids, selected),
	};
};
const MobileField = ({
	label,
	children,
}: {
	label: string;
	children: ReactNode;
}) => (
	<dl className={`${catalogueStyles.mobileCatalogField} m-0`}>
		<dt>{label}</dt>
		<dd>{children}</dd>
	</dl>
);
function artistCardModels(
	artists: CatalogArtist[],
	releases: Release[],
	missingCounts: Map<string, number>,
	hasAudio: (track: Release["tracks"][number]) => boolean,
	selection?: ArtistSelectionState,
) {
	const pageIds = new Set<string>();
	const rows: LibraryArtistCard[] = artists.map((artist) => {
		const own = releases.filter(
			(release) =>
				release.artistId === artist.id ||
				release.credits?.some((credit) => credit.artistId === artist.id),
		);
		const ownTracks = own.flatMap((release) => release.tracks);
		const trackIds = ownTracks.map((track) => track.id);
		for (const id of trackIds) pageIds.add(id);
		const missingEditionCount = missingCounts.get(artist.id);
		return {
			id: artist.id,
			name: artist.name,
			href: `#artist/${artist.id}`,
			monogram: artist.name.slice(0, 1),
			palette: artist.color || "clay",
			genres: artist.genre ? [artist.genre] : [],
			releaseCount: own.length + (missingEditionCount ?? 0),
			missingEditionCount,
			positionCount: ownTracks.length,
			availableCount: ownTracks.filter(hasAudio).length,
			publishedCount: ownTracks.filter((track) => Boolean(track.published))
				.length,
			selection: selectionControl(trackIds, selection),
		};
	});
	return {
		rows,
		pageSelectionControl: selection
			? selectionControl([...pageIds], selection)
			: undefined,
	};
}

export function LibraryCatalogRows({
	view,
	artists,
	releases,
	missingCounts,
	hasAudio,
	files,
	filter = "all",
	groupCheckbox,
	expanded,
	onToggleAlbum,
	pageScopedSelection = false,
	pageSelection,
}: Props) {
	const { locale } = useI18n();
	const m = (
		key: LibraryRowsMessage,
		values?: Record<string, string | number>,
	) => libraryRowsMessage(locale, key, values);
	const bulkSelectionLabel = m(
		pageScopedSelection ? "selectAllPagePositions" : "selectAllTablePositions",
	);
	if (view === "artists") {
		const { rows, pageSelectionControl } = artistCardModels(
			artists,
			releases,
			missingCounts,
			hasAudio,
			pageSelection,
		);
		return (
			<LibraryArtistCards
				locale={locale}
				artists={rows}
				pageSelectionControl={pageSelectionControl}
			/>
		);
	}
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const tableTrackIds = tracks.map(({ track }) => track.id);
	if (view === "tracks") {
		const rows: LibraryTrackRow[] = tracks.map(({ release, track }) => {
			const ids = sourceIds(track);
			const selectedFile = track.sourceId ? files[track.sourceId] : undefined;
			const sourceCodec = selectedFile?.codec ?? null;
			const pub =
				track.published && typeof track.published === "object"
					? track.published
					: null;
			const rowClass = availabilityRowClass(
				track as OriginalTrack,
				filter,
				hasAudio,
			);
			const filterState: LibraryTrackRow["filterState"] =
				rowClass === "filter-match"
					? "match"
					: rowClass === "filter-muted"
						? "muted"
						: undefined;
			// A persisted source with an unknown codec is still selected; only
			// the codec fact is omitted, never downgraded to "unselected".
			const sourceSelection: LibraryTrackRow["sourceSelection"] = sourceCodec
				? undefined
				: track.sourceId
					? undefined
					: ids.length > 0
						? "unselected"
						: "missing";
			const credits: LibraryTrackCredit[] = track.recordingCredits?.length
				? track.recordingCredits.map((credit) => ({
						name:
							credit.creditName ||
							(credit.artistId
								? artists.find((artist) => artist.id === credit.artistId)?.name
								: undefined) ||
							"",
						...(credit.artistId ? { href: `#artist/${credit.artistId}` } : {}),
						joinPhrase: credit.joinPhrase,
					}))
				: [{ name: originalArtistName(track.artistId, artists) }];
			return {
				id: track.id,
				title: track.title,
				durationMs:
					track.duration === null ? null : Math.round(track.duration) * 1000,
				href: `#track/${track.id}`,
				releaseTitle: release.title,
				releaseHref: artistReleaseHref(release, artists),
				releaseSubtitle: `${originalArtistName(release.artistId, artists)} · ${release.year}`,
				recordingTitle: track.recordingTitle,
				credits,
				positionLabel: `${
					release.media?.[(track.medium || 1) - 1]?.title || track.medium || "—"
				} · ${trackNumberLabel(release, track)}`,
				hasAudio: hasAudio(track),
				variantCount: ids.length,
				published: Boolean(track.published),
				sourceCodec,
				...(sourceSelection ? { sourceSelection } : {}),
				properties: audioProperties(selectedFile),
				paths: ids
					.map((id) => files[id]?.path || files[id]?.name)
					.filter((path): path is string => Boolean(path)),
				...(pub
					? {
							publicationContainer: pub.container,
							publicationCodec: pub.codec,
							tagsTitle: pub.tags.TITLE?.join(", ") || track.title,
						}
					: {}),
				tagsHref: `#tags/${track.id}/track`,
				...(filterState ? { filterState } : {}),
				selection: selectionControl([track.id], pageSelection),
			};
		});
		return (
			<LibraryTrackListView
				locale={locale}
				rows={rows}
				pageSelection={selectionControl(tableTrackIds, pageSelection)}
				label={m("tracksTable")}
			/>
		);
	}
	const albumRows = releases.flatMap((release) => {
		const summary = { id: release.id, release, detail: false };
		return expanded === release.id
			? [summary, { id: `${release.id}:tracks`, release, detail: true }]
			: [summary];
	});
	const positionsFor = (release: Release): LibraryAlbumPosition[] =>
		release.tracks.map((track) => {
			const ids = sourceIds(track);
			const source = track.sourceId ? files[track.sourceId] : undefined;
			const published =
				track.published && typeof track.published === "object"
					? track.published
					: null;
			const rowClass = availabilityRowClass(
				track as OriginalTrack,
				filter,
				hasAudio,
			);
			const filterState: LibraryAlbumPosition["filterState"] =
				rowClass === "filter-match"
					? "match"
					: rowClass === "filter-muted"
						? "muted"
						: undefined;
			const publication: LibraryAlbumPosition["publication"] = published
				? {
						state: "published",
						container: published.container,
						codec: published.codec,
						title: published.tags.TITLE?.join(", ") || track.title,
					}
				: { state: "not-published" };
			const sourceSelection: LibraryAlbumPosition["sourceSelection"] =
				source?.codec ? undefined : ids.length > 0 ? "unselected" : "missing";
			return {
				id: track.id,
				numberLabel: trackNumberLabel(release, track),
				title: track.title,
				recordingHref: `#track/${track.id}`,
				tagsHref: `#tags/${track.id}/track`,
				artist: originalArtistName(track.artistId, artists),
				recordingTitle: track.recordingTitle,
				durationMs:
					track.duration === null ? null : Math.round(track.duration) * 1000,
				sourceCount: ids.length,
				hasAudio: hasAudio(track),
				sourceLabel: source?.codec ?? null,
				...(sourceSelection ? { sourceSelection } : {}),
				paths: ids
					.map((id) => files[id]?.path || files[id]?.name)
					.filter((path): path is string => Boolean(path)),
				properties: audioProperties(source),
				publication,
				isNewRecording: !track.recordingId,
				...(filterState ? { filterState } : {}),
				selection: selectionControl([track.id], pageSelection),
			};
		});
	const albumColumnWidths = [
		catalogueStyles.selectionColumn,
		"", // The title gets the space left after the other columns.
		"w-[15%]",
		"w-[12%]",
		"w-[8%]",
		"w-[8%]",
		"w-[9%]",
		"w-[12%]",
		"w-[9%]",
	];
	const columns = [
		m("select"),
		m("album"),
		m("artist"),
		m("yearEdition"),
		m("positions"),
		m("sources"),
		m("audio"),
		m("publication"),
		m("provider"),
	];
	return (
		<div className="w-full min-w-0">
			<div
				className="hidden gap-2 max-[900px]:grid"
				aria-label={m("albumsTable")}
			>
				{releases.length === 0 ? (
					<div className={catalogueStyles.empty}>{m("noResults")}</div>
				) : (
					<div className={catalogueStyles.mobileCatalogRow}>
						<MobileField label={bulkSelectionLabel}>
							{groupCheckbox(tableTrackIds, bulkSelectionLabel)}
						</MobileField>
					</div>
				)}
				{releases.map((release) => {
					const provider = originalProviders.find(
						(item) => item.id === release.providerId,
					);
					const published = release.tracks.filter((track) =>
						Boolean(track.published),
					).length;
					const sourceCount = release.tracks.filter(
						(track) => sourceIds(track).length > 0,
					).length;
					return (
						<article
							className={catalogueStyles.mobileCatalogRow}
							key={release.id}
						>
							<MobileField label={m("select")}>
								{groupCheckbox(
									release.tracks.map((track) => track.id),
									m("selectAlbumPositions", { album: release.title }),
								)}
							</MobileField>
							<MobileField label={m("album")}>
								<Button
									className={catalogueStyles.expander}
									aria-expanded={expanded === release.id}
									onPress={() => onToggleAlbum(release.id)}
								>
									{release.title || m("untitled")}
								</Button>
								{artistReleaseHref(release, artists) && (
									<a
										className={catalogueStyles.sub}
										href={artistReleaseHref(release, artists)!}
									>
										{m("openRelease")}
									</a>
								)}
							</MobileField>
							<MobileField label={m("artist")}>
								{release.credits?.length ? (
									release.credits.map((credit, index) => (
										<span key={`${credit.artistId ?? "credit"}-${index}`}>
											{index > 0 && ", "}
											{credit.artistId ? (
												<a href={`#artist/${credit.artistId}`}>
													{credit.credit || credit.name}
												</a>
											) : (
												credit.credit || credit.name
											)}
										</span>
									))
								) : release.artistId ? (
									<a href={`#artist/${release.artistId}`}>
										{originalArtistName(release.artistId, artists)}
									</a>
								) : (
									m("artistNotSpecified")
								)}
							</MobileField>
							<MobileField label={m("yearEdition")}>
								{release.year || m("dateNotSpecified")} ·{" "}
								{release.country || m("local")} · {release.catalog || "—"}
							</MobileField>
							<MobileField label={m("positions")}>
								{release.tracks.length}
							</MobileField>
							<MobileField label={m("sources")}>{sourceCount}</MobileField>
							<MobileField label={m("audio")}>
								{m("audioSummary", {
									count: release.tracks.filter(hasAudio).length,
								})}
								{provider?.tracks.length ? ` / ${provider.tracks.length}` : ""}
							</MobileField>
							<MobileField label={m("publication")}>
								{m("publishedCount", {
									published,
									total: release.tracks.length,
								})}
								<span className={catalogueStyles.sub}>
									{provider?.tracks.length === release.tracks.length
										? m("completenessKnown")
										: m("completenessUnknown")}
								</span>
							</MobileField>
							<MobileField label={m("provider")}>
								{provider ? "MusicBrainz" : m("localCollection")}
							</MobileField>
							{expanded === release.id && (
								<LibraryAlbumPositionsView
									locale={locale}
									releaseTitle={release.title}
									positions={positionsFor(release)}
									layout="mobile"
								/>
							)}
						</article>
					);
				})}
			</div>
			<div className="min-w-0 max-[900px]:hidden">
				<Table
					key={view}
					aria-label={m("albumsTable")}
					className={`${catalogueStyles.table} table-fixed`}
				>
					<TableHeader>
						{columns.map((column, index) => (
							<Column
								key={column}
								isRowHeader={index === 1}
								className={`${albumColumnWidths[index]} whitespace-normal! wrap-anywhere`}
							>
								{index === 0
									? groupCheckbox(tableTrackIds, bulkSelectionLabel)
									: column}
							</Column>
						))}
					</TableHeader>
					<TableBody
						items={albumRows}
						dependencies={[
							releases,
							artists,
							groupCheckbox,
							hasAudio,
							files,
							expanded,
							filter,
							pageSelection,
						]}
						renderEmptyState={() => (
							<div className={catalogueStyles.empty}>{m("noResults")}</div>
						)}
					>
						{({ id, release, detail }) => {
							if (detail) {
								return (
									<Row
										id={id}
										aria-label={`${m("recording")} · ${release.title}`}
									>
										<Cell
											colSpan={columns.length}
											className="w-auto! min-w-0! max-w-none! px-2! text-left!"
										>
											<LibraryAlbumPositionsView
												locale={locale}
												releaseTitle={release.title}
												positions={positionsFor(release)}
												layout="desktop"
												releaseSelection={selectionControl(
													release.tracks.map((track) => track.id),
													pageSelection,
												)}
											/>
										</Cell>
									</Row>
								);
							}
							const provider = originalProviders.find(
								(item) => item.id === release.providerId,
							);
							const published = release.tracks.filter((track) =>
								Boolean(track.published),
							).length;
							const sourceCount = release.tracks.filter(
								(track) => sourceIds(track).length > 0,
							).length;
							return (
								<Row id={id}>
									<Cell>
										{groupCheckbox(
											release.tracks.map((track) => track.id),
											m("selectAlbumPositions", { album: release.title }),
										)}
									</Cell>
									<Cell>
										<Button
											className={`${catalogueStyles.expander} max-w-full wrap-anywhere`}
											aria-expanded={expanded === release.id}
											onPress={() => onToggleAlbum(release.id)}
										>
											{release.title || m("untitled")}
										</Button>
										<span className={catalogueStyles.sub}>
											{artistReleaseHref(release, artists) ? (
												<a
													href={
														artistReleaseHref(release, artists) ?? undefined
													}
												>
													{m("openRelease")}
												</a>
											) : (
												m("artistNotSpecified")
											)}
										</span>
									</Cell>
									<Cell>
										{release.credits?.length ? (
											release.credits.map((credit, index) => (
												<span key={`${credit.artistId ?? "credit"}-${index}`}>
													{index > 0 && ", "}
													{credit.artistId ? (
														<a href={`#artist/${credit.artistId}`}>
															{credit.credit || credit.name}
														</a>
													) : (
														credit.credit || credit.name
													)}
												</span>
											))
										) : release.artistId ? (
											<a href={`#artist/${release.artistId}`}>
												{originalArtistName(release.artistId, artists)}
											</a>
										) : (
											m("artistNotSpecified")
										)}
									</Cell>
									<Cell>
										{release.year || m("dateNotSpecified")}
										<span className={catalogueStyles.sub}>
											{release.country || m("local")} · {release.catalog || "—"}
										</span>
									</Cell>
									<Cell>{release.tracks.length}</Cell>
									<Cell>{sourceCount}</Cell>
									<Cell>
										{m("audioSummary", {
											count: release.tracks.filter(hasAudio).length,
										})}
										{provider?.tracks.length
											? ` / ${provider.tracks.length}`
											: ""}
									</Cell>
									<Cell>
										{m("publishedCount", {
											published,
											total: release.tracks.length,
										})}
										<span className={catalogueStyles.sub}>
											{provider?.tracks.length === release.tracks.length
												? m("completenessKnown")
												: m("completenessUnknown")}
										</span>
									</Cell>
									<Cell>{provider ? "MusicBrainz" : m("localCollection")}</Cell>
								</Row>
							);
						}}
					</TableBody>
				</Table>
			</div>
		</div>
	);
}
