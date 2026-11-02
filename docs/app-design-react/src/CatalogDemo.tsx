import { useState, useSyncExternalStore } from "react";
import { Checkbox } from "react-aria-components";
import {
	catalogData,
	originalReleases,
	applyMetadataPreview,
} from "./catalogData";
import { ThemeControl } from "./ThemeControl";
import { PublicationPreview } from "./PublicationPreview";
import { TrackScreen } from "./TrackScreen";
import { ReleaseScreen } from "./ReleaseScreen";

const subscribeRoute = (listener: () => void) => {
	window.addEventListener("hashchange", listener);
	return () => window.removeEventListener("hashchange", listener);
};

type View = "artists" | "albums" | "tracks";
type Track = (typeof catalogData.releases)[number]["tracks"][number];
type Availability = "all" | "available" | "missing";
type Publication = "all" | "published" | "unpublished";

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
const files: Record<string, { available: boolean; located: boolean }> =
	catalogData.files;
const hasAudio = (track: Track) =>
	track.published ||
	[...track.sourceIds, ...(track.sourceId ? [track.sourceId] : [])].some(
		(id) => files[id]?.available && files[id]?.located,
	);

export function CatalogDemo() {
	const route = useSyncExternalStore(
		subscribeRoute,
		() => window.location.hash.slice(1),
		() => "library",
	);
	const [releases, setReleases] = useState(() =>
		structuredClone(originalReleases),
	);
	const fixture = {
		...catalogData,
		releases: releases.map((release) => ({
			...release,
			tracks: release.tracks.map((track) => ({
				...track,
				sourceIds: track.sourceIds ?? [],
				published: Boolean(track.published),
			})),
		})),
	};
	const allTracks = fixture.releases.flatMap((release) => release.tracks);
	const [view, setView] = useState<View>("artists");
	const [preview, setPreview] = useState(false);
	const [contextIds, setContextIds] = useState<string[] | null>(null);
	const [artistId, setArtistId] = useState<string | null>(null);
	const [query, setQuery] = useState("");
	const [selected, setSelected] = useState<Set<string>>(() => new Set());
	const [expanded, setExpanded] = useState<string | null>(null);
	const [editions, setEditions] = useState<Record<string, string>>({});
	const [availability, setAvailability] = useState<Availability>("all");
	const [publication, setPublication] = useState<Publication>("all");
	const normalized = query.trim().toLocaleLowerCase();
	const matchesAvailability = (tracks: Track[]) =>
		availability === "all" ||
		tracks.some((track) =>
			availability === "available" ? hasAudio(track) : !hasAudio(track),
		);
	const matchesPublication = (tracks: Track[]) =>
		publication === "all" ||
		tracks.some((track) =>
			publication === "published" ? track.published : !track.published,
		);
	const matchesGroup = (tracks: Track[]) =>
		matchesAvailability(tracks) && matchesPublication(tracks);
	const artistName = (id: string) =>
		fixture.artists.find((artist) => artist.id === id)?.name ?? "";
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
	const tabs: [View, string][] = [
		["artists", "Артисты"],
		["albums", "Все альбомы"],
		["tracks", "Все треки"],
	];
	if (preview)
		return (
			<main className="demo-shell">
				<PublicationPreview
					selected={contextIds ? new Set(contextIds) : selected}
					releases={releases}
					onApply={() =>
						setReleases((previous) =>
							applyMetadataPreview(
								previous,
								contextIds ? new Set(contextIds) : selected,
							),
						)
					}
					onBack={() => setPreview(false)}
				/>
			</main>
		);
	if (route.startsWith("release/")) {
		const release = releases.find(
			(item) => item.id === route.slice("release/".length),
		);
		if (!release)
			return (
				<main className="demo-shell">
					<h1>Издание не найдено в медиатеке</h1>
					<a href="#library">К медиатеке</a>
				</main>
			);
		return (
			<main className="demo-shell">
				<ThemeControl />
				<ReleaseScreen
					release={release}
					selected={selected}
					onSelect={update}
					onPreview={(ids) => {
						setContextIds(ids);
						setPreview(true);
					}}
				/>
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
				<ThemeControl />
				<TrackScreen
					release={release}
					track={track}
					onPreview={() => {
						setContextIds([track.id]);
						setPreview(true);
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
	return (
		<main className="demo-shell">
			<header>
				<ThemeControl />
				<p className="demo-eyebrow">MeloTrove · стенд в разработке</p>
				<h1>{artistId ? artistName(artistId) : "Медиатека"}</h1>
				<p>Демонстрационные данные · перенесён только выбор в каталоге</p>
				{!new URLSearchParams(window.location.search).has("embedded") && (
					<a href="/compare">Сравнить со старым прототипом</a>
				)}
				{artistId && (
					<button
						type="button"
						onClick={() => {
							setArtistId(null);
							setView("artists");
						}}
					>
						К медиатеке
					</button>
				)}
			</header>
			<nav className="demo-tabs" aria-label="Вид медиатеки">
				{tabs.map(([key, label]) => (
					<button
						key={key}
						type="button"
						aria-pressed={view === key}
						onClick={() => {
							setView(key);
							setArtistId(null);
							setExpanded(null);
						}}
					>
						{label}
					</button>
				))}
			</nav>
			<label className="demo-search">
				Поиск в медиатеке
				<input
					type="search"
					value={query}
					onChange={(event) => setQuery(event.currentTarget.value)}
					placeholder="Артист, альбом, трек…"
				/>
			</label>
			<div className="demo-filters">
				<label>
					Наличие{" "}
					<select
						value={availability}
						onChange={(event) =>
							setAvailability(event.currentTarget.value as Availability)
						}
					>
						<option value="all">Все</option>
						<option value="available">В наличии</option>
						<option value="missing">Не хватает</option>
					</select>
				</label>
				<label>
					Публикация{" "}
					<select
						value={publication}
						onChange={(event) =>
							setPublication(event.currentTarget.value as Publication)
						}
					>
						<option value="all">Все</option>
						<option value="published">Опубликованные</option>
						<option value="unpublished">Не опубликованные</option>
					</select>
				</label>
			</div>
			{selected.size > 0 && (
				<div className="demo-selection" role="status">
					Выбрано позиций: {selected.size}
					<button
						type="button"
						onClick={() => {
							setContextIds(null);
							setPreview(true);
						}}
					>
						Предпросмотр публикации
					</button>
					<button type="button" onClick={() => setSelected(new Set())}>
						Снять выбор
					</button>
				</div>
			)}
			<section
				className="demo-list"
				aria-label={tabs.find(([key]) => key === view)?.[1]}
			>
				{view === "artists" &&
					fixture.artists
						.filter(
							(artist) =>
								artist.name.toLocaleLowerCase().includes(normalized) &&
								fixture.releases.some(
									(release) =>
										release.artistId === artist.id &&
										matchesGroup(release.tracks),
								),
						)
						.map((artist) => {
							const releases = fixture.releases.filter(
								(release) => release.artistId === artist.id,
							);
							const tracks = releases.flatMap((release) => release.tracks);
							return (
								<div className="demo-row" key={artist.id}>
									<GroupCheckbox
										ids={trackIds(tracks)}
										label={`Выбрать позиции артиста ${artist.name}`}
										selected={selected}
										onChange={update}
									/>
									<span className="demo-initial" aria-hidden="true">
										{artist.name[0]}
									</span>
									<div className="demo-copy">
										<button
											className="demo-link"
											type="button"
											onClick={() => {
												setArtistId(artist.id);
												setQuery("");
												setView("albums");
											}}
										>
											{artist.name}
										</button>
										<span>
											{releases.length} изданий · {tracks.length} позиций
										</span>
									</div>
									<span className="demo-count">
										Опубликовано{" "}
										{tracks.filter((track) => track.published).length} /{" "}
										{tracks.length}
									</span>
								</div>
							);
						})}
				{view === "albums" &&
					fixture.releases
						.filter(
							(release) =>
								(!artistId || release.artistId === artistId) &&
								`${release.title} ${artistName(release.artistId)} ${release.catalog}`
									.toLocaleLowerCase()
									.includes(normalized) &&
								matchesGroup(release.tracks),
						)
						.map((release) => (
							<div key={release.id}>
								<div className="demo-row">
									<GroupCheckbox
										ids={trackIds(release.tracks)}
										label={`Выбрать позиции альбома ${release.title}`}
										selected={selected}
										onChange={update}
									/>
									<div className="demo-copy">
										<button
											className="demo-link"
											type="button"
											aria-expanded={expanded === release.id}
											onClick={() =>
												setExpanded(expanded === release.id ? null : release.id)
											}
										>
											{release.title}
										</button>
										<a href={`#release/${release.id}`}>Открыть альбом</a>
										<span>
											{
												fixture.artists.find(
													(artist) => artist.id === release.artistId,
												)?.name
											}{" "}
											· {release.year} · {release.tracks.length} позиций
										</span>
									</div>
									<span className="demo-count">
										Опубликовано{" "}
										{release.tracks.filter((track) => track.published).length} /{" "}
										{release.tracks.length}
									</span>
								</div>
								{expanded === release.id &&
									release.tracks.map((track) => (
										<div className="demo-track" key={track.id}>
											<GroupCheckbox
												ids={[track.id]}
												label={`Выбрать ${track.title}`}
												selected={selected}
												onChange={update}
											/>
											<a href={`#track/${track.id}`}>
												{track.position}. {track.title}
											</a>
										</div>
									))}
							</div>
						))}
				{view === "tracks" &&
					allTracks
						.filter(
							(track) =>
								`${track.title} ${fixture.releases.find((release) => release.tracks.some((item) => item.id === track.id))?.title ?? ""}`
									.toLocaleLowerCase()
									.includes(normalized) && matchesGroup([track]),
						)
						.map((track) => (
							<div className="demo-row" key={track.id}>
								<GroupCheckbox
									ids={[track.id]}
									label={`Выбрать ${track.title}`}
									selected={selected}
									onChange={update}
								/>
								<div className="demo-copy">
									<a href={`#track/${track.id}`}>{track.title}</a>
									<span>
										{
											fixture.releases.find((release) =>
												release.tracks.some((item) => item.id === track.id),
											)?.title
										}
									</span>
								</div>
								<span className="demo-count">
									{track.published ? "Опубликовано" : "Не опубликовано"}
								</span>
							</div>
						))}
			</section>
			{view === "albums" &&
				availability !== "available" &&
				publication !== "published" &&
				fixture.absentGroups
					.filter(
						(group) =>
							(!artistId || group.artistId === artistId) &&
							`${group.title} ${artistName(group.artistId)} ${group.editions.map((edition) => `${edition.disambiguation} ${edition.titles.join(" ")}`).join(" ")}`
								.toLocaleLowerCase()
								.includes(normalized),
					)
					.map((group) => {
						const chosen = group.editions.find(
							(edition) => edition.id === editions[group.id],
						);
						return (
							<section className="demo-absent" key={group.id}>
								<h2>{group.title}</h2>
								<p>
									{artistName(group.artistId)} · альбом отсутствует в медиатеке
								</p>
								<label>
									Выберите издание{" "}
									<select
										value={editions[group.id] ?? ""}
										onChange={(event) => {
											const value = event.currentTarget.value;
											setEditions((previous) => ({
												...previous,
												[group.id]: value,
											}));
										}}
									>
										<option value="">Выберите издание</option>
										{group.editions.map((edition) => (
											<option key={edition.id} value={edition.id}>
												{edition.year} · {edition.country} · {edition.format} ·{" "}
												{edition.disambiguation}
											</option>
										))}
									</select>
								</label>
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
		</main>
	);
}
