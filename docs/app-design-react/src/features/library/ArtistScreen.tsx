import { useEffect, useMemo, useRef, useState } from "react";
import type { DeletionOperation } from "../../components/DeletionRequest";
import { DeletionRequest } from "../../components/DeletionRequest";
import { demoGenres } from "../../components/GenreLibrary";
import { useI18n } from "../../i18n/I18nProvider";
import { audioProperties } from "../../metadata/audioProperties";
import { ReleaseMetadataEditor } from "../../metadata/ReleaseMetadataEditor";
import {
	ReleasePublicationDemo,
	type ReleasePublicationDemoProvider,
} from "../publication/ReleasePublicationDemo";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
import {
	ArtistMetadataCard,
	type ArtistMetadataValues,
} from "./ArtistMetadataCard";
import {
	type ArtistDeletionAction,
	type ArtistEditionModel,
	type ArtistHeadingModel,
	type ArtistMissingGroupModel,
	ArtistScreenView,
	type ArtistTagFocus,
} from "./ArtistScreenView";
import {
	availabilityRowClass,
	type CatalogFilter as CatalogFilterValue,
} from "./availabilityRows";
import {
	type CatalogArtist,
	catalogData,
	metadataChanged,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	originalProviders,
	type SourceFiles,
	trackNumberLabel,
} from "./catalogData";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { LibraryMissingEditionDemo } from "./LibraryMissingEditionDemo";
import type { TagScope } from "./TagsComparison";

const libraryHref = "#library";

const releaseFields = (release: OriginalRelease) =>
	release.credits?.some((credit) => credit.artistId) ?? false;

function genreOptions(query: string) {
	const term = query.trim().toLocaleLowerCase("en");
	if (!term) return [];
	const exact = [];
	const partial = [];
	for (const { id, name, sourceName } of demoGenres) {
		const fields = [id, name, sourceName].map((item) =>
			item.toLocaleLowerCase("en"),
		);
		const option = { id, name, source_name: sourceName };
		if (fields.includes(term)) exact.push(option);
		else if (fields.some((field) => field.includes(term))) partial.push(option);
	}
	return [...exact, ...partial];
}

export function ArtistScreen({
	artistId,
	artists,
	releases,
	files,
	query: sharedQuery,
	filter: sharedFilter,
	selected = new Set(),
	onSelect,
	onQueryChange,
	onFilterChange,
	onDeleteArtist,
	onQueueDeletion,
	onUpdateArtist,
	onUpdateRelease,
	initialTags,
	initialReleaseId,
	publication,
	genresLoaded = false,
	onLoadGenres,
}: {
	artistId: string;
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	files: SourceFiles;
	query?: string;
	filter?: CatalogFilterValue;
	selected?: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onQueryChange?: (value: string) => void;
	onFilterChange?: (value: CatalogFilterValue) => void;
	onDeleteArtist?: (artistId: string) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	onUpdateArtist?: (artist: CatalogArtist) => void;
	onUpdateRelease?: (release: OriginalRelease) => void;
	initialTags?: { scope: TagScope; releaseId?: string };
	initialReleaseId?: string;
	publication?: ReleasePublicationDemoProvider;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
}) {
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const [localQuery, setLocalQuery] = useState("");
	const [genreQuery, setGenreQuery] = useState("");
	const [localGenresLoaded, setLocalGenresLoaded] = useState(false);
	const genreSearchResults = useMemo(
		() => genreOptions(genreQuery),
		[genreQuery],
	);
	const [localFilter, setLocalFilter] = useState<CatalogFilterValue>("all");
	const query = sharedQuery ?? localQuery;
	const filter = sharedFilter ?? localFilter;
	const changeQuery = (value: string) => {
		setLocalQuery(value);
		onQueryChange?.(value);
	};
	const changeFilter = (value: CatalogFilterValue) => {
		setLocalFilter(value);
		onFilterChange?.(value);
	};
	const [localArtistUpdates, setLocalArtistUpdates] = useState<
		Record<string, CatalogArtist>
	>({});
	const [localReleaseUpdates, setLocalReleaseUpdates] = useState<
		Record<string, OriginalRelease>
	>({});
	const artist =
		localArtistUpdates[artistId] ??
		artists.find((item) => item.id === artistId);
	const [absentEdition, setAbsentEdition] = useState<Record<string, string>>(
		{},
	);
	const initialTagsTarget = useRef<HTMLDivElement | null>(null);
	useEffect(() => {
		if (!initialTags?.scope) return;
		const target = initialTagsTarget.current;
		if (!target) return;
		target.focus({ preventScroll: true });
		target.scrollIntoView?.({ block: "start" });
	}, [initialTags?.scope, initialTags?.releaseId]);
	const artistReleases = useMemo(
		() =>
			releases
				.map((release) => localReleaseUpdates[release.id] ?? release)
				.filter(
					(release) =>
						release.artistId === artistId ||
						(releaseFields(release) &&
							release.credits?.some((credit) => credit.artistId === artistId)),
				),
		[artistId, localReleaseUpdates, releases],
	);
	const tracks = artistReleases.flatMap((release) => release.tracks);
	const allReleases = releases.map(
		(release) => localReleaseUpdates[release.id] ?? release,
	);
	const ids = tracks.map((track) => track.id);
	const hasAudio = (track: OriginalTrack) =>
		Boolean(track.published) ||
		[
			...new Set([
				...(track.sourceIds ?? []),
				...(track.sourceId ? [track.sourceId] : []),
			]),
		].some((id) => files[id]?.available && files[id]?.locations?.length);
	const selectedCount = ids.filter((id) => selected.has(id)).length;
	const providerFor = (release: OriginalRelease) =>
		originalProviders.find((item) => item.id === release.providerId);
	const providerComplete = (release: OriginalRelease) => {
		const provider = providerFor(release);
		return Boolean(
			provider?.tracks.length &&
				provider.tracks.length === release.tracks.length,
		);
	};
	const missingGroups = catalogData.absentGroups as MissingGroup[];
	const queryTerms = query
		.normalize("NFKC")
		.toLocaleLowerCase()
		.split(/\s+/)
		.filter(Boolean);
	const unrepresentedMissingGroups = missingGroups.filter(
		(group) =>
			group.artistId === artistId &&
			!allReleases.some(
				(release) =>
					(release as OriginalRelease & { releaseGroupId?: string })
						.releaseGroupId === group.groupId,
			),
	);
	const filteredMissingGroups = unrepresentedMissingGroups.filter((group) => {
		if (filter !== "all" && filter !== "missing") return false;
		const searchable =
			`${group.title} ${group.editions.map((edition) => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(" ")}`).join(" ")}`
				.normalize("NFKC")
				.toLocaleLowerCase();
		return queryTerms.every((term) => searchable.includes(term));
	});
	if (!artist)
		return (
			<ArtistScreenView
				locale={locale}
				libraryHref={libraryHref}
				heading={null}
			/>
		);
	const counts = (predicate: (track: OriginalTrack) => boolean) =>
		tracks.filter(predicate).length;
	const expandedReleaseId =
		initialTags?.scope === "release" ? initialTags.releaseId : initialReleaseId;
	const selectedGenres = (artist.genres ?? []).flatMap((genre) => {
		if (!genre.id) return [];
		const known = demoGenres.find((item) => item.id === genre.id);
		const name = known?.name ?? genre.name ?? genre.id;
		return [{ id: genre.id, name, source_name: known?.sourceName ?? name }];
	});
	const saveArtist = async (changes: Partial<ArtistMetadataValues>) => {
		const genre = changes.genre;
		const inherited =
			artist.genreOverride?.inherited ??
			(artist.genres ?? []).filter((item) => !item.id);
		const updated = {
			...artist,
			...(changes.name !== undefined ? { name: changes.name } : {}),
			...(changes.sortName !== undefined
				? { sortName: changes.sortName ?? undefined }
				: {}),
			...(changes.disambiguation !== undefined
				? { disambiguation: changes.disambiguation ?? "" }
				: {}),
			...(changes.tags !== undefined
				? {
						tags: changes.tags
							.split(",")
							.map((tag) => tag.trim())
							.filter(Boolean),
					}
				: {}),
			...(genre
				? {
						genreOverride: { state: genre.state, inherited },
						genres:
							genre.state === "explicit_empty"
								? []
								: [
										...genre.value.map(({ id, name }) => ({ id, name })),
										...inherited,
									],
					}
				: {}),
		};
		if (onUpdateArtist) onUpdateArtist(updated);
		else
			setLocalArtistUpdates((current) => ({
				...current,
				[artist.id]: updated,
			}));
	};
	const saveRelease = (updated: OriginalRelease) => {
		if (onUpdateRelease) onUpdateRelease(updated);
		else
			setLocalReleaseUpdates((current) => ({
				...current,
				[updated.id]: updated,
			}));
	};
	const metadataValues: ArtistMetadataValues = {
		name: artist.name,
		sortName: artist.sortName ?? null,
		disambiguation: artist.disambiguation ?? null,
		tags: artist.tags?.join(", ") ?? "",
		genre: {
			state: selectedGenres.length
				? "value"
				: (artist.genreOverride?.state ?? "absent"),
			value: selectedGenres,
		},
	};
	const heading: ArtistHeadingModel = {
		name: artist.name,
		disambiguation: artist.disambiguation,
		genres: artist.genres?.some((genre) => genre.name)
			? artist.genres.flatMap((genre) => (genre.name ? [genre.name] : []))
			: artist.genreOverride?.state !== "explicit_empty" && artist.genre
				? [artist.genre]
				: [],
		palette: artist.color,
		count: {
			releases: artistReleases.length + unrepresentedMissingGroups.length,
			positions: tracks.length,
			published: counts((track) => Boolean(track.published)),
			missing: unrepresentedMissingGroups.length,
			local: artistReleases.length,
		},
		discographyComplete: artist.discographyComplete,
		includesCachedGroups: !artist.discographyComplete,
		selection: {
			total: ids.length,
			selected: selectedCount,
			onChange: onSelect && ((value) => onSelect(ids, value)),
		},
	};
	const deletion: ArtistDeletionAction = onQueueDeletion
		? {
				kind: "actor",
				node: (
					<ArtistDeletionRequest
						artistId={artist.id}
						artists={artists}
						releases={releases}
						onQueue={onQueueDeletion}
					/>
				),
			}
		: {
				kind: "button",
				isDisabled: !onDeleteArtist,
				onPress: onDeleteArtist ? () => onDeleteArtist(artist.id) : undefined,
			};
	const metadataCard = (
		<ArtistMetadataCard
			key={artist.id}
			locale={locale}
			revision={artist.id}
			values={metadataValues}
			readOnly={[
				{ id: "id", label: m("artistID"), value: artist.id },
				...(
					(
						artist as CatalogArtist & {
							providerLinks?: {
								providerCode: string;
								providerKey: string;
							}[];
						}
					).providerLinks ?? []
				).map((link, index) => ({
					id: `provider-${link.providerCode}-${index}`,
					label: `ID ${link.providerCode}`,
					value: link.providerKey,
				})),
			]}
			genreCaption={
				artist.genres
					?.map((genre) => genre.name)
					.filter(Boolean)
					.join(", ") || artist.genre
			}
			genreCatalog={{
				options: genreSearchResults,
				query: genreQuery,
				onQueryChange: setGenreQuery,
				loading: false,
				catalogError: "",
				onClearCatalogError: () => undefined,
				onRetryCatalog: () => undefined,
				catalogStatusLoading: false,
				catalogStatusError: "",
				bundledCatalogReady: genresLoaded || localGenresLoaded,
				onLoadBundledCatalog: () => {
					if (onLoadGenres) void onLoadGenres();
					else setLocalGenresLoaded(true);
				},
				settingsHref: "#settings",
				onOpenCatalog: () => undefined,
				onRetryStatus: () => undefined,
			}}
			onSave={saveArtist}
		/>
	);
	const editions: ArtistEditionModel[] = artistReleases.map((release) => {
		const durationTotal =
			release.tracks.length > 0 &&
			release.tracks.every((track) => Number.isFinite(track.duration))
				? release.tracks.reduce((sum, track) => sum + (track.duration ?? 0), 0)
				: null;
		const pubCount = release.tracks.filter((track) => track.published).length;
		const provider = providerFor(release);
		const complete = providerComplete(release);
		const changes = release.tracks.filter((track) =>
			metadataChanged(release, track, artists),
		).length;
		const trackIds = release.tracks.map((track) => track.id);
		const everySelected = release.tracks.every((track) =>
			selected.has(track.id),
		);
		const someSelected = release.tracks.some((track) => selected.has(track.id));
		const ariaLabel = `${release.title} · ${release.year} · ${release.catalog || "—"}`;
		const selectTracks = (value: boolean) => onSelect?.(trackIds, value);
		return {
			key: release.id,
			title: release.title,
			year: release.year,
			catalog: release.catalog,
			ariaLabel,
			artwork: {
				palette: release.color || artist.color,
				title: release.title,
			},
			durationSeconds: durationTotal,
			providerText:
				release.provider === "Local"
					? "Local"
					: release.providerId
						? `${release.provider || "MusicBrainz"} · ${release.providerId}`
						: m("localRelease"),
			positionCount: release.tracks.length,
			country: release.country,
			label: release.label,
			publishedCount: pubCount,
			providerTrackCount: complete && provider ? provider.tracks.length : null,
			metadataChangedCount: changes,
			selection: {
				isSelected: release.tracks.length > 0 && everySelected,
				isIndeterminate: someSelected && !everySelected,
				isDisabled: !release.tracks.length || !onSelect,
				onChange: selectTracks,
			},
			trackSelection: {
				isSelected: everySelected,
				isIndeterminate: someSelected && !everySelected,
				isDisabled: !onSelect,
				onChange: selectTracks,
			},
			tracks: release.tracks.map((track) => ({
				id: track.id,
				numberLabel: trackNumberLabel(release, track),
				title: track.title,
				href: `#track/${track.id}`,
				durationMs: track.duration == null ? null : track.duration * 1000,
				properties: audioProperties(
					track.sourceId ? files[track.sourceId] : undefined,
				),
				hasAudio: hasAudio(track),
				sourceCount: new Set([
					...(track.sourceIds ?? []),
					...(track.sourceId ? [track.sourceId] : []),
				]).size,
				publication: track.published
					? {
							state: "published" as const,
							container: track.published.container,
							codec: track.published.codec,
						}
					: { state: "not-published" as const },
				isNewRecording: !track.recordingId,
				fileEvidence: track.observedFile ?? undefined,
				metadataChanged: track.published
					? metadataChanged(release, track, artists)
					: false,
				filterClass: availabilityRowClass(track, filter, hasAudio),
				selection: {
					isSelected: selected.has(track.id),
					isDisabled: !onSelect,
					onChange: (value: boolean) => onSelect?.([track.id], value),
				},
			})),
			editor: (
				<ReleaseMetadataEditor
					key={release.id}
					release={release}
					artists={artists}
					genresLoaded={genresLoaded}
					onLoadGenres={onLoadGenres}
					local={!onUpdateRelease}
					onSave={saveRelease}
				/>
			),
			publication: publication ? (
				<ReleasePublicationDemo
					key={`publication-${release.id}`}
					release={release}
					provider={publication}
				/>
			) : undefined,
			deletion: onQueueDeletion ? (
				<DeletionRequest
					key={`deletion-${release.id}`}
					iconOnly
					triggerLabel={`${m("deleteAlbum")} ${ariaLabel}`}
					release={release}
					onQueue={onQueueDeletion}
				/>
			) : undefined,
		};
	});
	const preparedMissingGroups: ArtistMissingGroupModel[] =
		filteredMissingGroups.map((group) => ({
			key: group.id,
			title: group.title,
			artistName: originalArtistName(group.artistId, artists),
			chooser: (
				<LibraryMissingEditionDemo
					key={group.id}
					locale={locale}
					groupTitle={group.title}
					editions={group.editions}
					selectedEditionKey={absentEdition[group.id] ?? ""}
					onSelectEdition={(editionKey) =>
						setAbsentEdition((value) => ({
							...value,
							[group.id]: editionKey,
						}))
					}
				/>
			),
		}));
	const tagFocus: ArtistTagFocus | undefined = initialTags
		? {
				scope: initialTags.scope === "artist" ? "artist" : "release",
				releaseId: initialTags.releaseId,
				ref: (node: HTMLDivElement | null) => {
					initialTagsTarget.current = node;
				},
			}
		: undefined;
	return (
		<ArtistScreenView
			locale={locale}
			libraryHref={libraryHref}
			heading={heading}
			deletion={deletion}
			metadataCard={metadataCard}
			query={query}
			filter={filter}
			onQueryChange={changeQuery}
			onFilterChange={changeFilter}
			editionsKey={artistId}
			editions={editions}
			expandedReleaseId={expandedReleaseId}
			missingGroups={preparedMissingGroups}
			tagFocus={tagFocus}
		/>
	);
}

type MissingGroup = {
	id: string;
	groupId: string;
	artistId: string;
	title: string;
	editions: {
		id: string;
		year: string;
		country: string;
		format: string;
		disambiguation: string;
		titles: string[];
	}[];
};
