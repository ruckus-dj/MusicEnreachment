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
import type { AbsentCatalogGroup } from "./catalogViewModel";
import {
	type LibraryAlbumCredit,
	type LibraryAlbumDetail,
	LibraryAlbumListView,
	type LibraryAlbumRow,
} from "./LibraryAlbumListView";
import type { LibraryAlbumPosition } from "./LibraryAlbumPositionsView";
import {
	type ArtistSelectionState,
	type LibraryArtistCard,
	LibraryArtistCards,
} from "./LibraryArtistCards";
import { missingEditionChooserProps } from "./LibraryMissingEditionDemo";
import {
	type LibraryTrackCredit,
	LibraryTrackListView,
	type LibraryTrackRow,
} from "./LibraryTrackListView";
import type { LibraryAlbumEntry } from "./libraryCatalogPage";
import {
	type LibraryRowsMessage,
	libraryRowsMessage,
} from "./libraryRowsMessages";

type View = "artists" | "albums" | "tracks";
type CatalogTrack = Omit<OriginalTrack, "published"> & {
	published: boolean | OriginalTrack["published"];
};
type Release = Omit<OriginalRelease, "tracks"> & { tracks: CatalogTrack[] };
type AlbumEntry =
	| { kind: "release"; release: Release }
	| { kind: "missing"; group: AbsentCatalogGroup };
interface Props {
	view: View;
	artists: CatalogArtist[];
	releases: Release[];
	missingCounts: Map<string, number>;
	hasAudio: (track: Release["tracks"][number]) => boolean;
	files: SourceFiles;
	filter?: CatalogFilter;
	/** Ordered, page-sliced album entries; local releases and absent groups. */
	albumEntries?: readonly LibraryAlbumEntry[];
	/** Explicit edition selection per absent group, owned by the host. */
	selectedEditions?: Readonly<Record<string, string>>;
	onSelectEdition?: (groupId: string, editionKey: string) => void;
	expanded: string | null;
	onToggleAlbum: (id: string) => void;
	/** Raw page-scoped selection used to derive artist cards and row controls. */
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
	albumEntries,
	selectedEditions,
	onSelectEdition,
	expanded,
	onToggleAlbum,
	pageSelection,
}: Props) {
	const { locale } = useI18n();
	const m = (
		key: LibraryRowsMessage,
		values?: Record<string, string | number>,
	) => libraryRowsMessage(locale, key, values);
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
				track.sourceId ? undefined : ids.length > 0 ? "unselected" : "missing";
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
	const albumListEntries: AlbumEntry[] =
		albumEntries?.map(
			(entry): AlbumEntry =>
				entry.kind === "release"
					? { kind: "release", release: entry.release }
					: { kind: "missing", group: entry.group },
		) ?? releases.map((release): AlbumEntry => ({ kind: "release", release }));
	const albumTrackIds = albumListEntries.flatMap((entry) =>
		entry.kind === "release"
			? entry.release.tracks.map((track) => track.id)
			: [],
	);
	const albumPageSelection = selectionControl(albumTrackIds, pageSelection);
	const artistName = (id: string | null | undefined) => {
		if (!id) return null;
		return artists.find((artist) => artist.id === id)?.name ?? null;
	};
	const artistCredit = (
		id: string | null | undefined,
		fallbackName?: string | null,
	) => {
		const name = fallbackName || artistName(id);
		if (!name) return null;
		return {
			name,
			...(id ? { href: `#artist/${id}` } : {}),
			joinPhrase: null,
		};
	};
	const releaseCredits = (release: Release): LibraryAlbumCredit[] => {
		const sourceCredits = release.credits ?? [];
		const explicit = sourceCredits.flatMap((credit, index) => {
			const entry = artistCredit(
				credit.artistId,
				credit.credit || credit.name || null,
			);
			return entry
				? [
						{
							...entry,
							joinPhrase:
								credit.joinPhrase ??
								(index < sourceCredits.length - 1 ? ", " : ""),
						},
					]
				: [];
		});
		if (explicit.length) return explicit;
		const fallback = artistCredit(release.artistId);
		return fallback ? [fallback] : [];
	};
	const albums: LibraryAlbumRow[] = albumListEntries.map(
		(entry): LibraryAlbumRow => {
			if (entry.kind === "release") {
				const release = entry.release;
				const provider = originalProviders.find(
					(item) => item.id === release.providerId,
				);
				const credits = releaseCredits(release);
				return {
					id: release.id,
					title: release.title,
					href: artistReleaseHref(release, artists),
					artist: credits.length ? undefined : m("artistNotSpecified"),
					artistCredits: credits,
					date: release.year || m("dateNotSpecified"),
					editionNote: `${release.country || m("local")} · ${release.catalog || "—"}`,
					positionCount: release.tracks.length,
					sourceCount: release.tracks.filter(
						(track) => sourceIds(track).length > 0,
					).length,
					availableCount: release.tracks.filter(hasAudio).length,
					providerPositionCount: provider?.tracks.length ?? null,
					publishedCount: release.tracks.filter((track) =>
						Boolean(track.published),
					).length,
					publicationCoverage: {
						total: release.tracks.length,
						complete:
							Boolean(provider?.tracks.length) &&
							provider?.tracks.length === release.tracks.length,
					},
					provider: provider ? "musicbrainz" : "local",
					selection: selectionControl(
						release.tracks.map((track) => track.id),
						pageSelection,
					),
				};
			}
			const group = entry.group;
			const credit = artistCredit(group.artistId);
			return {
				id: `provider:${group.groupId || group.id}`,
				title: group.title,
				href: null,
				missing: true,
				artist: credit?.name ?? m("artistNotSpecified"),
				artistCredits: credit ? [credit] : [],
				date: null,
				editionNote: null,
				positionCount: null,
				sourceCount: null,
				availableCount: null,
				providerPositionCount: null,
				publishedCount: null,
				provider: "musicbrainz",
				selection: null,
			};
		},
	);
	const expandedEntry = albumListEntries.find((entry) => {
		if (entry.kind === "release") return entry.release.id === expanded;
		return `provider:${entry.group.groupId || entry.group.id}` === expanded;
	});
	const expandedPositionIds =
		expandedEntry?.kind === "release"
			? expandedEntry.release.tracks.map((track) => track.id)
			: [];
	const detail: LibraryAlbumDetail | null = !expandedEntry
		? null
		: expandedEntry.kind === "release"
			? {
					kind: "positions" as const,
					releaseTitle: expandedEntry.release.title,
					positions: positionsFor(expandedEntry.release),
					releaseSelection: selectionControl(
						expandedPositionIds,
						pageSelection,
					),
				}
			: (() => {
					const group = expandedEntry.group;
					const credit = artistCredit(group.artistId);
					const chooser = missingEditionChooserProps({
						locale,
						groupTitle: group.title,
						editions: group.editions,
						selectedEditionKey: selectedEditions?.[group.id] ?? "",
						onSelectEdition: (editionKey) =>
							onSelectEdition?.(group.id, editionKey),
					});
					return {
						kind: "missing-edition" as const,
						groupTitle: chooser.groupTitle,
						editions: chooser.editions,
						editionsStatus: chooser.editionsStatus,
						selectedEditionKey: chooser.selectedEditionKey,
						onSelectEdition: chooser.onSelectEdition,
						tracksStatus: chooser.tracksStatus,
						tracks: chooser.tracks,
						originalReleaseDate: null,
						credits: credit ? [credit] : undefined,
					};
				})();
	return (
		<LibraryAlbumListView
			locale={locale}
			albums={albums}
			expandedId={expanded}
			onToggleExpanded={onToggleAlbum}
			pageSelection={albumPageSelection}
			detail={detail}
		/>
	);
}
