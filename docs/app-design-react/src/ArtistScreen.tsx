import { useEffect, useMemo, useRef, useState } from "react";
import { TagsComparison, type TagScope } from "./TagsComparison";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
import {
	availabilityRowClass,
	type CatalogFilter as CatalogFilterValue,
} from "./availabilityRows";
import { CatalogFilter } from "./CatalogFilter";
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
import type { DeletionOperation } from "./DeletionRequest";
import { DeletionRequest } from "./DeletionRequest";
import { catalogueStyles } from "./catalogueStyles";
import {
	Button,
	Checkbox,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	ListBox,
	ListBoxItem,
	Popover,
	SearchField,
	Select,
	SelectValue,
} from "./ui";

const releaseFields = (release: OriginalRelease) =>
	release.credits?.some((credit) => credit.artistId) ?? false;

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
	initialTags,
	initialReleaseId,
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
	initialTags?: { scope: TagScope; releaseId?: string };
	initialReleaseId?: string;
}) {
	const [localQuery, setLocalQuery] = useState("");
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
	const artist = artists.find((item) => item.id === artistId);
	const [absentEdition, setAbsentEdition] = useState<Record<string, string>>(
		{},
	);
	const [artistTagsOpen, setArtistTagsOpen] = useState(
		initialTags?.scope === "artist",
	);
	const [releaseTagsOpen, setReleaseTagsOpen] = useState<
		Record<string, boolean>
	>(() =>
		initialTags?.scope === "release" && initialTags.releaseId
			? { [initialTags.releaseId]: true }
			: {},
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
			releases.filter(
				(release) =>
					release.artistId === artistId ||
					(releaseFields(release) &&
						release.credits?.some((credit) => credit.artistId === artistId)),
			),
		[artistId, releases],
	);
	const artistTagReleases = useMemo(
		() =>
			artistReleases
				.map((release) => ({
					...release,
					tracks: release.tracks.filter((track) => track.artistId === artistId),
				}))
				.filter((release) => release.tracks.length > 0),
		[artistId, artistReleases],
	);
	const tracks = artistReleases.flatMap((release) => release.tracks);
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
	const filteredMissingGroups = missingGroups.filter((group) => {
		if (
			group.artistId !== artistId ||
			artistReleases.some(
				(release) =>
					(release as OriginalRelease & { releaseGroupId?: string })
						.releaseGroupId === group.groupId,
			)
		)
			return false;
		if (filter !== "all" && filter !== "missing") return false;
		const searchable =
			`${group.title} ${group.editions.map((edition) => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(" ")}`).join(" ")}`
				.normalize("NFKC")
				.toLocaleLowerCase();
		return queryTerms.every((term) => searchable.includes(term));
	});
	if (!artist)
		return (
			<header className={catalogueStyles.objectHeader}>
				<div>
					<h1>Артист не найден</h1>
					<p>Эта ссылка не соответствует артисту медиатеки.</p>
				</div>
				<a href="#library">Вся медиатека</a>
			</header>
		);
	const counts = (predicate: (track: OriginalTrack) => boolean) =>
		tracks.filter(predicate).length;
	const expandedReleaseId =
		initialTags?.scope === "release" ? initialTags.releaseId : initialReleaseId;
	return (
		<section className={catalogueStyles.object}>
			<nav className={catalogueStyles.heroBack} aria-label="Навигация">
				<a href="#library">К медиатеке</a>
			</nav>
			<header
				className={`${catalogueStyles.objectHeader} ${catalogueStyles.hero}`}
			>
				<div
					className={catalogueStyles.heroArt}
					data-palette={artist.color || "clay"}
				>
					<span aria-hidden="true">
						{artist.name.trim().slice(0, 1).toLocaleUpperCase()}
					</span>
					<i aria-hidden="true" />
					<div className={catalogueStyles.cardSelection}>
						<Checkbox
							aria-label={`Выбрать все позиции артиста ${artist.name}`}
							className={catalogueStyles.cardCheckbox}
							isSelected={ids.length > 0 && selectedCount === ids.length}
							isIndeterminate={selectedCount > 0 && selectedCount < ids.length}
							isDisabled={!ids.length || !onSelect}
							onChange={(value) => onSelect?.(ids, value)}
						>
							<span
								className={catalogueStyles.cardCheckboxBox}
								aria-hidden="true"
							>
								{ids.length > 0 && selectedCount === ids.length
									? "✓"
									: selectedCount > 0
										? "−"
										: ""}
							</span>
							<span className="sr-only">Выбрать все позиции артиста</span>
						</Checkbox>
					</div>
				</div>
				<div className={catalogueStyles.heroBody}>
					<div className={catalogueStyles.heroHeading}>
						<p className={catalogueStyles.heroKicker}>ПРОФИЛЬ АРТИСТА</p>
						<h1>{artist.name}</h1>
						<p className={catalogueStyles.heroGenre}>
							{artist.genre || "Исполнитель"}
						</p>
					</div>
					<div className={catalogueStyles.heroFoot}>
						<p className={catalogueStyles.heroStats}>
							{artistReleases.length} изданий · {tracks.length} позиций ·{" "}
							{counts((track) => Boolean(track.published))} опубликовано ·{" "}
							{filteredMissingGroups.length} альбомов без издания
						</p>
						<div
							className={`${catalogueStyles.actions} ${catalogueStyles.artistDanger}`}
						>
							<Button
								aria-expanded={artistTagsOpen}
								onPress={() => setArtistTagsOpen((open) => !open)}
							>
								Теги артиста
							</Button>
							{onQueueDeletion ? (
								<ArtistDeletionRequest
									artistId={artist.id}
									artists={artists}
									releases={releases}
									onQueue={onQueueDeletion}
								/>
							) : (
								<Button
									className="border-[color-mix(in_srgb,var(--error)_45%,var(--control))]! bg-transparent! text-[var(--error)]!"
									isDisabled={!onDeleteArtist}
									onPress={() => onDeleteArtist?.(artist.id)}
								>
									Удалить артиста
								</Button>
							)}
						</div>
					</div>
				</div>
			</header>
			{artistTagsOpen && (
				<div
					ref={initialTagsTarget}
					tabIndex={-1}
					className="inline-tags-comparison"
				>
					<TagsComparison
						scope="artist"
						releases={artistTagReleases}
						artists={artists}
						files={files}
						initialTrackId={
							artistTagReleases.flatMap((release) => release.tracks)[0]?.id
						}
					/>
				</div>
			)}
			<div className={catalogueStyles.controls}>
				<div className={catalogueStyles.panelControls}>
					<SearchField
						aria-label="Поиск в медиатеке"
						value={query}
						onChange={changeQuery}
						className={catalogueStyles.search}
					>
						<Input placeholder="Артист, альбом, трек…" />
					</SearchField>
					<CatalogFilter value={filter} onChange={changeFilter} />
				</div>
			</div>
			<DisclosureGroup
				key={artistId}
				className={catalogueStyles.editions}
				allowsMultipleExpanded
				defaultExpandedKeys={
					expandedReleaseId ? new Set([expandedReleaseId]) : undefined
				}
			>
				{artistReleases.map((release, index) => {
					const durationTotal =
						release.tracks.length > 0 &&
						release.tracks.every((track) => Number.isFinite(track.duration))
							? release.tracks.reduce(
									(sum, track) => sum + (track.duration ?? 0),
									0,
								)
							: null;
					const pubCount = release.tracks.filter(
						(track) => track.published,
					).length;
					const provider = providerFor(release);
					const complete = providerComplete(release);
					const changes = release.tracks.filter((track) =>
						metadataChanged(release, track, artists),
					).length;
					return (
						<div className={catalogueStyles.albumRow} key={release.id}>
							<Disclosure className={catalogueStyles.albumCard} id={release.id}>
								<div className={catalogueStyles.cardSelection}>
									<Checkbox
										aria-label={`Выбрать все позиции альбома ${release.title} · ${release.year} · ${release.catalog || "—"}`}
										className={catalogueStyles.cardCheckbox}
										isSelected={
											release.tracks.length > 0 &&
											release.tracks.every((track) => selected.has(track.id))
										}
										isIndeterminate={
											release.tracks.some((track) => selected.has(track.id)) &&
											!release.tracks.every((track) => selected.has(track.id))
										}
										isDisabled={!release.tracks.length || !onSelect}
										onChange={(value) =>
											onSelect?.(
												release.tracks.map((track) => track.id),
												value,
											)
										}
									>
										<span
											className={catalogueStyles.cardCheckboxBox}
											aria-hidden="true"
										>
											{release.tracks.length > 0 &&
											release.tracks.every((track) => selected.has(track.id))
												? "✓"
												: release.tracks.some((track) => selected.has(track.id))
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
											aria-label={`${release.title} · ${release.year} · ${release.catalog || "—"}`}
										>
											<span
												className={`artist-album-cover ${catalogueStyles.albumCover}`}
												data-palette={release.color || artist.color || "clay"}
												aria-hidden="true"
											>
												<span>
													{release.title.trim().slice(0, 1).toLocaleUpperCase()}
												</span>
											</span>
											<span className={catalogueStyles.albumCopy}>
												<strong>{release.title}</strong>
												<span className={catalogueStyles.albumMeta}>
													{durationTotal === null
														? "Длительность неизвестна"
														: `${Math.floor(durationTotal / 60)}:${String(Math.round(durationTotal % 60)).padStart(2, "0")} всего`}
													·{" "}
													{release.provider === "Local"
														? "Local"
														: release.providerId
															? `${release.provider || "MusicBrainz"} · ${release.providerId}`
															: "Локальное издание"}
												</span>
												<span className={catalogueStyles.albumMeta}>
													{release.year} · {release.tracks.length} позиций ·{" "}
													{release.country || "Страна не указана"} ·{" "}
													{release.label || "Локальный"} ·{" "}
													{release.catalog || "—"}
												</span>
												<span className={catalogueStyles.albumMeta}>
													{complete && provider
														? `${pubCount}/${provider.tracks.length} опубликовано`
														: `${pubCount} опубликовано · полнота неизвестна`}
													{changes
														? ` · Метаданные изменились у ${changes} позиций`
														: ""}
												</span>
											</span>
										</Button>
									</Heading>
									{onQueueDeletion && (
										<DeletionRequest
											iconOnly
											triggerLabel={`Удалить альбом ${release.title} · ${release.year} · ${release.catalog || "—"}`}
											release={release}
											onQueue={onQueueDeletion}
										/>
									)}
								</div>
								<DisclosurePanel className={catalogueStyles.albumPanel}>
									<div
										className={`${catalogueStyles.actions} ${catalogueStyles.albumActions}`}
									>
										<Button
											aria-expanded={Boolean(releaseTagsOpen[release.id])}
											onPress={() =>
												setReleaseTagsOpen((open) => ({
													...open,
													[release.id]: !open[release.id],
												}))
											}
										>
											Теги альбома
										</Button>
									</div>
									{releaseTagsOpen[release.id] && (
										<div
											ref={
												initialTags?.releaseId === release.id
													? initialTagsTarget
													: undefined
											}
											tabIndex={
												initialTags?.releaseId === release.id ? -1 : undefined
											}
											className="inline-tags-comparison"
										>
											<TagsComparison
												scope="release"
												releases={[release]}
												artists={artists}
												files={files}
												initialTrackId={release.tracks[0]?.id}
											/>
										</div>
									)}
									<div className={catalogueStyles.previewTable}>
										<table>
											<thead>
												<tr>
													{[
														"Выбрать все позиции этого издания",
														"№",
														"Композиция",
														"Параметры",
														"Есть аудио",
														"Связей с исходниками",
														"Публикация",
													].map((label) => (
														<th
															scope="col"
															key={label}
															className={
																label.startsWith("Выбрать")
																	? catalogueStyles.selectionColumn
																	: undefined
															}
														>
															{label.startsWith("Выбрать") ? (
																<Checkbox
																	className={catalogueStyles.tableCheckbox}
																	aria-label={label}
																	isSelected={
																		release.tracks.length > 0 &&
																		release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																	}
																	isIndeterminate={
																		release.tracks.some((track) =>
																			selected.has(track.id),
																		) &&
																		!release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																	}
																	isDisabled={
																		!release.tracks.length || !onSelect
																	}
																	onChange={(value) =>
																		onSelect?.(
																			release.tracks.map((track) => track.id),
																			value,
																		)
																	}
																>
																	<span
																		className={catalogueStyles.checkboxBox}
																		aria-hidden="true"
																	>
																		{release.tracks.length > 0 &&
																		release.tracks.every((track) =>
																			selected.has(track.id),
																		)
																			? "✓"
																			: release.tracks.some((track) =>
																						selected.has(track.id),
																					)
																				? "−"
																				: ""}
																	</span>
																</Checkbox>
															) : (
																label
															)}
														</th>
													))}
												</tr>
											</thead>
											<tbody>
												{release.tracks.map((track) => {
													const sourceIds = [
														...new Set([
															...(track.sourceIds ?? []),
															...(track.sourceId ? [track.sourceId] : []),
														]),
													];
													const source = track.sourceId
														? files[track.sourceId]
														: undefined;
													return (
														<tr
															key={track.id}
															className={availabilityRowClass(
																track,
																filter,
																hasAudio,
															)}
														>
															<td className={catalogueStyles.selectionColumn}>
																<Checkbox
																	className={catalogueStyles.tableCheckbox}
																	aria-label={`Выбрать ${track.title}`}
																	isSelected={selected.has(track.id)}
																	isDisabled={!onSelect}
																	onChange={(value) =>
																		onSelect?.([track.id], value)
																	}
																>
																	<span
																		className={catalogueStyles.checkboxBox}
																		aria-hidden="true"
																	>
																		{selected.has(track.id) ? "✓" : ""}
																	</span>
																</Checkbox>
															</td>
															<td className="align-middle">
																{trackNumberLabel(release, track)}
															</td>
															<td>
																<a href={`#track/${track.id}`}>{track.title}</a>
																<span className={catalogueStyles.secondary}>
																	{track.duration === null
																		? "—"
																		: `${Math.floor(track.duration / 60)}:${String(Math.round(track.duration % 60)).padStart(2, "0")}`}
																</span>
																{track.recordingId ? null : (
																	<span className={catalogueStyles.secondary}>
																		Новая запись
																	</span>
																)}
																{track.observedFile && (
																	<span className={catalogueStyles.secondary}>
																		Файл изменён вне приложения
																	</span>
																)}
															</td>
															<td>
																{source
																	? `${source.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} · ${source.codec}`
																	: "—"}
															</td>
															<td>{hasAudio(track) ? "Да" : "Нет"}</td>
															<td>{sourceIds.length}</td>
															<td>
																{track.published ? (
																	<>
																		{track.published.container} ·{" "}
																		{track.published.codec}
																		{metadataChanged(
																			release,
																			track,
																			artists,
																		) && (
																			<span
																				className={catalogueStyles.secondary}
																			>
																				Метаданные изменились
																			</span>
																		)}
																	</>
																) : (
																	"Не опубликовано"
																)}
															</td>
														</tr>
													);
												})}
											</tbody>
										</table>
									</div>
									{!release.tracks.length && <p>Позиции не добавлены.</p>}
								</DisclosurePanel>
							</Disclosure>
						</div>
					);
				})}
			</DisclosureGroup>
			{filteredMissingGroups.length > 0 && (
				<section className={catalogueStyles.artistPanel}>
					<h2>Отсутствующие альбомы</h2>
					<p>
						Известная часть каталога провайдера. Выберите издание, чтобы
						посмотреть его треки. Просмотр не добавляет музыку в медиатеку и не
						скачивает файлы.
					</p>
					<div className={catalogueStyles.previewTable}>
						<table>
							<thead>
								<tr>
									{["Альбом", "Выбор издания", "Детали издания", "Треки"].map(
										(label) => (
											<th scope="col" key={label}>
												{label}
											</th>
										),
									)}
								</tr>
							</thead>
							<tbody>
								{filteredMissingGroups.map((group) => {
									const chosen = group.editions.find(
										(edition) => edition.id === absentEdition[group.id],
									);
									return (
										<tr key={group.id}>
											<th scope="row">
												{group.title}
												<span className={catalogueStyles.secondary}>
													{originalArtistName(group.artistId, artists)} ·
													альбом, которого нет в медиатеке
												</span>
											</th>
											<td>
												<Select
													aria-label={`Выберите издание группы ${group.title}`}
													selectedKey={absentEdition[group.id] || "none"}
													onSelectionChange={(key) =>
														setAbsentEdition((value) => ({
															...value,
															[group.id]:
																String(key) === "none" ? "" : String(key),
														}))
													}
													className={catalogueStyles.select}
												>
													<Button>
														<SelectValue />
														<span aria-hidden="true">⌄</span>
													</Button>
													<Popover className={catalogueStyles.popover}>
														<ListBox>
															<ListBoxItem id="none">
																Выберите конкретное издание
															</ListBoxItem>
															{group.editions.map((edition) => (
																<ListBoxItem key={edition.id} id={edition.id}>
																	{edition.year} · {edition.country} ·{" "}
																	{edition.format} · {edition.titles.length}{" "}
																	поз. · {edition.disambiguation}
																</ListBoxItem>
															))}
														</ListBox>
													</Popover>
												</Select>
											</td>
											<td>
												{chosen
													? `${chosen.year} · ${chosen.country} · ${chosen.format} · ${chosen.disambiguation}`
													: "—"}
											</td>
											<td>
												{chosen
													? chosen.titles.map((title, index) => (
															<div key={`${index}:${title}`}>
																{index + 1}. {title}
															</div>
														))
													: "Выберите конкретное издание; выбор не создаёт запись в медиатеке."}
											</td>
										</tr>
									);
								})}
							</tbody>
						</table>
					</div>
				</section>
			)}
		</section>
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
