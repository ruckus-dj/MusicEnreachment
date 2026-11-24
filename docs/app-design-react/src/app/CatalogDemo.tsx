import { useEffect, useState, useSyncExternalStore } from "react";
import { ComponentsScreen } from "../components/ComponentsScreen";
import type { DeletionOperation } from "../components/DeletionRequest";
import { Button, TabPanel, Tabs } from "../components/ui";
import { IncomingScreen } from "../features/incoming/IncomingScreen";
import {
	createIncomingState,
	type IncomingPlan,
	makeReleasePlan,
} from "../features/incoming/incomingModel";
import {
	combineLibraryAlbumEntries,
	LibraryPagination,
	libraryArtistPositionIds,
	libraryPageCount,
	libraryPageSlice,
	libraryTrackPositionIds,
	sliceTrackReleasePage,
	useLibraryRouteState,
} from "../features/library";
import { ArtistScreen } from "../features/library/ArtistScreen";
import { artistReleaseHref } from "../features/library/artistReleaseRoute";
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
} from "../features/library/catalogData";
import { catalogMessage } from "../features/library/catalogMessages";
import { catalogueStyles } from "../features/library/catalogueStyles";
import {
	albumSearchText,
	artistReleases,
	catalogArtistMatches,
	missingCatalogGroups,
	normalizeCatalogSearch,
	trackSearchText,
} from "../features/library/catalogViewModel";
import {
	LibraryCatalogQueryControls,
	LibraryCatalogTabList,
	libraryViewLabel,
} from "../features/library/LibraryCatalogControls";
import { LibraryCatalogRows } from "../features/library/LibraryCatalogRows";
import {
	type LibrarySelectionPreviewPosition,
	type LibrarySelectionPreviewState,
	LibrarySelectionPreviewView,
	maxLibraryPreviewPositions,
} from "../features/library/LibrarySelectionPreviewView";
import {
	completeManagedLibraryDeletion,
	managedDeletionObservation,
} from "../features/library/libraryDeletionModel";
import { SelectionDock } from "../features/library/SelectionDock";
import { TagsScreen } from "../features/library/TagsScreen";
import { TrackScreen } from "../features/library/TrackScreen";
import { applyAudioUpgradePreview } from "../features/library/trackActionModel";
import { LocalCatalogScreen } from "../features/local-catalog";
import { releaseDraftToRecord } from "../features/local-catalog/localCatalogModel";
import { localProviderReleases } from "../features/local-catalog/localProviderModel";
import { LocalReleaseEditor } from "../features/local-release/LocalReleaseEditor";
import { MatchingScreen } from "../features/matching/MatchingScreen";
import { OperationsScreen } from "../features/operations/OperationsScreen";
import { ContextPublicationPreview } from "../features/publication/ContextPublicationPreview";
import {
	type DemoPublicationCatalog,
	type DemoPublicationRow,
	planDemoPublication,
	settleDemoPublication,
} from "../features/publication/libraryPublicationDemo";
import type { ManagedPathObservation } from "../features/publication/managedPublicationDeletionModel";
import {
	PublicationPreview,
	type PublicationPreviewMode,
} from "../features/publication/PublicationPreview";
import {
	captureMetadataPreview,
	isMetadataPreviewFresh,
	type MetadataPreviewToken,
	previewScopeKey,
} from "../features/publication/previewModel";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "../features/publication/publicationOutput";
import { defaultPublicationTemplate } from "../features/settings/publicationTemplateDemo";
import {
	initialDemoSettings,
	SettingsScreen,
} from "../features/settings/SettingsScreen";
import { SetupScreen } from "../features/setup/SetupScreen";
import {
	initialSourceRoots,
	SourcesScreen,
} from "../features/sources/SourcesScreen";
import { parseDemoSourceRoute } from "../features/sources/sourceDemoRoute";
import { removeRootFromInventory } from "../features/sources/sourceInventory";
import {
	initialSourceProcessingState,
	resetSourceProcessingQueue,
	sourceProcessingLocks,
} from "../features/sources/sourceProcessingModel";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../fixtures/directoryBrowseDemo";
import { LocalReleaseDemoGenres } from "../fixtures/LocalReleaseDemoGenres";
import { createLocalCatalogDemoState } from "../fixtures/localCatalogDemo";
import {
	appendDemoLocalRecord,
	checkDemoReleases,
	demoRecordingWithCredits,
	findDemoArtists,
	findDemoRecordings,
	localReleaseSuggestions,
	newDemoLocalRecord,
	selectDemoRelease,
} from "../fixtures/localReleaseDemo";
import {
	type DemoPublicationJob,
	useOperationsDemoProvider,
} from "../fixtures/useOperationsDemoProvider";
import { useSourceScanDemoProvider } from "../fixtures/useSourceScanDemoProvider";
import { useI18n } from "../i18n";
import { formatUiNotice, type UiNotice } from "../infrastructure/uiNotice";
import { ShellContent } from "../routes/ShellView";

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

const routeId = (value: string) => {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
};

type View = "artists" | "albums" | "tracks";
type Track = (typeof catalogData.releases)[number]["tracks"][number];
type PublicationJob = DemoPublicationJob & { plan: DemoPublicationRow };

const recordingIdentity = (track: OriginalTrack) =>
	track.localRecordingId
		? `local:${track.localRecordingId}`
		: track.recordingId
			? `musicbrainz:${track.recordingId}`
			: null;

const trackIds = (tracks: Track[]) => tracks.map((track) => track.id);

const screenLayout =
	"demo-shell max-w-none p-3 [&_h1]:my-[2px] [&_h1]:text-[20px] [&_header_p]:my-1 [&_header_p]:text-[var(--muted)] [&_a]:text-[var(--accent)] [&_a:hover]:underline";

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
			return hash === "publication-list"
				? "library"
				: hash === "create-release"
					? "local"
					: hash;
		},
		() => "library",
	);
	const route = routePath;
	const [routeName, routeQuery] = route.split("?", 2);
	const {
		view,
		query,
		filter,
		page,
		searchInput,
		isLibraryRoute,
		setView,
		setFilter,
		setPage,
		setSearch,
	} = useLibraryRouteState(route);
	const headingRoute =
		routeName === "library" || routeName === "settings" ? routeName : route;
	const [releases, setReleases] = useState(() =>
		structuredClone(originalReleases),
	);
	const [artists, setArtists] = useState<CatalogArtist[]>(() =>
		structuredClone(catalogData.artists),
	);
	useEffect(() => {
		let target: string | null = null;
		if (routePath.startsWith("release/")) {
			const release = releases.find(
				(item) => item.id === routePath.slice("release/".length),
			);
			target = release ? artistReleaseHref(release, artists) : null;
		} else if (routePath.startsWith("tags/")) {
			const [, trackId, scope] = routePath.split("/");
			if (scope === "artist" || scope === "release") {
				const release = releases.find((item) =>
					item.tracks.some((track) => track.id === trackId),
				);
				target = release
					? artistReleaseHref(release, artists, { tags: scope })
					: null;
			}
		}
		if (target) window.location.hash = target.slice(1);
	}, [routePath, releases, artists]);
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
	useEffect(() => {
		if (window.location.hash === "#create-release")
			window.location.hash = "local";
	}, [route]);
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
	useEffect(() => {
		const match = route.match(/^tags\/([^/?]+)\/(artist|release)$/);
		if (!match) return;
		const [, trackId, scope] = match;
		const release = releases.find((item) =>
			item.tracks.some((track) => track.id === trackId),
		);
		if (!release) return;
		const href = artistReleaseHref(
			release,
			artists,
			scope === "release" ? { tags: "release" } : { tags: "artist" },
		);
		if (href) window.location.hash = href.slice(1);
	}, [route, releases, artists]);
	useEffect(() => {
		const match = route.match(/^release\/([^/?]+)/);
		if (!match) return;
		const release = releases.find((item) => item.id === match[1]);
		if (!release) return;
		const href = artistReleaseHref(release, artists);
		if (href) window.location.hash = href.slice(1);
	}, [route, releases, artists]);
	const allTracks = fixture.releases.flatMap((release) => release.tracks);
	const [selectionPreview, setSelectionPreview] = useState<{
		positions: LibrarySelectionPreviewPosition[];
		state: LibrarySelectionPreviewState;
	} | null>(null);
	const [positionPreview, setPositionPreview] = useState<{
		plan: IncomingPlan;
		confirmDowngrade: boolean;
	} | null>(null);
	const [positionNotice, setPositionNotice] = useState<{
		releaseId: string;
		positionId: string;
		notice: UiNotice;
	} | null>(null);
	useEffect(() => {
		setSelectionPreview(null);
		setPositionPreview(null);
		setPositionNotice(null);
	}, [route]);
	const [preview, setPreview] = useState(false);
	const [previewMode, setPreviewMode] =
		useState<PublicationPreviewMode>("metadata");
	useEffect(() => {
		if (route !== "publication-preview") setPreview(false);
	}, [route]);
	const [contextIds, setContextIds] = useState<string[] | null>(null);
	const [previewOriginRoute, setPreviewOriginRoute] = useState("library");
	const [previewToken, setPreviewToken] = useState<MetadataPreviewToken | null>(
		null,
	);
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
		window.location.hash = "operations";
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
	useEffect(() => {
		if (routeName !== "review" && !routeName.startsWith("match/")) return;
		const content = document.querySelector<HTMLElement>("#content");
		if (content) content.scrollTop = 0;
		if (document.scrollingElement) document.scrollingElement.scrollTop = 0;
	}, [routeName]);
	useEffect(() => {
		const heading =
			document.querySelector<HTMLElement>("#content h1") ??
			document.querySelector<HTMLElement>("h1");
		if (heading) {
			heading.tabIndex = -1;
			heading.focus({ preventScroll: true });
		}
		const main = document.querySelector<HTMLElement>(".demo-shell");
		if (main) main.tabIndex = -1;
	}, [headingRoute, preview]);
	useEffect(() => {
		const heading =
			document.querySelector<HTMLElement>("#content h1") ??
			document.querySelector<HTMLElement>("h1");
		if (heading)
			document.title = `${heading.textContent} · ${msg("documentTitleSuffix")}`;
	}, [route, preview, locale]);
	const [artistId, setArtistId] = useState<string | null>(null);
	const [artistQuery, setArtistQuery] = useState("");
	const [selected, setSelected] = useState<Set<string>>(() => new Set());
	const [expanded, setExpanded] = useState<string | null>(null);
	const [editions, setEditions] = useState<Record<string, string>>({});
	const normalized = normalizeCatalogSearch(query);
	const matchesGroup = (tracks: Track[]) =>
		filter === "all" ||
		tracks.some((track) =>
			filter === "published"
				? Boolean(track.published)
				: filter === "available"
					? hasAudio(track)
					: !hasAudio(track),
		);
	const artistName = (id: string | null) =>
		fixture.artists.find((artist) => artist.id === id)?.name ?? "";
	const missingGroups = missingCatalogGroups(
		fixture.absentGroups,
		fixture.releases,
		fixture.artists,
		query,
		filter,
	);
	const unrepresentedMissingGroups = missingCatalogGroups(
		fixture.absentGroups,
		fixture.releases,
		fixture.artists,
		"",
		"all",
	);
	const visibleArtists = fixture.artists.filter((artist) =>
		catalogArtistMatches(
			artist,
			fixture.releases,
			missingGroups,
			query,
			filter,
			(track) => hasAudio(track as Track),
		),
	);
	const visibleReleaseIds = new Set(
		fixture.releases
			.filter(
				(release) =>
					(!artistId ||
						release.artistId === artistId ||
						release.credits?.some((credit) => credit.artistId === artistId)) &&
					normalizeCatalogSearch(
						albumSearchText(release, fixture.artists),
					).includes(normalized) &&
					matchesGroup(release.tracks),
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
						).includes(normalized) && matchesGroup([track]),
				)
				.map((track) => track.id),
		),
	);
	const visibleTrackRefs = releases.flatMap((release) =>
		release.tracks
			.filter((track) => visibleTrackIds.has(track.id))
			.map((track) => ({ release, track })),
	);
	const missingGroupsForView = missingGroups.filter(
		(group) => !artistId || group.artistId === artistId,
	);
	const albumEntries = combineLibraryAlbumEntries(
		orderedVisibleReleases,
		missingGroupsForView,
	);
	const libraryTotalItems =
		view === "artists"
			? visibleArtists.length
			: view === "albums"
				? albumEntries.length
				: visibleTrackRefs.length;
	const totalPages = libraryPageCount(libraryTotalItems);
	const currentPage = Math.min(page, totalPages);
	const pageArtists = libraryPageSlice(visibleArtists, currentPage);
	const pageAlbumEntries = libraryPageSlice(albumEntries, currentPage);
	const pageAlbumReleases = pageAlbumEntries.flatMap((entry) =>
		entry.kind === "release" ? [entry.release] : [],
	);
	const pageTracks = libraryPageSlice(visibleTrackRefs, currentPage);
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
		setPage(currentPage, totalPages);
	}, [currentPage, isLibraryRoute, setPage, totalPages]);
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
		setPreviewMode(mode);
		setPreviewOriginRoute(route);
		setContextIds(frozenIds);
		setPreviewToken(
			captureMetadataPreview(
				incoming.inventoryVersion,
				previewScopeKey(route, frozenIds),
				frozenIds,
			),
		);
		setPreview(true);
		window.location.hash = "publication-preview";
	};
	const applyPreview = (token: MetadataPreviewToken) => {
		const activeIds = contextIds ?? [...selected];
		const activeScope = previewScopeKey(previewOriginRoute, activeIds);
		if (!isMetadataPreviewFresh(token, incoming.inventoryVersion, activeScope))
			return false;
		setReleases((previous) =>
			previewMode === "audioUpgrade"
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
	const openPositionPreview = (
		release: (typeof releases)[number],
		trackId: string,
		force = false,
	) => {
		setPositionNotice(null);
		setPositionPreview({
			plan: { ...makeReleasePlan(incoming, release, files, trackId), force },
			confirmDowngrade: false,
		});
	};
	const selectionPreviewDialog = selectionPreview && (
		<LibrarySelectionPreviewView
			locale={locale}
			positions={selectionPreview.positions}
			state={selectionPreview.state}
			onClose={() => setSelectionPreview(null)}
		/>
	);
	if (preview || route === "publication-preview")
		return (
			<section className={screenLayout}>
				{previewToken ? (
					<PublicationPreview
						mode={previewMode}
						destination={publicationDestination}
						token={previewToken}
						inventoryVersion={incoming.inventoryVersion}
						scopeKey={previewScopeKey(
							previewOriginRoute,
							contextIds ?? [...selected],
						)}
						files={files}
						releases={releases}
						artists={artists}
						onApply={applyPreview}
						onBack={() => {
							setPreview(false);
							window.location.hash = previewOriginRoute;
						}}
						returnLabel={
							previewOriginRoute.startsWith("track/")
								? msg("backToTrack")
								: msg("backToLibrary")
						}
					/>
				) : (
					<>
						<h1>{msg("previewTitle")}</h1>
						<p>{msg("noSavedPreview")}</p>
						<a href="#library">{msg("goToLibrary")}</a>
					</>
				)}
			</section>
		);
	if (route.startsWith("artist/")) {
		const [artistPath, queryString = ""] = route.split("?");
		const params = new URLSearchParams(queryString);
		const initialTags = params.get("tags");
		const initialTagsScope =
			initialTags === "artist" || initialTags === "release"
				? initialTags
				: undefined;
		const releaseId = params.get("release") ?? undefined;
		return (
			<section className={`demo-shell ${catalogueStyles.dockScreen}`}>
				<ArtistScreen
					key={route}
					artistId={artistPath.slice(7)}
					artists={artists}
					releases={releases}
					files={files}
					genresLoaded={settings.genresLoaded}
					onLoadGenres={loadGenres}
					selected={selected}
					query={artistQuery}
					filter={filter}
					onQueryChange={setArtistQuery}
					onFilterChange={setFilter}
					onSelect={update}
					onQueueDeletion={queueDeletion}
					onUpdateArtist={(updated) =>
						setArtists((current) =>
							current.map((artist) =>
								artist.id === updated.id ? updated : artist,
							),
						)
					}
					onUpdateRelease={(updated) =>
						setReleases((current) =>
							current.map((release) =>
								release.id === updated.id ? updated : release,
							),
						)
					}
					initialTags={
						initialTagsScope
							? { scope: initialTagsScope, releaseId }
							: undefined
					}
					initialReleaseId={releaseId}
					publication={{
						catalog: publicationCatalog,
						pending: publicationJobs.filter((job) => {
							const state = operationsProvider.publicationStates?.get(job.id);
							return !state || state === "queued" || state === "running";
						}),
						onQueue: queuePublications,
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
			</section>
		);
	}
	if (route.startsWith("tags/")) {
		const [, id, requestedScope] = route.split("/");
		const scope =
			requestedScope === "artist" || requestedScope === "release"
				? requestedScope
				: "track";
		return (
			<section className={screenLayout}>
				<TagsScreen
					key={route}
					trackId={id}
					scope={scope}
					releases={releases}
					providers={providers}
					artists={artists}
					files={files}
					onPreview={(ids) => {
						openPreview(ids);
					}}
				/>
			</section>
		);
	}
	if (route === "components")
		return (
			<section className={screenLayout}>
				<ComponentsScreen />
			</section>
		);
	if (route.startsWith("release/")) {
		const release = releases.find(
			(item) => item.id === route.slice("release/".length),
		);
		return (
			<section className={screenLayout}>
				<h1>
					{!release
						? msg("libraryNotFound")
						: artistReleaseHref(release, artists)
							? msg("redirectingArtist")
							: msg("artistMissing")}
				</h1>
				<a href="#library">{msg("goToLibrary")}</a>
			</section>
		);
	}
	if (route.startsWith("track/")) {
		const trackId = route.slice("track/".length);
		const release = releases.find((item) =>
			item.tracks.some((track) => track.id === trackId),
		);
		const track = release?.tracks.find((item) => item.id === trackId);
		if (!release || !track)
			return (
				<section className={screenLayout}>
					<h1>{msg("trackNotFound")}</h1>
					<a href="#library">{msg("goToLibrary")}</a>
				</section>
			);
		const identity = recordingIdentity(track);
		const activePositionPreview =
			positionPreview &&
			positionPreview.plan.positionId === track.id &&
			positionPreview.plan.localReleaseId === release.id
				? positionPreview
				: null;
		const activePositionNotice =
			positionNotice &&
			positionNotice.releaseId === release.id &&
			positionNotice.positionId === track.id
				? positionNotice.notice
				: null;
		const publicationPreview =
			activePositionPreview || activePositionNotice ? (
				<>
					{activePositionNotice && (
						<p role="status">{formatUiNotice(locale, activePositionNotice)}</p>
					)}
					{activePositionPreview && (
						<ContextPublicationPreview
							destination={publicationDestination}
							plan={activePositionPreview.plan}
							files={files}
							releases={releases}
							providers={providers}
							artists={providerArtists}
							inventoryVersion={incoming.inventoryVersion}
							confirmDowngrade={activePositionPreview.confirmDowngrade}
							onPlan={(plan, confirmDowngrade) => {
								setPositionPreview((current) =>
									current?.plan === activePositionPreview.plan
										? plan
											? {
													plan,
													confirmDowngrade:
														confirmDowngrade ?? current.confirmDowngrade,
												}
											: null
										: current,
								);
							}}
							onReleases={setImportedReleases}
							onSuccess={(notice) => {
								setPositionNotice({
									releaseId: release.id,
									positionId: track.id,
									notice,
								});
							}}
						/>
					)}
				</>
			) : undefined;
		return (
			<section className={screenLayout}>
				<TrackScreen
					files={files}
					providers={providers}
					artists={artists}
					publicationPreview={publicationPreview}
					onQueueDeletion={queueDeletion}
					release={release}
					track={track}
					genresLoaded={settings.genresLoaded}
					onLoadGenres={loadGenres}
					onRecordingChange={(updateTrack) =>
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
						)
					}
					onMutate={(updateTrack) =>
						setReleases((previous) =>
							previous.map((item) => ({
								...item,
								tracks: item.tracks.map((candidate) =>
									candidate.id === track.id
										? updateTrack(candidate)
										: candidate,
								),
							})),
						)
					}
					onPreview={() => {
						openPositionPreview(release, track.id);
					}}
					onRepublish={() => {
						openPositionPreview(release, track.id, true);
					}}
					onAudioUpgrade={() => {
						openPreview([track.id], "audioUpgrade");
					}}
					onMetadataPreview={(ids) => {
						openPreview(ids);
					}}
					onSource={(id) =>
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
						)
					}
				/>
			</section>
		);
	}
	if (route === "local")
		return (
			<LocalCatalogScreen
				state={localProvider}
				onLoadGenres={loadGenres}
				onState={setLocalProvider}
				genresLoaded={settings.genresLoaded}
			/>
		);
	if (routeName === "review")
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
	const localReleaseRoute = /^match\/([^/]+)\/local-release$/.exec(routeName);
	if (localReleaseRoute) {
		const groupId = routeId(localReleaseRoute[1]);
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
		const returnToMatching = (prepareReleaseId?: string) => {
			const query = prepareReleaseId
				? `?${new URLSearchParams({ prepare: prepareReleaseId })}`
				: "";
			window.location.replace(`#match/${encodeURIComponent(group.id)}${query}`);
		};
		const suggested = localReleaseSuggestions(group, files, providerArtists);
		return (
			<LocalReleaseEditor
				key={group.id}
				locale={locale}
				incomingLabel={group.title}
				suggestedArtist={suggested.artist}
				suggestedTitle={suggested.title}
				renderGenreField={(props) => (
					<LocalReleaseDemoGenres
						{...props}
						loaded={settings.genresLoaded}
						onLoad={loadGenres}
					/>
				)}
				artists={localProvider.items.artists}
				recordings={localProvider.items.recordings.map((recording) =>
					demoRecordingWithCredits(recording, localProvider.items.artists),
				)}
				checkRelease={async (artist, title) =>
					checkDemoReleases(providers, providerArtists, artist, title)
				}
				findRecordings={async (query) =>
					findDemoRecordings(localProvider.items.recordings, query).map(
						(recording) =>
							demoRecordingWithCredits(recording, localProvider.items.artists),
					)
				}
				findArtists={async (query) =>
					findDemoArtists(localProvider.items.artists, query)
				}
				saveArtist={(data) => {
					const artist = newDemoLocalRecord(data);
					setLocalProvider((current) =>
						appendDemoLocalRecord(current, "artists", artist),
					);
					return artist;
				}}
				saveRecording={(data) => {
					const recording = newDemoLocalRecord({
						...data,
						artistIds: data.credits?.map((credit) => credit.artistId),
					});
					setLocalProvider((current) =>
						appendDemoLocalRecord(current, "recordings", recording),
					);
					return recording;
				}}
				onUseRelease={(id) => {
					const candidate = providers.find((item) => item.id === id);
					if (!candidate) return;
					if (candidate.provider !== "Local") {
						returnToMatching(candidate.providerId ?? candidate.id);
						return;
					}
					setIncoming((current) =>
						selectDemoRelease(current, group.id, candidate),
					);
					returnToMatching();
				}}
				onSave={(draft) => {
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
					returnToMatching();
				}}
				onCancel={() => returnToMatching()}
			/>
		);
	}
	if (routeName.startsWith("match/"))
		return (
			<MatchingScreen
				destination={publicationDestination}
				groupId={routeId(routeName.slice(6))}
				state={incoming}
				onState={setIncoming}
				files={files}
				providers={providers}
				artists={providerArtists}
				releases={releases}
				onReleases={setImportedReleases}
				createLocalReleaseHref={`#match/${encodeURIComponent(routeId(routeName.slice(6)))}/local-release`}
				prepareReleaseId={
					new URLSearchParams(routeQuery).get("prepare") ?? undefined
				}
			/>
		);
	if (routeName === "settings")
		return (
			<ShellContent>
				<SettingsScreen
					settings={settings}
					sourceRoots={sourceRoots.map((root) => root.path)}
					browseDirectory={browseDemoDirectory}
					resolveDirectoryPath={demoDirectoryStart}
					onSettings={setSettings}
					workDirectoryBusy={
						sourceProcessingLocks(sourceProcessing).blocksWorkDirectoryChange
					}
					onOutputReset={() => {
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
					}}
				/>
			</ShellContent>
		);
	if (route === "setup")
		return (
			<section className={screenLayout}>
				<SetupScreen
					settings={settings}
					onSettings={setSettings}
					step={setupStep}
					onStepChange={setSetupStep}
				/>
			</section>
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
		return sourceRoute.view === "location" ? (
			<ShellContent>{sourceScreen}</ShellContent>
		) : (
			<section className={screenLayout}>{sourceScreen}</section>
		);
	}
	if (route === "operations")
		return (
			<section className={screenLayout}>
				<OperationsScreen
					deletions={deletions}
					provider={operationsProvider}
					isTargetCurrent={(href) => {
						const [route, id] = href.slice(1).split("/");
						switch (route) {
							case "track":
								return releases.some((release) =>
									release.tracks.some((track) => track.id === id),
								);
							case "release":
								return releases.some((release) => release.id === id);
							case "artist":
								return artists.some((artist) => artist.id === id);
							case "match":
								return incoming.groups.some(
									(group) => group.id === id && !group.processed,
								);
							default:
								return false;
						}
					}}
				/>
			</section>
		);
	if (route && routeName !== "library")
		return (
			<section className={screenLayout}>
				<h1>{msg("screenNotMoved")}</h1>
				<p>{msg("routeNotMoved", { route })}</p>
				<p>
					<a href="#library">{msg("goToReactLibrary")}</a>
				</p>
			</section>
		);
	return (
		<section
			className={`demo-shell ${catalogueStyles.shell} ${catalogueStyles.dockScreen}`}
		>
			<header className={catalogueStyles.pageHeader}>
				<h1 className={catalogueStyles.pageTitle}>
					{artistId
						? artistName(artistId) || msg("artistNotSpecified")
						: msg("library")}
				</h1>
				{artistId && (
					<Button
						className={catalogueStyles.pageHeaderAction}
						onPress={() => {
							setArtistId(null);
							setView("artists");
						}}
					>
						{msg("backToLibrary")}
					</Button>
				)}
			</header>
			<div className={catalogueStyles.controls}>
				<Tabs
					selectedKey={view}
					onSelectionChange={(key) => {
						setView(key as View);
						setArtistId(null);
						setExpanded(null);
					}}
				>
					<LibraryCatalogTabList locale={locale} />
					<TabPanel id={view} className={catalogueStyles.tabsPanel}>
						<LibraryCatalogQueryControls
							locale={locale}
							search={searchInput}
							onSearch={setSearch}
							filter={filter}
							onFilter={setFilter}
						/>
						<section
							className={catalogueStyles.list}
							aria-label={libraryViewLabel(locale, view)}
						>
							<LibraryCatalogRows
								view={view}
								artists={view === "artists" ? pageArtists : fixture.artists}
								releases={
									view === "albums"
										? pageAlbumReleases
										: view === "tracks"
											? pageTrackReleases
											: releases
								}
								albumEntries={view === "albums" ? pageAlbumEntries : undefined}
								selectedEditions={editions}
								onSelectEdition={(groupId, editionKey) =>
									setEditions((previous) => ({
										...previous,
										[groupId]: editionKey,
									}))
								}
								missingCounts={
									new Map(
										fixture.artists.map((artist) => [
											artist.id,
											unrepresentedMissingGroups.filter(
												(group) => group.artistId === artist.id,
											).length,
										]),
									)
								}
								hasAudio={(track) => hasAudio(track as Track)}
								files={files}
								filter={filter}
								expanded={expanded}
								onToggleAlbum={(id) => setExpanded(expanded === id ? null : id)}
								pageSelection={{ selectedIds: selected, onChange: update }}
							/>
						</section>
						<LibraryPagination
							page={currentPage}
							totalPages={totalPages}
							totalItems={libraryTotalItems}
							onPageChange={(next) => setPage(next, totalPages)}
						/>
					</TabPanel>
				</Tabs>
			</div>
			<SelectionDock
				selected={selected}
				visiblePositionIds={visiblePositionIds}
				releases={releases}
				onSelect={update}
				onPreview={openSelectionPreview}
				onQueueDeletion={queueDeletion}
			/>
			{selectionPreviewDialog}
		</section>
	);
}
