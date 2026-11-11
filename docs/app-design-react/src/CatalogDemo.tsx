import { useEffect, useState, useSyncExternalStore } from "react";
import {
	Button,
	Checkbox,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Tab,
	TabList,
	TabPanel,
	Tabs,
} from "./ui";
import { Input, SearchField } from "./ui";
import {
	catalogData,
	originalReleases,
	originalProviders,
	applyMetadataPreview,
	originalFiles,
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
} from "./catalogData";
import {
	PublicationPreview,
	type PublicationPreviewMode,
} from "./PublicationPreview";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import {
	captureMetadataPreview,
	isMetadataPreviewFresh,
	previewScopeKey,
	type MetadataPreviewToken,
} from "./previewModel";
import { TrackScreen } from "./TrackScreen";
import { artistReleaseHref } from "./artistReleaseRoute";
import {
	initialOperationsDemoState,
	OperationsScreen,
} from "./OperationsScreen";
import type { DeletionOperation } from "./DeletionRequest";
import { recoveryFixture } from "./catalogData";
import { initialSourceRoots, SourcesScreen } from "./SourcesScreen";
import { removeRootFromInventory } from "./sourceInventory";
import { SettingsScreen, initialDemoSettings } from "./SettingsScreen";
import { SetupScreen } from "./SetupScreen";
import { IncomingScreen } from "./IncomingScreen";
import { MatchingScreen } from "./MatchingScreen";
import { createIncomingState, makeReleasePlan } from "./incomingModel";
import { LocalProviderScreen } from "./LocalProviderScreen";
import {
	createLocalProviderState,
	localProviderReleases,
} from "./localProviderModel";
import { ArtistScreen } from "./ArtistScreen";
import { TagsScreen } from "./TagsScreen";
import { ComponentsScreen } from "./ComponentsScreen";
import {
	albumSearchText,
	artistReleases,
	catalogArtistMatches,
	missingCatalogGroups,
	normalizeCatalogSearch,
	trackSearchText,
} from "./catalogViewModel";
import { LibraryCatalogRows } from "./LibraryCatalogRows";
import { CatalogFilter as CatalogFilterControl } from "./CatalogFilter";
import { SelectionDock } from "./SelectionDock";
import type { CatalogFilter } from "./availabilityRows";
import { catalogueStyles } from "./catalogueStyles";
import {
	completeManagedLibraryDeletion,
	managedDeletionObservation,
} from "./libraryDeletionModel";
import type { ManagedPathObservation } from "./managedPublicationDeletionModel";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";
import {
	initialSourceProcessingState,
	resetSourceProcessingQueue,
	sourceProcessingLocks,
} from "./sourceProcessingModel";
import { useI18n } from "./i18n";
import { catalogMessage } from "./catalogMessages";
import { applyAudioUpgradePreview } from "./trackActionModel";

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

type View = "artists" | "albums" | "tracks";
type Track = (typeof catalogData.releases)[number]["tracks"][number];

const recordingIdentity = (track: OriginalTrack) =>
	track.localRecordingId
		? `local:${track.localRecordingId}`
		: track.recordingId
			? `musicbrainz:${track.recordingId}`
			: null;

function GroupCheckbox({
	ids,
	label,
	selected,
	onChange,
	appearance = "table",
}: {
	ids: string[];
	label: string;
	selected: Set<string>;
	onChange: (ids: string[], checked: boolean) => void;
	appearance?: "table" | "artwork";
}) {
	const count = ids.filter((id) => selected.has(id)).length;
	const checked = ids.length > 0 && count === ids.length;
	return (
		<Checkbox
			slot="selection"
			className={
				appearance === "artwork"
					? catalogueStyles.cardCheckbox
					: catalogueStyles.tableCheckbox
			}
			aria-label={label}
			isSelected={checked}
			isIndeterminate={count > 0 && !checked}
			isDisabled={ids.length === 0}
			onChange={(value) => onChange(ids, value)}
		>
			<span
				className={
					appearance === "artwork"
						? catalogueStyles.cardCheckboxBox
						: catalogueStyles.checkboxBox
				}
				aria-hidden="true"
			>
				{count > 0 && !checked ? "−" : checked ? "✓" : ""}
			</span>
		</Checkbox>
	);
}

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
	const [localProvider, setLocalProvider] = useState(createLocalProviderState);
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
	const [operationsDemo, setOperationsDemo] = useState(() =>
		structuredClone(initialOperationsDemoState),
	);
	const [removedRoots, setRemovedRoots] = useState<Set<string>>(
		() => new Set(),
	);
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
	const [view, setView] = useState<View>("artists");
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
	const [residualDeletions, setResidualDeletions] = useState(
		() => new Map<DeletionOperation, DeletionOperation>(),
	);
	const queueDeletion = (operation: DeletionOperation) => {
		setDeletions((previous) => [...previous, operation]);
		window.location.hash = "operations";
	};
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
		const heading =
			document.querySelector<HTMLElement>("#content h1") ??
			document.querySelector<HTMLElement>("h1");
		if (heading) {
			heading.tabIndex = -1;
			heading.focus({ preventScroll: true });
		}
		const main = document.querySelector<HTMLElement>(".demo-shell");
		if (main) main.tabIndex = -1;
	}, [route, preview]);
	useEffect(() => {
		const heading =
			document.querySelector<HTMLElement>("#content h1") ??
			document.querySelector<HTMLElement>("h1");
		if (heading)
			document.title = `${heading.textContent} · ${msg("documentTitleSuffix")}`;
	}, [route, preview, locale]);
	const [artistId, setArtistId] = useState<string | null>(null);
	const [query, setQuery] = useState("");
	const [selected, setSelected] = useState<Set<string>>(() => new Set());
	const [expanded, setExpanded] = useState<string | null>(null);
	const [editions, setEditions] = useState<Record<string, string>>({});
	const [filter, setFilter] = useState<CatalogFilter>("all");
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
	const openPreview = (
		ids: Iterable<string>,
		mode: PublicationPreviewMode = "metadata",
	) => {
		const frozenIds = [...ids];
		setPreviewMode(mode);
		setPreviewOriginRoute(route);
		setContextIds(route === "library" ? null : frozenIds);
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
	const openContextPublication = (
		release: (typeof releases)[number],
		trackId: string | null = null,
		force = false,
	) => {
		setIncoming((current) => ({
			...current,
			plan: { ...makeReleasePlan(current, release, files, trackId), force },
			confirmDowngrade: false,
		}));
		window.location.hash = "publications";
	};
	const tabs: [View, string][] = [
		["artists", msg("artists")],
		["albums", msg("allAlbums")],
		["tracks", msg("allTracks")],
	];
	if (preview || route === "publication-preview")
		return (
			<main className={screenLayout}>
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
			</main>
		);
	if (route === "publications")
		return (
			<main className={screenLayout}>
				<ContextPublicationPreview
					destination={publicationDestination}
					plan={incoming.plan}
					files={files}
					releases={releases}
					providers={providers}
					artists={providerArtists}
					inventoryVersion={incoming.inventoryVersion}
					confirmDowngrade={incoming.confirmDowngrade}
					onPlan={(plan, confirmDowngrade) =>
						setIncoming((current) => ({
							...current,
							plan,
							confirmDowngrade: confirmDowngrade ?? current.confirmDowngrade,
						}))
					}
					onReleases={setImportedReleases}
					onSuccess={(notice) => {
						setIncoming((current) => ({
							...current,
							notice: "",
							noticeDescriptor: null,
							uiNotice: notice,
						}));
						window.location.hash = "review";
					}}
					onBack={() => {
						window.location.hash = incoming.plan?.localReleaseId
							? `release/${incoming.plan.localReleaseId}`
							: "library";
					}}
				/>
			</main>
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
			<main className={`demo-shell ${catalogueStyles.dockScreen}`}>
				<ArtistScreen
					key={route}
					artistId={artistPath.slice(7)}
					artists={artists}
					releases={releases}
					files={files}
					genresLoaded={settings.genresLoaded}
					onLoadGenres={loadGenres}
					selected={selected}
					query={query}
					filter={filter}
					onQueryChange={setQuery}
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
				/>
				<SelectionDock
					selected={selected}
					releases={releases}
					onSelect={update}
					onPreview={(ids) => {
						setContextIds(null);
						openPreview(ids);
					}}
					onQueueDeletion={queueDeletion}
				/>
			</main>
		);
	}
	if (route.startsWith("tags/")) {
		const [, id, requestedScope] = route.split("/");
		const scope =
			requestedScope === "artist" || requestedScope === "release"
				? requestedScope
				: "track";
		return (
			<main className={screenLayout}>
				<TagsScreen
					key={route}
					trackId={id}
					scope={scope}
					releases={releases}
					artists={artists}
					files={files}
					onPreview={(ids) => {
						openPreview(ids);
					}}
				/>
			</main>
		);
	}
	if (route === "components")
		return (
			<main className={screenLayout}>
				<ComponentsScreen />
			</main>
		);
	if (route.startsWith("release/")) {
		const release = releases.find(
			(item) => item.id === route.slice("release/".length),
		);
		return (
			<main className={screenLayout}>
				<h1>
					{!release
						? msg("libraryNotFound")
						: artistReleaseHref(release, artists)
							? msg("redirectingArtist")
							: msg("artistMissing")}
				</h1>
				<a href="#library">{msg("goToLibrary")}</a>
			</main>
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
				<main className={screenLayout}>
					<h1>{msg("trackNotFound")}</h1>
					<a href="#library">{msg("goToLibrary")}</a>
				</main>
			);
		const identity = recordingIdentity(track);
		const recordingPositions = identity
			? releases.flatMap((candidateRelease) =>
					candidateRelease.tracks
						.filter((candidate) => recordingIdentity(candidate) === identity)
						.map((candidate) => ({
							release: candidateRelease,
							track: candidate,
						})),
				)
			: [{ release, track }];
		const recordingImpact =
			new Set(recordingPositions.map(({ track: position }) => position.id))
				.size > 1
				? recordingPositions.map(
						({ release: positionRelease, track: position }) =>
							`${positionRelease.title} · ${position.position} · ${position.title}`,
					)
				: [];
		return (
			<main className={screenLayout}>
				<TrackScreen
					files={files}
					artists={artists}
					onQueueDeletion={queueDeletion}
					release={release}
					track={track}
					genresLoaded={settings.genresLoaded}
					onLoadGenres={loadGenres}
					recordingImpact={recordingImpact}
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
						openContextPublication(release, track.id);
					}}
					onRepublish={() => {
						openContextPublication(release, track.id, true);
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
			</main>
		);
	}
	if (route === "local")
		return (
			<LocalProviderScreen
				state={localProvider}
				onLoadGenres={loadGenres}
				onState={setLocalProvider}
				onMatchRelease={(release) => {
					const next = structuredClone(incoming);
					const group = next.groups.find((item) => item.id === next.groupId);
					if (!group) return;
					group.mode = "provider";
					group.candidateId = release.id;
					next.groupId = group.id;
					next.selectedTarget = null;
					next.plan = null;
					next.notice = "";
					next.noticeDescriptor = null;
					next.uiNotice = {
						namespace: "library",
						id: "localReleaseSelected",
						params: {
							title: release.title ?? release.release_group_title ?? "",
						},
					};
					setIncoming(next);
					window.location.hash = `match/${group.id}`;
				}}
				genresLoaded={settings.genresLoaded}
			/>
		);
	if (route === "review")
		return (
			<IncomingScreen
				destination={publicationDestination}
				state={incoming}
				onState={setIncoming}
				files={files}
				providers={providers}
				artists={providerArtists}
				releases={releases}
				onReleases={setImportedReleases}
			/>
		);
	if (route.startsWith("match/"))
		return (
			<MatchingScreen
				destination={publicationDestination}
				groupId={route.slice(6)}
				state={incoming}
				onState={setIncoming}
				files={files}
				providers={providers}
				artists={providerArtists}
				releases={releases}
				onReleases={setImportedReleases}
			/>
		);
	if (route === "settings")
		return (
			<main className={screenLayout}>
				<SettingsScreen
					settings={settings}
					sourceRoots={sourceRoots.map((root) => root.path)}
					onSettings={setSettings}
					workDirectoryBusy={
						sourceProcessingLocks(sourceProcessing).blocksWorkDirectoryChange
					}
					onOutputReset={() => {
						setSourceProcessing((previous) =>
							resetSourceProcessingQueue(previous),
						);
						setOperationsDemo({
							retryState: "succeeded",
							dismissedOperations: new Set(["signals", "recovery"]),
						});
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
			</main>
		);
	if (route === "setup")
		return (
			<main className={screenLayout}>
				<SetupScreen
					settings={settings}
					onSettings={setSettings}
					step={setupStep}
					onStepChange={setSetupStep}
				/>
			</main>
		);
	if (route === "sources")
		return (
			<main className={screenLayout}>
				<SourcesScreen
					roots={sourceRoots}
					onRoots={setSourceRoots}
					files={files}
					removed={removedRoots}
					workDirectory={settings.workDirectory}
					toolsPath={settings.toolsPath}
					currentOutput={settings.currentOutput ?? "/srv/music"}
					initialProcessingState={sourceProcessing}
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
						setRemovedRoots((previous) => new Set([...previous, id]));
					}}
				/>
			</main>
		);
	if (route === "operations")
		return (
			<main className={screenLayout}>
				<OperationsScreen
					releases={releases}
					files={files}
					deletions={deletions}
					initialState={operationsDemo}
					onState={setOperationsDemo}
					onCompleteDeletion={completeDeletion}
					onDismissDeletion={(operation) =>
						setDeletions((previous) =>
							previous.filter((item) => item !== operation),
						)
					}
					onRecover={(snapshot) => {
						const currentOutput = settings.currentOutput ?? "/srv/music";
						if (
							!snapshot.path.startsWith(
								`${currentOutput.replace(/\/$/, "")}/`,
							) ||
							!releases.some((release) =>
								release.tracks.some(
									(track) => track.id === recoveryFixture.trackId,
								),
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
					}}
				/>
			</main>
		);
	if (route && route !== "library")
		return (
			<main className={screenLayout}>
				<h1>{msg("screenNotMoved")}</h1>
				<p>{msg("routeNotMoved", { route })}</p>
				<p>
					<a href="#library">{msg("goToReactLibrary")}</a>
				</p>
			</main>
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
	const visibleReleases = fixture.releases.filter(
		(release) =>
			(!artistId ||
				release.artistId === artistId ||
				release.credits?.some((credit) => credit.artistId === artistId)) &&
			normalizeCatalogSearch(
				albumSearchText(release, fixture.artists),
			).includes(normalized) &&
			matchesGroup(release.tracks),
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
	return (
		<main
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
					className={catalogueStyles.tabs}
				>
					<TabList aria-label={msg("libraryView")}>
						{tabs.map(([key, label]) => (
							<Tab key={key} id={key}>
								{label}
							</Tab>
						))}
					</TabList>
					<TabPanel id={view} className={catalogueStyles.tabsPanel}>
						<div className={catalogueStyles.panelControls}>
							<SearchField
								aria-label={msg("searchLibrary")}
								value={query}
								onChange={setQuery}
								className={catalogueStyles.search}
							>
								<Input placeholder={msg("searchPlaceholder")} />
							</SearchField>
							<CatalogFilterControl value={filter} onChange={setFilter} />
						</div>
						<section
							className={catalogueStyles.list}
							aria-label={tabs.find(([key]) => key === view)?.[1]}
						>
							<LibraryCatalogRows
								view={view}
								artists={view === "artists" ? visibleArtists : fixture.artists}
								releases={
									view === "albums"
										? releases.filter((release) =>
												visibleReleases.some((item) => item.id === release.id),
											)
										: view === "tracks"
											? releases
													.map((release) => ({
														...release,
														tracks: release.tracks.filter((track) =>
															visibleTrackIds.has(track.id),
														),
													}))
													.filter((release) => release.tracks.length > 0)
											: releases
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
								groupCheckbox={(ids, label, appearance) => (
									<GroupCheckbox
										ids={ids}
										label={label}
										appearance={appearance}
										selected={selected}
										onChange={update}
									/>
								)}
								expanded={expanded}
								onToggleAlbum={(id) => setExpanded(expanded === id ? null : id)}
							/>
						</section>
						{view === "albums" &&
							missingGroups
								.filter((group) => !artistId || group.artistId === artistId)
								.map((group) => {
									const chosen = group.editions.find(
										(edition) => edition.id === editions[group.id],
									);
									return (
										<section
											className={catalogueStyles.detailSelect}
											key={group.id}
										>
											<h2>{group.title}</h2>
											<p>
												{artistName(group.artistId) ||
													msg("artistNotSpecified")}{" "}
												· {msg("missingAlbum")}
											</p>
											<Select
												aria-label={msg("chooseEditionFor", {
													title: group.title,
												})}
												selectedKey={editions[group.id] ?? "none"}
												onSelectionChange={(key) => {
													const value =
														String(key) === "none" ? "" : String(key);
													setEditions((previous) => ({
														...previous,
														[group.id]: value,
													}));
												}}
												className={catalogueStyles.select}
											>
												<Button>
													<SelectValue />
													<span aria-hidden="true">⌄</span>
												</Button>
												<Popover className={catalogueStyles.popover}>
													<ListBox>
														<ListBoxItem id="none">
															{msg("chooseEdition")}
														</ListBoxItem>
														{group.editions.map((edition) => (
															<ListBoxItem key={edition.id} id={edition.id}>
																{edition.year} · {edition.country} ·{" "}
																{edition.format} · {edition.disambiguation}
															</ListBoxItem>
														))}
													</ListBox>
												</Popover>
											</Select>
											{chosen && (
												<ol>
													{chosen.titles.map((title) => (
														<li key={title}>{title}</li>
													))}
												</ol>
											)}
											<p>{msg("previewDoesNotAdd")}</p>
										</section>
									);
								})}
					</TabPanel>
				</Tabs>
			</div>
			<SelectionDock
				selected={selected}
				releases={releases}
				onSelect={update}
				onPreview={(ids) => {
					setContextIds(null);
					openPreview(ids);
				}}
				onQueueDeletion={queueDeletion}
			/>
		</main>
	);
}
