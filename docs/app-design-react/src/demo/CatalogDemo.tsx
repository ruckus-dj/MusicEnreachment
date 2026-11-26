import { useEffect, useState, useSyncExternalStore } from "react";
import { useI18n } from "../app/i18n";
import { ApplicationRouteOutlet } from "../ui/app/ApplicationRouteOutlet";
import { focusView, focusViewById } from "../ui/components/useViewFocus";
import { IncomingWorkspaceView } from "../ui/features/incoming/IncomingWorkspaceView";
import { catalogMessage } from "../ui/features/library/catalogMessages";
import type { LibraryCatalogPageState } from "../ui/features/library/LibraryCatalogPageView";
import type { LibraryCatalogResults } from "../ui/features/library/LibraryCatalogResults";
import { libraryPositionTitleId } from "../ui/features/library/LibraryPositionHeadingView";
import { LibraryReleaseWorkspaceView } from "../ui/features/library/LibraryReleaseWorkspaceView";
import {
	type LibrarySelectionPreviewPosition,
	type LibrarySelectionPreviewState,
	LibrarySelectionPreviewView,
	maxLibraryPreviewPositions,
} from "../ui/features/library/LibrarySelectionPreviewView";
import { LibrarySelectionScreenFrame } from "../ui/features/library/LibrarySelectionScreenFrame";
import {
	libraryPositionHref,
	libraryReleaseHref,
	parseLibraryArtistAddress,
	parseLibraryPositionAddress,
	parseLibraryReleaseAddress,
} from "../ui/features/library/libraryDetailRoute";
import { libraryReleaseMessage } from "../ui/features/library/libraryReleaseMessages";
import { ScreenFeedbackView } from "../ui/shell/ScreenFeedbackView";
import { ShellStartupContent } from "../ui/shell/ShellStartupContent";
import { ShellHomeView } from "../ui/shell/ShellStartupView";
import { useShellPageEffects } from "../ui/shell/useShellPageEffects";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "./fixtures/directoryBrowseDemo";
import { createLocalCatalogDemoState } from "./fixtures/localCatalogDemo";
import {
	appendDemoLocalRecord,
	checkDemoReleases,
	demoRecordingWithCredits,
	findDemoArtists,
	findDemoRecordings,
	localReleaseSuggestions,
	newDemoLocalRecord,
	selectDemoRelease,
} from "./fixtures/localReleaseDemo";
import { IncomingScreen } from "./providers/incoming/IncomingScreen";
import {
	incomingGroupMatchingHref,
	incomingGroupsListHref,
	parseIncomingGroupsRoute,
} from "./providers/incoming/incomingGroupsRoute";
import { createIncomingState } from "./providers/incoming/incomingModel";
import {
	combineLibraryAlbumEntries,
	libraryArtistPositionIds,
	libraryOffsetSlice,
	libraryPageCount,
	libraryRootHref,
	libraryTrackPositionIds,
	parseLibraryRoute,
	sliceTrackReleasePage,
	useLibraryRouteState,
} from "./providers/library";
import { ArtistScreen } from "./providers/library/ArtistScreen";
import {
	applyMetadataPreview,
	type CatalogArtist,
	catalogData,
	type OriginalRelease,
	type OriginalTrack,
	originalFiles,
	originalProviders,
	originalReleases,
	recoveryFixture,
} from "./providers/library/catalogData";
import {
	albumSearchText,
	catalogArtistMatchesForAxes,
	missingCatalogGroupsForAxes,
	normalizeCatalogSearch,
	releaseMatchesFilterAxes,
	trackMatchesFilterAxes,
	trackSearchText,
} from "./providers/library/catalogViewModel";
import type { DeletionOperation } from "./providers/library/DeletionRequest";
import { DemoLibraryReleaseWorkspace } from "./providers/library/DemoLibraryReleaseWorkspace";
import {
	DemoLibraryAlbumRowsProvider,
	libraryCatalogArtistResults,
	libraryCatalogTrackResults,
} from "./providers/library/LibraryCatalogRows";
import { DemoLibraryCatalogBinder } from "./providers/library/LibraryCatalogScreen";
import {
	completeManagedLibraryDeletion,
	managedDeletionObservation,
} from "./providers/library/libraryDeletionModel";
import { SelectionDock } from "./providers/library/SelectionDock";
import { applyAudioUpgradePreview } from "./providers/library/trackActionModel";
import type { ReleaseOverviewScreenProps } from "./providers/library/useReleaseOverviewDemoModel";
import type { TrackScreenProps } from "./providers/library/useTrackScreenDemoProvider";
import { DemoLocalCatalogBinder } from "./providers/local-catalog";
import { releaseDraftToRecord } from "./providers/local-catalog/localCatalogModel";
import { localProviderReleases } from "./providers/local-catalog/localProviderModel";
import {
	DemoLocalReleaseGenreCatalogProvider,
	DemoLocalReleaseGenreLoadProvider,
} from "./providers/local-release/DemoLocalReleaseGenreCatalogProvider";
import { MatchingScreen } from "./providers/matching/MatchingScreen";
import { formatUiNotice, type UiNotice } from "./providers/notices/uiNotice";
import { DemoOperationsBinder } from "./providers/operations/OperationsScreen";
import {
	type DemoPublicationJob,
	useOperationsDemoProvider,
} from "./providers/operations/useOperationsDemoProvider";
import {
	DemoPositionPublication,
	type DemoPositionPublicationSource,
} from "./providers/publication/ContextPublicationPreview";
import {
	type DemoPublicationCatalog,
	type DemoPublicationRow,
	planDemoPublication,
	settleDemoPublication,
} from "./providers/publication/libraryPublicationDemo";
import type { ManagedPathObservation } from "./providers/publication/managedPublicationDeletionModel";
import {
	PublicationPreview,
	type PublicationPreviewMode,
} from "./providers/publication/PublicationPreview";
import {
	captureMetadataPreview,
	isMetadataPreviewFresh,
	type MetadataPreviewToken,
	previewScopeKey,
} from "./providers/publication/previewModel";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "./providers/publication/publicationOutput";
import { defaultPublicationTemplate } from "./providers/settings/publicationTemplateDemo";
import {
	DemoSettingsBinder,
	initialDemoSettings,
} from "./providers/settings/SettingsScreen";
import { SetupScreen } from "./providers/setup/SetupScreen";
import {
	initialSourceRoots,
	SourcesScreen,
} from "./providers/sources/SourcesScreen";
import {
	demoSourcesHref,
	parseDemoSourceRoute,
} from "./providers/sources/sourceDemoRoute";
import { removeRootFromInventory } from "./providers/sources/sourceInventory";
import {
	initialSourceProcessingState,
	resetSourceProcessingQueue,
	sourceProcessingLocks,
} from "./providers/sources/sourceProcessingModel";
import { useSourceScanDemoProvider } from "./providers/sources/useSourceScanDemoProvider";

const subscribeRoute = (listener: () => void) => {
	let observedHash = window.location.hash;
	const notifyForCurrentHash = () => {
		if (observedHash === window.location.hash) return;
		observedHash = window.location.hash;
		listener();
	};
	window.addEventListener("hashchange", notifyForCurrentHash);
	window.addEventListener("popstate", notifyForCurrentHash);
	return () => {
		window.removeEventListener("hashchange", notifyForCurrentHash);
		window.removeEventListener("popstate", notifyForCurrentHash);
	};
};

function openIncomingFromHome() {
	window.location.hash = incomingGroupsListHref();
}

type Track = (typeof catalogData.releases)[number]["tracks"][number];
type PublicationJob = DemoPublicationJob & { plan: DemoPublicationRow };
type InlinePublicationPreview = {
	originRoute: string;
	mode: PublicationPreviewMode;
	ids: string[];
	token: MetadataPreviewToken;
	originControl: HTMLElement | null;
};
type PreviewFocusReturn = {
	control: HTMLElement | null;
	originRoute: string;
};

const recordingIdentity = (track: OriginalTrack) =>
	track.localRecordingId
		? `local:${track.localRecordingId}`
		: track.recordingId
			? `musicbrainz:${track.recordingId}`
			: null;

export function CatalogDemo({
	onIncomingCount,
}: {
	onIncomingCount?: (count: number) => void;
} = {}) {
	const { locale } = useI18n();
	const msg = (
		key: Parameters<typeof catalogMessage>[1],
		values?: Record<string, string | number>,
	) => catalogMessage(locale, key, values);
	const [localProvider, setLocalProvider] = useState(
		createLocalCatalogDemoState,
	);
	const [incoming, setIncoming] = useState(createIncomingState);
	const incomingCount = incoming.groups.filter(
		(group) => !group.processed,
	).length;
	useEffect(() => {
		onIncomingCount?.(incomingCount);
	}, [incomingCount, onIncomingCount]);
	const [settings, setSettings] = useState(initialDemoSettings);
	const publicationDestination: PublicationDestination = {
		outputDirectory:
			settings.currentOutput ?? fixturePublicationDestination.outputDirectory,
		format: settings.publicationFormat || fixturePublicationDestination.format,
	};
	const loadGenres = async () => {
		setSettings((current) => ({ ...current, genresLoaded: true }));
	};
	const [setupStep, setSetupStep] = useState(0);
	const [files, setFiles] = useState(() => structuredClone(originalFiles));
	const [sourceRoots, setSourceRoots] = useState(() =>
		structuredClone(initialSourceRoots),
	);
	const [sourceProcessing, setSourceProcessing] = useState(() =>
		structuredClone(initialSourceProcessingState),
	);
	const sourceScans = useSourceScanDemoProvider({
		files,
		workDirectory: settings.workDirectory,
		updateRoots: setSourceRoots,
		updateProcessing: setSourceProcessing,
	});
	const hasAudio = (track: {
		published: unknown;
		sourceIds?: string[];
		sourceId?: string | null;
	}) =>
		Boolean(track.published) ||
		[
			...new Set([
				...(track.sourceIds ?? []),
				...(track.sourceId ? [track.sourceId] : []),
			]),
		].some((id) => files[id]?.available && files[id]?.locations?.length);
	const routePath = useSyncExternalStore(
		subscribeRoute,
		() => {
			const hash = window.location.hash.slice(1);
			return hash === "publication-list" ? "/library" : hash;
		},
		() => "/library",
	);
	const route = routePath;
	const [routeName] = route.split("?", 1);
	const incomingRoute = parseIncomingGroupsRoute(route);
	const artistAddress = parseLibraryArtistAddress(route);
	const {
		view,
		query,
		filter,
		page,
		offset,
		publication,
		availability,
		searchInput,
		isLibraryRoute,
		setView,
		setFilter,
		setPage,
		setSearch,
		reset: resetLibrarySearch,
	} = useLibraryRouteState(route);
	const headingRoute = artistAddress
		? `/library/artists/${artistAddress.artistId}`
		: routeName === "/library" ||
				routeName === "/settings" ||
				routeName === "/operations" ||
				routeName === "/own-catalog"
			? routeName
			: route;
	const [releases, setReleases] = useState(() =>
		structuredClone(originalReleases),
	);
	const [artists, setArtists] = useState<CatalogArtist[]>(() =>
		structuredClone(catalogData.artists),
	);
	const providers = [
		...originalProviders,
		...localProviderReleases(localProvider),
	];
	const providerArtists = [
		...artists,
		...localProvider.items.artists
			.filter((artist) => artist.provider === "Local")
			.map((artist) => ({
				id: artist.id,
				name: artist.name ?? "",
				sortName: artist.sortName,
				genre: msg("localCatalog"),
				color: "slate",
			})),
	];
	const setImportedReleases = (nextReleases: OriginalRelease[]) => {
		const localProviderKeys = new Set(
			providers
				.filter((release) => release.provider === "Local")
				.map((release) => release.providerId ?? release.id),
		);
		const importedArtistIds = new Set(
			nextReleases
				.filter(
					(release) =>
						release.providerId && localProviderKeys.has(release.providerId),
				)
				.flatMap((release) => [
					release.artistId,
					...(release.credits ?? []).map((credit) => credit.artistId ?? null),
					...release.tracks.flatMap((track) => [
						track.artistId,
						...(track.recordingCredits ?? []).map((credit) => credit.artistId),
					]),
				])
				.filter((id): id is string => Boolean(id)),
		);
		setArtists((current) => {
			const known = new Set(current.map((artist) => artist.id));
			return [
				...current,
				...providerArtists.filter(
					(artist) => importedArtistIds.has(artist.id) && !known.has(artist.id),
				),
			];
		});
		setReleases(nextReleases);
	};
	const fixture = {
		...catalogData,
		artists,
		releases: releases.map((release) => ({
			...release,
			tracks: release.tracks.map((track) => ({
				...track,
				sourceIds: track.sourceIds ?? [],
				published: Boolean(track.published),
			})),
		})),
	};
	const [selectionPreview, setSelectionPreview] = useState<{
		positions: LibrarySelectionPreviewPosition[];
		state: LibrarySelectionPreviewState;
	} | null>(null);
	const [positionNotice, setPositionNotice] = useState<{
		releaseId: string;
		positionId: string;
		notice: UiNotice;
	} | null>(null);
	const [inlinePreview, setInlinePreview] =
		useState<InlinePublicationPreview | null>(null);
	const [previewFocusReturn, setPreviewFocusReturn] =
		useState<PreviewFocusReturn | null>(null);
	useEffect(() => {
		setSelectionPreview(null);
		setPositionNotice(null);
		setInlinePreview(null);
	}, [route]);
	useEffect(() => {
		if (!previewFocusReturn) return;
		const { control, originRoute } = previewFocusReturn;
		setPreviewFocusReturn(null);
		if (originRoute !== route || window.location.hash.slice(1) !== originRoute)
			return;
		if (control?.isConnected) {
			focusView(control);
			if (document.activeElement === control) return;
		}
		focusViewById(libraryPositionTitleId);
	}, [previewFocusReturn, route]);
	const [deletions, setDeletions] = useState<DeletionOperation[]>([]);
	const [publicationJobs, setPublicationJobs] = useState<PublicationJob[]>([]);
	const publicationCatalog: DemoPublicationCatalog = {
		releases,
		files,
		artists,
		destination: publicationDestination,
		hasCustomTemplate: Boolean(
			settings.publicationTemplate &&
				settings.publicationTemplate !== defaultPublicationTemplate,
		),
		inventoryVersion: incoming.inventoryVersion,
	};
	const queuePublications = (plans: readonly DemoPublicationRow[]) => {
		const jobs = plans.flatMap((plan) => {
			const release = releases.find(({ id }) => id === plan.release_id);
			const track = release?.tracks.find(({ id }) => id === plan.position_id);
			return track
				? [
						{
							id: `publication-${crypto.randomUUID()}`,
							releaseId: plan.release_id,
							positionId: plan.position_id,
							title: track.title,
							createdAt: Date.now(),
							plan,
						},
					]
				: [];
		});
		setPublicationJobs((current) => [...current, ...jobs]);
	};
	const [residualDeletions, setResidualDeletions] = useState(
		() => new Map<DeletionOperation, DeletionOperation>(),
	);
	const queueDeletion = (operation: DeletionOperation) => {
		setDeletions((previous) => [...previous, operation]);
		window.location.hash = "/operations";
	};
	const operationsProvider = useOperationsDemoProvider({
		deletions,
		publicationJobs,
		settlePublication: (job) => {
			const queued = publicationJobs.find(({ id }) => id === job.id);
			if (!queued) return false;
			const result = settleDemoPublication(publicationCatalog, queued.plan);
			if (!result) return false;
			setReleases((current) =>
				current.map((release) =>
					release.id !== job.releaseId
						? release
						: {
								...release,
								tracks: release.tracks.map((track) =>
									track.id !== job.positionId
										? track
										: {
												...track,
												published: result,
												metadataBaseline: result.tags,
												metadataBaselineExtras: {
													coverHash: result.coverHash ?? null,
													lyrics: result.lyrics ?? null,
												},
											},
								),
							},
				),
			);
			return true;
		},
		settleDeletion: (operation) => completeDeletion(operation),
		recover: (snapshot) => {
			const currentOutput = settings.currentOutput ?? "/srv/music";
			if (
				!snapshot.path.startsWith(`${currentOutput.replace(/\/$/, "")}/`) ||
				!releases.some((release) =>
					release.tracks.some((track) => track.id === recoveryFixture.trackId),
				)
			)
				return false;
			setReleases((previous) =>
				previous.map((release) => ({
					...release,
					tracks: release.tracks.map((track) =>
						track.id === recoveryFixture.trackId
							? {
									...track,
									published: structuredClone(snapshot),
									recovered: true,
									metadataBaseline: structuredClone(snapshot.tags),
									metadataBaselineExtras: {
										coverHash: snapshot.coverHash ?? null,
										lyrics: snapshot.lyrics ?? null,
									},
								}
							: track,
					),
				})),
			);
			return true;
		},
	});
	const completeDeletion = (operation: DeletionOperation) => {
		const confirmedOperation = residualDeletions.get(operation) ?? operation;
		const tracks = releases.flatMap((release) => release.tracks);
		const selectedTracks = new Set(confirmedOperation.libraryTrackIds);
		// Fixtures supply observations, not real unlink or database commit results.
		const paths = Object.fromEntries(
			confirmedOperation.paths.map((path) => {
				const owners = tracks.filter((track) => track.published?.path === path);
				const observation = owners[0]?.observedFile;
				let state: ManagedPathObservation = "unknown";
				if (owners.some((track) => !selectedTracks.has(track.id)))
					state = "replaced";
				else if (owners.length) state = managedDeletionObservation(observation);
				return [path, { state, deleteSucceeded: state === "present" }];
			}),
		);
		const next = completeManagedLibraryDeletion(
			confirmedOperation,
			releases,
			artists,
			{
				paths,
				metadataCommitSucceeded: true,
			},
		);
		if (next.status === "stale") return next.status;
		setReleases(next.releases);
		setArtists(next.artists);
		if (next.status !== "completed") {
			setResidualDeletions((previous) =>
				new Map(previous).set(operation, next.residualOperation),
			);
			if (
				next.residualOperation.paths.length < confirmedOperation.paths.length
			) {
				setIncoming((previous) => ({
					...previous,
					plan: null,
					inventoryVersion: previous.inventoryVersion + 1,
				}));
			}
			return next.status;
		}
		setResidualDeletions((previous) => {
			const updated = new Map(previous);
			updated.delete(operation);
			return updated;
		});
		setDeletions((previous) => previous.filter((item) => item !== operation));
		setIncoming((previous) => ({
			...previous,
			plan: null,
			inventoryVersion: previous.inventoryVersion + 1,
		}));
		setSelected(
			(previous) =>
				new Set(
					[...previous].filter((id) =>
						next.releases.some((release) =>
							release.tracks.some((track) => track.id === id),
						),
					),
				),
		);
		return next.status;
	};
	useShellPageEffects({
		focusKey: headingRoute,
		titleKey: route,
		titleSuffix: msg("documentTitleSuffix"),
		scrollResetKey: incomingRoute ? routeName : undefined,
	});
	const [selected, setSelected] = useState<Set<string>>(() => new Set());
	const [expanded, setExpanded] = useState<string | null>(null);
	const [editions, setEditions] = useState<Record<string, string>>({});
	const normalized = normalizeCatalogSearch(query);
	const filterAxes = { publication, availability };
	const matchesTrack = (track: Track) =>
		trackMatchesFilterAxes(track, filterAxes, hasAudio);
	const matchesRelease = (tracks: Track[]) =>
		releaseMatchesFilterAxes(tracks, filterAxes, hasAudio);
	const missingGroups = missingCatalogGroupsForAxes(
		fixture.absentGroups,
		fixture.releases,
		fixture.artists,
		query,
		filterAxes,
	);
	const unrepresentedMissingGroups = missingCatalogGroupsForAxes(
		fixture.absentGroups,
		fixture.releases,
		fixture.artists,
		"",
		{ publication: "all", availability: "all" },
	);
	const visibleArtists = fixture.artists.filter((artist) =>
		catalogArtistMatchesForAxes(
			artist,
			fixture.releases,
			missingGroups,
			query,
			filterAxes,
			(track) => hasAudio(track as Track),
		),
	);
	const visibleReleaseIds = new Set(
		fixture.releases
			.filter(
				(release) =>
					normalizeCatalogSearch(
						albumSearchText(release, fixture.artists),
					).includes(normalized) && matchesRelease(release.tracks),
			)
			.map((release) => release.id),
	);
	const orderedVisibleReleases = releases.filter((release) =>
		visibleReleaseIds.has(release.id),
	);
	const visibleTrackIds = new Set(
		fixture.releases.flatMap((release) =>
			release.tracks
				.filter(
					(track) =>
						normalizeCatalogSearch(
							trackSearchText(track, release, fixture.artists),
						).includes(normalized) && matchesTrack(track),
				)
				.map((track) => track.id),
		),
	);
	const visibleTrackRefs = releases.flatMap((release) =>
		release.tracks
			.filter((track) => visibleTrackIds.has(track.id))
			.map((track) => ({ release, track })),
	);
	const albumEntries = combineLibraryAlbumEntries(
		orderedVisibleReleases,
		missingGroups,
	);
	const libraryTotalItems =
		view === "artists"
			? visibleArtists.length
			: view === "albums"
				? albumEntries.length
				: visibleTrackRefs.length;
	const totalPages = libraryPageCount(libraryTotalItems);
	const currentPage = Math.min(page, totalPages);
	const pageArtists = libraryOffsetSlice(visibleArtists, offset);
	const pageAlbumEntries = libraryOffsetSlice(albumEntries, offset);
	const pageAlbumReleases = pageAlbumEntries.flatMap((entry) =>
		entry.kind === "release" ? [entry.release] : [],
	);
	const pageTracks = libraryOffsetSlice(visibleTrackRefs, offset);
	const pageTrackReleases = sliceTrackReleasePage(releases, pageTracks);
	const visiblePositionIds = new Set(
		view === "artists"
			? libraryArtistPositionIds(pageArtists, releases)
			: view === "albums"
				? pageAlbumReleases.flatMap((release) =>
						release.tracks.map((track) => track.id),
					)
				: libraryTrackPositionIds(pageTracks),
	);
	useEffect(() => {
		if (!isLibraryRoute) return;
		// Wait for the hook to adopt the whole route before clamping its offset.
		const adopted = parseLibraryRoute(route);
		if (
			adopted.view !== view ||
			adopted.query !== query ||
			adopted.offset !== offset ||
			adopted.publication !== publication ||
			adopted.availability !== availability
		)
			return;
		if (page > totalPages) setPage(totalPages, totalPages);
	}, [
		availability,
		isLibraryRoute,
		offset,
		page,
		publication,
		query,
		route,
		setPage,
		totalPages,
		view,
	]);
	const update = (ids: string[], checked: boolean) => {
		setSelected((previous) => {
			const next = new Set(previous);
			for (const id of ids) {
				if (checked) next.add(id);
				else next.delete(id);
			}
			return next;
		});
	};
	const openSelectionPreview = (ids: string[]) => {
		const selectedIds = new Set(ids);
		const positions = releases.flatMap((release) =>
			release.tracks
				.filter((track) => selectedIds.has(track.id))
				.map((track) => ({
					release_id: release.id,
					position_id: track.id,
					title: track.title,
					release_title: release.title,
				})),
		);
		setSelectionPreview({
			positions,
			state:
				positions.length > maxLibraryPreviewPositions
					? { status: "limit" }
					: {
							status: "ready",
							rows: planDemoPublication(publicationCatalog, positions),
						},
		});
	};
	const openPreview = (
		ids: Iterable<string>,
		mode: PublicationPreviewMode = "metadata",
	) => {
		const frozenIds = [...ids];
		const activeElement = document.activeElement;
		setInlinePreview({
			originRoute: route,
			mode,
			ids: frozenIds,
			token: captureMetadataPreview(
				incoming.inventoryVersion,
				previewScopeKey(route, frozenIds),
				frozenIds,
			),
			originControl:
				activeElement instanceof HTMLElement &&
				activeElement !== document.body &&
				activeElement !== document.documentElement
					? activeElement
					: null,
		});
	};
	const closeInlinePreview = (restoreFocus: boolean) => {
		if (restoreFocus && inlinePreview) {
			setPreviewFocusReturn({
				control: inlinePreview.originControl,
				originRoute: inlinePreview.originRoute,
			});
		}
		setInlinePreview(null);
	};
	const applyPreview = (token: MetadataPreviewToken) => {
		if (!inlinePreview) return false;
		const activeScope = previewScopeKey(
			inlinePreview.originRoute,
			inlinePreview.ids,
		);
		const frozenIds = new Set(inlinePreview.token.selectedIds);
		if (
			window.location.hash.slice(1) !== inlinePreview.originRoute ||
			token.selectedIds.length === 0 ||
			token.selectedIds.some((id) => !frozenIds.has(id)) ||
			!isMetadataPreviewFresh(token, incoming.inventoryVersion, activeScope)
		)
			return false;
		setReleases((previous) =>
			inlinePreview.mode === "audioUpgrade"
				? applyAudioUpgradePreview(previous, token.selectedIds, files)
				: applyMetadataPreview(
						previous,
						new Set(token.selectedIds),
						files,
						artists,
						publicationDestination,
					),
		);
		return true;
	};
	const selectionPreviewDialog = selectionPreview && (
		<LibrarySelectionPreviewView
			locale={locale}
			positions={selectionPreview.positions}
			state={selectionPreview.state}
			onClose={() => setSelectionPreview(null)}
		/>
	);
	if (!routeName || routeName === "/")
		return (
			<ShellHomeView
				locale={locale}
				sources={{ kind: "ready", count: sourceRoots.length }}
				sourcesHref={demoSourcesHref}
				onOpenIncoming={openIncomingFromHome}
			/>
		);
	if (artistAddress) {
		const artistId = artistAddress.artistId;
		return (
			<LibrarySelectionScreenFrame>
				<ApplicationRouteOutlet
					key={artistId}
					route="library-artist"
					provider={ArtistScreen}
					providerProps={{
						artistId,
						artists,
						releases,
						files,
						genresLoaded: settings.genresLoaded,
						onLoadGenres: loadGenres,
						selected,
						onSelect: update,
						onQueueDeletion: queueDeletion,
						onUpdateArtist: (updated) =>
							setArtists((current) =>
								current.map((artist) =>
									artist.id === updated.id ? updated : artist,
								),
							),
					}}
				/>
				<SelectionDock
					selected={selected}
					releases={releases}
					onSelect={update}
					onPreview={openSelectionPreview}
					onQueueDeletion={queueDeletion}
				/>
				{selectionPreviewDialog}
			</LibrarySelectionScreenFrame>
		);
	}
	const positionAddress = parseLibraryPositionAddress(route);
	const releaseAddress = parseLibraryReleaseAddress(route);
	const address = positionAddress ?? releaseAddress;
	if (address) {
		const release = releases.find((item) => item.id === address.releaseId);
		if (!release)
			return (
				<LibraryReleaseWorkspaceView
					locale={locale}
					selection={{ kind: "unavailable" }}
					overview={{ libraryHref: libraryRootHref(), heading: null }}
					positions={[]}
				/>
			);
		const track = positionAddress
			? release.tracks.find((item) => item.id === positionAddress.positionId)
			: undefined;
		const missingPosition =
			positionAddress && !track
				? {
						message: libraryReleaseMessage(locale, "positionNotFound"),
						releaseHref: libraryReleaseHref(release.id),
						releaseLabel: libraryReleaseMessage(locale, "fullRelease"),
					}
				: undefined;
		const overview: ReleaseOverviewScreenProps = {
			release,
			artists,
			files,
			selected,
			onSelect: update,
			onUpdateRelease: (updated) =>
				setReleases((current) =>
					current.map((item) => (item.id === updated.id ? updated : item)),
				),
			onQueueDeletion: queueDeletion,
			publication: {
				catalog: publicationCatalog,
				pending: publicationJobs.filter((job) => {
					const state = operationsProvider.publicationStates?.get(job.id);
					return !state || state === "queued" || state === "running";
				}),
				onQueue: queuePublications,
			},
			genresLoaded: settings.genresLoaded,
			onLoadGenres: loadGenres,
		};
		let position: TrackScreenProps | null = null;
		if (track) {
			const identity = recordingIdentity(track);
			const activePositionNotice =
				positionNotice &&
				positionNotice.releaseId === release.id &&
				positionNotice.positionId === track.id
					? positionNotice.notice
					: null;
			const activePreview =
				inlinePreview && inlinePreview.originRoute === route
					? inlinePreview
					: null;
			const contextualPublication: DemoPositionPublicationSource | undefined =
				activePreview
					? undefined
					: {
							key: `${release.id}:${track.id}`,
							Component: DemoPositionPublication,
							props: {
								destination: publicationDestination,
								release,
								positionId: track.id,
								state: incoming,
								files,
								releases,
								providers,
								artists: providerArtists,
								onReleases: setImportedReleases,
								onSuccess: (notice) => {
									setPositionNotice({
										releaseId: release.id,
										positionId: track.id,
										notice,
									});
								},
							},
						};
			const inlinePublicationPreview = activePreview
				? {
						key: `${activePreview.token.scopeKey}:${activePreview.mode}`,
						Component: PublicationPreview,
						props: {
							mode: activePreview.mode,
							destination: publicationDestination,
							token: activePreview.token,
							inventoryVersion: incoming.inventoryVersion,
							scopeKey: previewScopeKey(
								activePreview.originRoute,
								activePreview.ids,
							),
							files,
							releases,
							artists,
							onApply: applyPreview,
							onBack: () => closeInlinePreview(true),
						},
					}
				: undefined;
			position = {
				files,
				providers,
				artists,
				publicationPreviewNotice: activePositionNotice
					? formatUiNotice(locale, activePositionNotice)
					: undefined,
				contextualPublication,
				inlinePublicationPreview,
				onQueueDeletion: queueDeletion,
				release,
				track,
				genresLoaded: settings.genresLoaded,
				onLoadGenres: loadGenres,
				onRecordingChange: (updateTrack) => {
					closeInlinePreview(false);
					setReleases((previous) =>
						previous.map((item) => ({
							...item,
							tracks: item.tracks.map((candidate) =>
								(identity && recordingIdentity(candidate) === identity) ||
								(!identity && candidate.id === track.id)
									? updateTrack(candidate)
									: candidate,
							),
						})),
					);
				},
				onMutate: (updateTrack) => {
					closeInlinePreview(false);
					setReleases((previous) =>
						previous.map((item) => ({
							...item,
							tracks: item.tracks.map((candidate) =>
								candidate.id === track.id ? updateTrack(candidate) : candidate,
							),
						})),
					);
				},
				onAudioUpgrade: () => {
					openPreview([track.id], "audioUpgrade");
				},
				onMetadataPreview: (ids) => {
					openPreview(ids);
				},
				onSource: (id) => {
					closeInlinePreview(false);
					setReleases((previous) =>
						previous.map((item) => ({
							...item,
							tracks: item.tracks.map((candidate) =>
								candidate.id === track.id &&
								(
									candidate.sourceIds ??
									(candidate.sourceId ? [candidate.sourceId] : [])
								).includes(id)
									? { ...candidate, sourceId: id }
									: candidate,
							),
						})),
					);
				},
			};
		}
		return (
			<LibrarySelectionScreenFrame dockSpace={!track && !missingPosition}>
				<DemoLibraryReleaseWorkspace
					overview={overview}
					position={position}
					notFound={missingPosition}
				/>
				{!track && !missingPosition && (
					<>
						<SelectionDock
							selected={selected}
							visiblePositionIds={
								new Set(release.tracks.map((track) => track.id))
							}
							releases={releases}
							onSelect={update}
							onPreview={openSelectionPreview}
							onQueueDeletion={queueDeletion}
						/>
						{selectionPreviewDialog}
					</>
				)}
			</LibrarySelectionScreenFrame>
		);
	}
	if (routeName === "/own-catalog")
		return (
			<ApplicationRouteOutlet
				route="own-catalog"
				provider={DemoLocalCatalogBinder}
				providerProps={{
					state: localProvider,
					onLoadGenres: loadGenres,
					onState: setLocalProvider,
					genresLoaded: settings.genresLoaded,
				}}
			/>
		);
	if (incomingRoute?.view === "list")
		return (
			<IncomingScreen
				destination={publicationDestination}
				state={incoming}
				onState={setIncoming}
				files={files}
				providers={providers}
				roots={sourceRoots}
				artists={providerArtists}
				releases={releases}
				onReleases={setImportedReleases}
			/>
		);
	if (
		incomingRoute?.view === "matching" &&
		incomingRoute.creatingLocalRelease
	) {
		const { groupId } = incomingRoute;
		const group = incoming.groups.find((item) => item.id === groupId);
		if (!group)
			return (
				<MatchingScreen
					groupId={groupId}
					state={incoming}
					files={files}
					releases={releases}
					providers={providers}
				/>
			);
		const returnToMatching = (
			selection: { prepareReleaseId?: string; localReleaseId?: string } = {},
			releaseId?: string,
		) => {
			window.location.replace(
				incomingGroupMatchingHref(group.id, releaseId, selection),
			);
		};
		const suggested = localReleaseSuggestions(group, files, providerArtists);
		return (
			<DemoLocalReleaseGenreLoadProvider
				loaded={settings.genresLoaded}
				onLoad={loadGenres}
			>
				<IncomingWorkspaceView
					creation={{
						kind: "editor",
						key: group.id,
						editor: {
							locale,
							incomingLabel: group.title,
							suggestedArtist: suggested.artist,
							suggestedTitle: suggested.title,
							genreCatalogProvider: DemoLocalReleaseGenreCatalogProvider,
							artists: localProvider.items.artists,
							recordings: localProvider.items.recordings.map((recording) =>
								demoRecordingWithCredits(
									recording,
									localProvider.items.artists,
								),
							),
							checkRelease: async (artist, title) =>
								checkDemoReleases(providers, providerArtists, artist, title),
							findRecordings: async (query) =>
								findDemoRecordings(localProvider.items.recordings, query).map(
									(recording) =>
										demoRecordingWithCredits(
											recording,
											localProvider.items.artists,
										),
								),
							findArtists: async (query) =>
								findDemoArtists(localProvider.items.artists, query),
							saveArtist: (data) => {
								const artist = newDemoLocalRecord(data);
								setLocalProvider((current) =>
									appendDemoLocalRecord(current, "artists", artist),
								);
								return artist;
							},
							saveRecording: (data) => {
								const recording = newDemoLocalRecord({
									...data,
									artistIds: data.credits?.map((credit) => credit.artistId),
								});
								setLocalProvider((current) =>
									appendDemoLocalRecord(current, "recordings", recording),
								);
								return recording;
							},
							onUseRelease: (id) => {
								const candidate = providers.find((item) => item.id === id);
								if (!candidate) return;
								if (candidate.provider !== "Local") {
									returnToMatching(
										{
											prepareReleaseId: candidate.providerId ?? candidate.id,
										},
										incomingRoute.releaseId,
									);
									return;
								}
								setIncoming((current) =>
									selectDemoRelease(current, group.id, candidate),
								);
								returnToMatching({ localReleaseId: candidate.id });
							},
							onSave: (draft) => {
								const release = releaseDraftToRecord(
									draft,
									undefined,
									localProvider.items.artists,
								);
								setLocalProvider((current) =>
									appendDemoLocalRecord(current, "releases", release),
								);
								setIncoming((current) =>
									selectDemoRelease(
										current,
										group.id,
										{ id: release.id, title: draft.title },
										true,
									),
								);
								returnToMatching({ localReleaseId: release.id });
							},
							onCancel: () =>
								window.location.replace(
									incomingGroupMatchingHref(group.id, incomingRoute.releaseId, {
										localReleaseId: incomingRoute.localReleaseId,
										prepareReleaseId: incomingRoute.prepareReleaseId,
									}),
								),
						},
					}}
					matching={null}
				/>
			</DemoLocalReleaseGenreLoadProvider>
		);
	}
	if (incomingRoute?.view === "matching")
		return (
			<MatchingScreen
				destination={publicationDestination}
				groupId={incomingRoute.groupId}
				state={incoming}
				onState={setIncoming}
				files={files}
				providers={providers}
				artists={providerArtists}
				releases={releases}
				onReleases={setImportedReleases}
				createLocalReleaseHref={incomingGroupMatchingHref(
					incomingRoute.groupId,
					incomingRoute.releaseId,
					{ ...incomingRoute, creatingLocalRelease: true },
				)}
				prepareReleaseId={incomingRoute.prepareReleaseId}
				releaseId={incomingRoute.releaseId}
				localReleaseId={incomingRoute.localReleaseId}
				onSelectProviderRelease={(releaseId) => {
					window.location.hash = incomingGroupMatchingHref(
						incomingRoute.groupId,
						releaseId,
					);
				}}
				onSelectLocalRelease={(localReleaseId) => {
					window.location.hash = incomingGroupMatchingHref(
						incomingRoute.groupId,
						undefined,
						{ localReleaseId },
					);
				}}
			/>
		);
	if (routeName === "/settings")
		return (
			<>
				<ApplicationRouteOutlet
					route="settings"
					provider={DemoSettingsBinder}
					providerProps={{
						settings,
						sourceRoots: sourceRoots.map((root) => root.path),
						browseDirectory: browseDemoDirectory,
						resolveDirectoryPath: demoDirectoryStart,
						onSettings: setSettings,
						workDirectoryBusy:
							sourceProcessingLocks(sourceProcessing).blocksWorkDirectoryChange,
						onOutputReset: () => {
							setSourceProcessing((previous) =>
								resetSourceProcessingQueue(previous),
							);
							operationsProvider.reset();
							setPublicationJobs([]);
							setReleases((previous) =>
								previous.map((release) => ({
									...release,
									tracks: release.tracks.map((track) => ({
										...track,
										published: null,
										metadataBaseline: undefined,
										metadataBaselineExtras: undefined,
									})),
								})),
							);
							setDeletions([]);
							setIncoming((previous) => ({
								...previous,
								plan: null,
								inventoryVersion: previous.inventoryVersion + 1,
							}));
						},
					}}
				/>
			</>
		);
	if (routeName === "/setup")
		return (
			<ShellStartupContent
				locale={locale}
				facts={{
					kind: "setup",
					setup: {
						component: SetupScreen,
						props: {
							settings,
							onSettings: setSettings,
							step: setupStep,
							onStepChange: setSetupStep,
						},
					},
				}}
			/>
		);
	const sourceRoute = parseDemoSourceRoute(routeName);
	if (sourceRoute) {
		const sourceScreen = (
			<SourcesScreen
				roots={sourceRoots}
				onRoots={setSourceRoots}
				files={files}
				selectedRootId={
					sourceRoute.view === "list" ? undefined : sourceRoute.rootId
				}
				selectedLocationId={
					sourceRoute.view === "location" ? sourceRoute.locationId : undefined
				}
				workDirectory={settings.workDirectory}
				toolsPath={settings.toolsPath}
				currentOutput={settings.currentOutput ?? "/srv/music"}
				processing={sourceProcessing}
				scans={sourceScans}
				onProcessingState={setSourceProcessing}
				onRemove={(id) => {
					const next = removeRootFromInventory(files, releases, id);
					setIncoming((previous) => {
						const updated = structuredClone(previous);
						for (const group of updated.groups) {
							group.fileIds = group.fileIds.filter(
								(fileId) => !next.orphaned.has(fileId),
							);
							for (const assignments of Object.values(group.drafts))
								for (const assignment of Object.values(assignments)) {
									assignment.sourceIds = assignment.sourceIds.filter(
										(fileId) => !next.orphaned.has(fileId),
									);
									if (
										assignment.selectedSourceId &&
										next.orphaned.has(assignment.selectedSourceId)
									)
										assignment.selectedSourceId = null;
									for (const fileId of next.orphaned)
										delete assignment.sourceMeta[fileId];
								}
						}
						updated.inventoryVersion++;
						return updated;
					});
					setFiles(next.files);
					setReleases(next.releases);
					setSourceRoots((previous) =>
						previous.filter((root) => root.id !== id),
					);
				}}
			/>
		);
		return sourceScreen;
	}
	if (routeName === "/operations")
		return (
			<ApplicationRouteOutlet
				route="operations"
				provider={DemoOperationsBinder}
				providerProps={{
					deletions,
					provider: operationsProvider,
					positionHrefForTrack: (positionId) => {
						const release = releases.find((item) =>
							item.tracks.some((track) => track.id === positionId),
						);
						return release
							? libraryPositionHref(release.id, positionId)
							: undefined;
					},
					isTargetCurrent: (href) => {
						const incomingTarget = parseIncomingGroupsRoute(href);
						if (incomingTarget?.view === "matching")
							return incoming.groups.some(
								(group) =>
									group.id === incomingTarget.groupId && !group.processed,
							);
						const positionAddress = parseLibraryPositionAddress(href);
						if (positionAddress)
							return releases.some(
								(release) =>
									release.id === positionAddress.releaseId &&
									release.tracks.some(
										(track) => track.id === positionAddress.positionId,
									),
							);
						const releaseAddress = parseLibraryReleaseAddress(href);
						if (releaseAddress)
							return releases.some(
								(release) => release.id === releaseAddress.releaseId,
							);
						const artistAddress = parseLibraryArtistAddress(href);
						if (artistAddress)
							return artists.some(
								(artist) => artist.id === artistAddress.artistId,
							);
						return false;
					},
				}}
			/>
		);
	if (route && routeName !== "/library")
		return (
			<ScreenFeedbackView
				title={msg("screenNotMoved")}
				message={msg("routeNotMoved", { route })}
				link={{ href: libraryRootHref(), label: msg("goToReactLibrary") }}
			/>
		);
	const librarySummary = {
		artists: fixture.artists.length,
		releases: releases.length,
		positions: releases.reduce(
			(total, release) => total + release.tracks.length,
			0,
		),
		published: releases.reduce(
			(total, release) =>
				total + release.tracks.filter((track) => track.published).length,
			0,
		),
	};
	const catalogIsEmpty =
		librarySummary.artists === 0 &&
		librarySummary.releases === 0 &&
		librarySummary.positions === 0;
	const isPristine = query === "" && filter === "all";
	const hasResults =
		view === "artists"
			? pageArtists.length > 0
			: view === "albums"
				? pageAlbumEntries.length > 0
				: pageTracks.length > 0;
	const libraryPageState: LibraryCatalogPageState = hasResults
		? { status: "results" }
		: {
				status: "empty",
				reason:
					catalogIsEmpty && isPristine
						? "library"
						: filter === "published" && librarySummary.published === 0
							? "unpublished"
							: isPristine
								? "view"
								: "filtered",
				onReset: !isPristine ? resetLibrarySearch : undefined,
				addSourceHref:
					catalogIsEmpty && isPristine ? demoSourcesHref : undefined,
			};

	const missingCounts = new Map(
		fixture.artists.map((artist) => [
			artist.id,
			unrepresentedMissingGroups.filter((group) => group.artistId === artist.id)
				.length,
		]),
	);
	const pageSelection = { selectedIds: selected, onChange: update };
	const libraryResults: LibraryCatalogResults =
		view === "artists"
			? {
					kind: "artists",
					...libraryCatalogArtistResults({
						artists: pageArtists,
						releases,
						missingCounts,
						hasAudio: (track) => hasAudio(track as Track),
						pageSelection,
					}),
				}
			: view === "tracks"
				? {
						kind: "tracks",
						...libraryCatalogTrackResults({
							locale,
							artists: fixture.artists,
							releases: pageTrackReleases,
							hasAudio: (track) => hasAudio(track as Track),
							files,
							filter,
							pageSelection,
						}),
					}
				: {
						kind: "albums",
						DataProvider: DemoLibraryAlbumRowsProvider,
					};

	return (
		<ApplicationRouteOutlet
			route="library-catalog"
			provider={DemoLibraryCatalogBinder}
			providerProps={{
				locale,
				summary: librarySummary,
				incomingHref: incomingGroupsListHref(),
				view,
				search: searchInput,
				onSearch: setSearch,
				filter,
				onFilter: setFilter,
				onViewChange: (nextView) => {
					setView(nextView);
					setExpanded(null);
				},
				state: libraryPageState,
				results: libraryResults,
				albumRows:
					view === "albums"
						? {
								locale,
								artists: fixture.artists,
								releases: pageAlbumReleases,
								albumEntries: pageAlbumEntries,
								selectedEditions: editions,
								onSelectEdition: (groupId, editionKey) =>
									setEditions((previous) => ({
										...previous,
										[groupId]: editionKey,
									})),
								hasAudio: (track) => hasAudio(track as Track),
								files,
								filter,
								expanded,
								onToggleAlbum: (id) => setExpanded(expanded === id ? null : id),
								pageSelection,
							}
						: null,
				page: currentPage,
				totalPages,
				totalItems: libraryTotalItems,
				hasPrevious: currentPage > 1,
				hasNext: currentPage < totalPages,
				onPageChange: (next) => setPage(next, totalPages),
				selectionDock: (
					<SelectionDock
						selected={selected}
						visiblePositionIds={visiblePositionIds}
						releases={releases}
						onSelect={update}
						onPreview={openSelectionPreview}
						onQueueDeletion={queueDeletion}
					/>
				),
				selectionPreview: selectionPreviewDialog ?? undefined,
				dockSpace: true,
			}}
		/>
	);
}
