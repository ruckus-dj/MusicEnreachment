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
} from "./catalogData";
import { PublicationPreview } from "./PublicationPreview";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import {
	captureMetadataPreview,
	isMetadataPreviewFresh,
	previewScopeKey,
	type MetadataPreviewToken,
} from "./previewModel";
import { TrackScreen } from "./TrackScreen";
import { artistReleaseHref } from "./artistReleaseRoute";
import { OperationsScreen } from "./OperationsScreen";
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
import "./catalogue-shell.css";

const subscribeRoute = (listener: () => void) => {
	window.addEventListener("hashchange", listener);
	return () => window.removeEventListener("hashchange", listener);
};

type View = "artists" | "albums" | "tracks";
type Track = (typeof catalogData.releases)[number]["tracks"][number];

function GroupCheckbox({
	ids,
	label,
	selected,
	onChange,
}: {
	ids: string[];
	label: string;
	selected: Set<string>;
	onChange: (ids: string[], checked: boolean) => void;
}) {
	const count = ids.filter((id) => selected.has(id)).length;
	const checked = ids.length > 0 && count === ids.length;
	return (
		<Checkbox
			slot="selection"
			className="demo-checkbox"
			aria-label={label}
			isSelected={checked}
			isIndeterminate={count > 0 && !checked}
			isDisabled={ids.length === 0}
			onChange={(value) => onChange(ids, value)}
		>
			<span className="demo-checkbox-box" aria-hidden="true">
				{count > 0 && !checked ? "−" : checked ? "✓" : ""}
			</span>
		</Checkbox>
	);
}

const trackIds = (tracks: Track[]) => tracks.map((track) => track.id);

export function CatalogDemo({
	onIncomingCount,
}: {
	onIncomingCount?: (count: number) => void;
} = {}) {
	const [localProvider, setLocalProvider] = useState(createLocalProviderState);
	const [incoming, setIncoming] = useState(createIncomingState);
	const incomingCount = incoming.groups.filter(
		(group) => !group.processed,
	).length;
	useEffect(() => {
		onIncomingCount?.(incomingCount);
	}, [incomingCount, onIncomingCount]);
	const [settings, setSettings] = useState(initialDemoSettings);
	const [setupStep, setSetupStep] = useState(0);
	const [files, setFiles] = useState(() => structuredClone(originalFiles));
	const [sourceRoots, setSourceRoots] = useState(() =>
		structuredClone(initialSourceRoots),
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
				genre: "Локальный каталог",
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
					...release.tracks.map((track) => track.artistId),
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
	useEffect(() => {
		if (route !== "publication-preview") setPreview(false);
	}, [route]);
	const [contextIds, setContextIds] = useState<string[] | null>(null);
	const [previewOriginRoute, setPreviewOriginRoute] = useState("library");
	const [previewToken, setPreviewToken] = useState<MetadataPreviewToken | null>(
		null,
	);
	const [deletions, setDeletions] = useState<DeletionOperation[]>([]);
	const queueDeletion = (operation: DeletionOperation) => {
		setDeletions((previous) => [...previous, operation]);
		window.location.hash = "operations";
	};
	useEffect(() => {
		const heading = document.querySelector<HTMLElement>(".demo-shell h1");
		if (heading) {
			heading.tabIndex = -1;
			heading.focus({ preventScroll: true });
			document.title = `${heading.textContent} · MeloTrove · React-макет`;
		}
		const main = document.querySelector<HTMLElement>(".demo-shell");
		if (main) main.tabIndex = -1;
	}, [route, preview]);
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
	const openPreview = (ids: Iterable<string>) => {
		const frozenIds = [...ids];
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
			applyMetadataPreview(
				previous,
				new Set(token.selectedIds),
				files,
				artists,
			),
		);
		return true;
	};
	const openContextPublication = (
		release: (typeof releases)[number],
		trackId: string | null = null,
	) => {
		setIncoming((current) => ({
			...current,
			plan: makeReleasePlan(current, release, files, trackId),
			confirmDowngrade: false,
		}));
		window.location.hash = "publications";
	};
	const tabs: [View, string][] = [
		["artists", "Артисты"],
		["albums", "Все альбомы"],
		["tracks", "Все треки"],
	];
	if (preview || route === "publication-preview")
		return (
			<main className="demo-shell">
				{previewToken ? (
					<PublicationPreview
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
						returnRoute={
							previewOriginRoute.startsWith("track/")
								? previewOriginRoute
								: "library"
						}
						returnLabel={
							previewOriginRoute.startsWith("track/")
								? "Вернуться к треку"
								: "Вернуться в медиатеку"
						}
					/>
				) : (
					<>
						<h1>Предпросмотр медиатеки</h1>
						<p>Нет сохранённого предпросмотра метаданных.</p>
						<a href="#library">К медиатеке</a>
					</>
				)}
			</main>
		);
	if (route === "publications")
		return (
			<main className="demo-shell">
				<ContextPublicationPreview
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
						setIncoming((current) => ({ ...current, notice }));
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
			<main className="demo-shell selection-dock-screen">
				<ArtistScreen
					key={route}
					artistId={artistPath.slice(7)}
					artists={artists}
					releases={releases}
					files={files}
					selected={selected}
					query={query}
					filter={filter}
					onQueryChange={setQuery}
					onFilterChange={setFilter}
					onSelect={update}
					onQueueDeletion={queueDeletion}
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
			<main className="demo-shell">
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
			<main className="demo-shell">
				<ComponentsScreen />
			</main>
		);
	if (route.startsWith("release/")) {
		const release = releases.find(
			(item) => item.id === route.slice("release/".length),
		);
		return (
			<main className="demo-shell">
				<h1>
					{!release
						? "Издание не найдено в медиатеке"
						: artistReleaseHref(release, artists)
							? "Переход к артисту…"
							: "Для издания не указан артист"}
				</h1>
				<a href="#library">К медиатеке</a>
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
				<main className="demo-shell">
					<h1>Позиция не найдена</h1>
					<a href="#library">К медиатеке</a>
				</main>
			);
		return (
			<main className="demo-shell">
				<TrackScreen
					files={files}
					artists={artists}
					onQueueDeletion={queueDeletion}
					release={release}
					track={track}
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
					onMetadataPreview={(ids) => {
						openPreview(ids);
						window.location.hash = "publication-preview";
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
					next.notice = `Выбрана запись Local «${release.title ?? release.release_group_title ?? ""}» для сопоставления входящих.`;
					setIncoming(next);
					window.location.hash = `match/${group.id}`;
				}}
				genresLoaded={settings.genresLoaded}
			/>
		);
	if (route === "review")
		return (
			<IncomingScreen
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
			<main className="demo-shell">
				<SettingsScreen settings={settings} onSettings={setSettings} />
			</main>
		);
	if (route === "setup")
		return (
			<main className="demo-shell">
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
			<main className="demo-shell">
				<SourcesScreen
					roots={sourceRoots}
					onRoots={setSourceRoots}
					files={files}
					removed={removedRoots}
					workDirectory={settings.workDirectory}
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
						setRemovedRoots((previous) => new Set([...previous, id]));
					}}
				/>
			</main>
		);
	if (route === "operations")
		return (
			<main className="demo-shell">
				<OperationsScreen
					releases={releases}
					deletions={deletions}
					onRecover={() =>
						setReleases((previous) =>
							previous.map((release) => ({
								...release,
								tracks: release.tracks.map((track) =>
									track.id === recoveryFixture.trackId && track.published
										? {
												...track,
												recovered: true,
												metadataBaseline: structuredClone(track.published.tags),
												metadataBaselineExtras: {
													coverHash: track.published.coverHash || null,
													lyrics: track.published.lyrics || null,
												},
											}
										: track,
								),
							})),
						)
					}
				/>
			</main>
		);
	if (route && route !== "library")
		return (
			<main className="demo-shell">
				<h1>Экран ещё не перенесён</h1>
				<p>
					Маршрут: {route}. Чтобы не подменять исходный сценарий упрощённой
					демонстрацией, он пока доступен только в старом прототипе.
				</p>
				<a href={`/reference/index.html#${encodeURI(route)}`}>
					Открыть исходный экран
				</a>
				<p>
					<a href="#library">К медиатеке React</a>
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
		<main className="demo-shell catalogue-shell selection-dock-screen">
			<header>
				<h1>{artistId ? artistName(artistId) : "Медиатека"}</h1>
				{artistId && (
					<Button
						onPress={() => {
							setArtistId(null);
							setView("artists");
						}}
					>
						К медиатеке
					</Button>
				)}
			</header>
			<div className="catalogue-controls">
				<Tabs
					selectedKey={view}
					onSelectionChange={(key) => {
						setView(key as View);
						setArtistId(null);
						setExpanded(null);
					}}
					className="catalogue-tabs"
				>
					<TabList aria-label="Вид медиатеки">
						{tabs.map(([key, label]) => (
							<Tab key={key} id={key}>
								{label}
							</Tab>
						))}
					</TabList>
					<TabPanel id={view}>
						<div className="catalogue-panel-controls">
							<SearchField
								aria-label="Поиск в медиатеке"
								value={query}
								onChange={setQuery}
								className="catalogue-search"
							>
								<Input placeholder="Артист, альбом, трек…" />
							</SearchField>
							<CatalogFilterControl value={filter} onChange={setFilter} />
						</div>
						<section
							className="demo-list"
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
											missingGroups.filter(
												(group) => group.artistId === artist.id,
											).length,
										]),
									)
								}
								hasAudio={(track) => hasAudio(track as Track)}
								files={files}
								filter={filter}
								groupCheckbox={(ids, label) => (
									<GroupCheckbox
										ids={ids}
										label={label}
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
										<section className="demo-absent" key={group.id}>
											<h2>{group.title}</h2>
											<p>
												{artistName(group.artistId)} · альбом отсутствует в
												медиатеке
											</p>
											<Select
												aria-label={`Выберите издание для ${group.title}`}
												selectedKey={editions[group.id] ?? "none"}
												onSelectionChange={(key) => {
													const value =
														String(key) === "none" ? "" : String(key);
													setEditions((previous) => ({
														...previous,
														[group.id]: value,
													}));
												}}
												className="catalogue-filter"
											>
												<Button>
													<SelectValue />
													<span aria-hidden="true">⌄</span>
												</Button>
												<Popover>
													<ListBox>
														<ListBoxItem id="none">
															Выберите издание
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
											<p>Просмотр не добавляет альбом в медиатеку.</p>
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
