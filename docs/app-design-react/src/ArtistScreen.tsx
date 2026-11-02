import { useMemo, useState } from "react";
import { trackNumberLabel } from "./catalogData";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
import {
	Button,
	Checkbox,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Radio,
	RadioGroup,
	SearchField,
	Select,
	SelectValue,
} from "./ui";
import type { DeletionOperation } from "./DeletionRequest";
import {
	catalogData,
	metadataChanged,
	originalArtistName,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";
import {
	availabilityRowClass,
	type AvailabilityFilter,
} from "./availabilityRows";

type Availability = "all" | "available" | "missing";
type Publication = "all" | "published" | "unpublished";

const releaseFields = (release: OriginalRelease) =>
	release.credits?.some((credit) => credit.artistId) ?? false;

export function ArtistScreen({
	artistId,
	artists,
	releases,
	files,
	query: sharedQuery,
	availability: sharedAvailability,
	publication: sharedPublication,
	selected = new Set(),
	onSelect,
	onPreview,
	onContextPreview,
	onQueryChange,
	onAvailabilityChange,
	onPublicationChange,
	onDeleteArtist,
	onQueueDeletion,
}: {
	artistId: string;
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	files: SourceFiles;
	query?: string;
	availability?: AvailabilityFilter;
	publication?: Publication;
	selected?: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onPreview?: (ids: string[]) => void;
	onContextPreview?: (release: OriginalRelease) => void;
	onQueryChange?: (value: string) => void;
	onAvailabilityChange?: (value: Availability) => void;
	onPublicationChange?: (value: Publication) => void;
	onDeleteArtist?: (artistId: string) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
}) {
	const [localQuery, setLocalQuery] = useState("");
	const [localAvailability, setLocalAvailability] =
		useState<Availability>("all");
	const [localPublication, setLocalPublication] = useState<Publication>("all");
	const query = sharedQuery ?? localQuery;
	const availability = sharedAvailability ?? localAvailability;
	const publication = sharedPublication ?? localPublication;
	const changeQuery = (value: string) => {
		setLocalQuery(value);
		onQueryChange?.(value);
	};
	const changeAvailability = (value: Availability) => {
		setLocalAvailability(value);
		onAvailabilityChange?.(value);
	};
	const changePublication = (value: Publication) => {
		setLocalPublication(value);
		onPublicationChange?.(value);
	};
	const artist = artists.find((item) => item.id === artistId);
	const [absentEdition, setAbsentEdition] = useState<Record<string, string>>(
		{},
	);
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
		if (availability === "available") return false;
		const searchable =
			`${group.title} ${group.editions.map((edition) => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(" ")}`).join(" ")}`
				.normalize("NFKC")
				.toLocaleLowerCase();
		return queryTerms.every((term) => searchable.includes(term));
	});
	if (!artist)
		return (
			<header className="demo-object-header">
				<div>
					<h1>Артист не найден</h1>
					<p>Эта ссылка не соответствует артисту медиатеки.</p>
				</div>
				<a href="#library">Вся медиатека</a>
			</header>
		);
	const counts = (predicate: (track: OriginalTrack) => boolean) =>
		tracks.filter(predicate).length;
	return (
		<section className="catalogue-object">
			<nav className="artist-hero-back" aria-label="Навигация">
				<a href="#library">К медиатеке</a>
			</nav>
			<header className="demo-object-header artist-hero">
				<div className="artist-hero-art" data-palette={artist.color || "clay"}>
					<span aria-hidden="true">
						{artist.name.trim().slice(0, 1).toLocaleUpperCase()}
					</span>
					<i aria-hidden="true" />
					<div className="artist-card-selection">
						<Checkbox
							aria-label={`Выбрать все позиции артиста ${artist.name}`}
							className="demo-checkbox"
							isSelected={ids.length > 0 && selectedCount === ids.length}
							isIndeterminate={selectedCount > 0 && selectedCount < ids.length}
							isDisabled={!ids.length || !onSelect}
							onChange={(value) => onSelect?.(ids, value)}
						>
							<span className="demo-checkbox-box" aria-hidden="true">
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
				<div className="artist-hero-body">
					<div className="artist-hero-heading">
						<p className="artist-kicker">ПРОФИЛЬ АРТИСТА</p>
						<h1>{artist.name}</h1>
						<p className="artist-genre">{artist.genre || "Исполнитель"}</p>
					</div>
					<div className="artist-hero-foot">
						<p className="artist-hero-stats">
							{artistReleases.length} изданий · {tracks.length} позиций ·{" "}
							{counts((track) => Boolean(track.published))} опубликовано ·{" "}
							{filteredMissingGroups.length} альбомов без издания
						</p>
						<div className="demo-object-actions artist-hero-actions">
							<Button
								aria-label={`Предпросмотр ${selectedCount} выбранных позиций`}
								isDisabled={!selectedCount || !onPreview}
								onPress={() =>
									onPreview?.(ids.filter((id) => selected.has(id)))
								}
							>
								Предпросмотр
								<span className="artist-preview-count">{selectedCount}</span>
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
									className="ui-button--secondary ui-button--danger"
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
			<div className="catalogue-controls">
				<div className="catalogue-panel-controls">
					<SearchField
						aria-label="Поиск в медиатеке"
						value={query}
						onChange={changeQuery}
						className="catalogue-search"
					>
						<Input placeholder="Артист, альбом, трек…" />
					</SearchField>
					<RadioGroup
						value={availability}
						onChange={(value) => changeAvailability(value as Availability)}
						className="catalogue-filter catalogue-radio-filter"
					>
						<Label>Наличие</Label>
						<div className="catalogue-segments">
							<Radio value="all" className="catalogue-segment">
								Все
							</Radio>
							<Radio value="available" className="catalogue-segment">
								В наличии
							</Radio>
							<Radio value="missing" className="catalogue-segment">
								Не хватает
							</Radio>
						</div>
					</RadioGroup>
					<RadioGroup
						value={publication}
						onChange={(value) => changePublication(value as Publication)}
						className="catalogue-filter catalogue-radio-filter"
					>
						<Label>Публикация</Label>
						<div className="catalogue-segments">
							<Radio value="all" className="catalogue-segment">
								Все
							</Radio>
							<Radio value="published" className="catalogue-segment">
								Есть
							</Radio>
							<Radio value="unpublished" className="catalogue-segment">
								Нет
							</Radio>
						</div>
					</RadioGroup>
				</div>
			</div>
			{selectedCount > 0 && (
				<div
					className="demo-preview-counts"
					role="region"
					aria-label="Действия с выбранным"
				>
					<span>
						Выбраны только существующие позиции. Снимите отдельную отметку у
						позиции, чтобы исключить её.
					</span>
					<Button
						onPress={() => onPreview?.(ids.filter((id) => selected.has(id)))}
						isDisabled={!onPreview}
					>
						Предпросмотр {selectedCount} выбранных
					</Button>
					<Button onPress={() => onSelect?.(ids, false)} isDisabled={!onSelect}>
						Снять выбор артиста
					</Button>
				</div>
			)}
			<DisclosureGroup
				key={artistId}
				className="catalogue-editions"
				allowsMultipleExpanded
			>
				{artistReleases.map((release, index) => {
					const pubCount = release.tracks.filter(
						(track) => track.published,
					).length;
					const provider = providerFor(release);
					const complete = providerComplete(release);
					const changes = release.tracks.filter((track) =>
						metadataChanged(release, track, artists),
					).length;
					return (
						<div className="artist-release-row" key={release.id}>
							<Disclosure
								className="catalogue-edition artist-album-card"
								id={release.id}
							>
								<div className="artist-release-select artist-card-selection">
									<Checkbox
										aria-label={`Выбрать все позиции альбома ${release.title}`}
										className="demo-checkbox"
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
										<span className="demo-checkbox-box" aria-hidden="true">
											{release.tracks.length > 0 &&
											release.tracks.every((track) => selected.has(track.id))
												? "✓"
												: release.tracks.some((track) => selected.has(track.id))
													? "−"
													: ""}
										</span>
									</Checkbox>
								</div>
								<Heading>
									<Button slot="trigger" className="catalogue-edition-trigger">
										<span
											className="artist-album-cover"
											data-palette={release.color || artist.color || "clay"}
											aria-hidden="true"
										>
											<span>
												{release.title.trim().slice(0, 1).toLocaleUpperCase()}
											</span>
										</span>
										<span className="artist-album-copy">
											<strong>{release.title}</strong>
											<span className="artist-album-meta">
												{release.year} · {release.tracks.length} позиций ·{" "}
												{release.label || "Локальный"} · {release.catalog}
											</span>
											<span className="artist-album-meta">
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
								<DisclosurePanel>
									<div className="demo-object-actions">
										<a href={`#release/${release.id}`}>Открыть альбом</a>
										<Button
											isDisabled={!release.tracks.length || !onContextPreview}
											onPress={() => onContextPreview?.(release)}
										>
											Предпросмотр публикации
										</Button>
										{release.tracks[0] && (
											<>
												<a href={`#tags/${release.tracks[0].id}/artist`}>
													Теги артиста
												</a>
												<a href={`#tags/${release.tracks[0].id}/release`}>
													Теги альбома
												</a>
											</>
										)}
									</div>
									<div className="demo-preview-table">
										<table>
											<thead>
												<tr>
													{[
														"Выбрать",
														"№",
														"Композиция",
														"Параметры",
														"Есть аудио",
														"Связей с исходниками",
														"Публикация",
													].map((label) => (
														<th scope="col" key={label}>
															{label}
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
																availability,
																hasAudio,
																publication,
															)}
														>
															<td>
																<Checkbox
																	className="demo-checkbox"
																	aria-label={`Выбрать ${track.title}`}
																	isSelected={selected.has(track.id)}
																	isDisabled={!onSelect}
																	onChange={(value) =>
																		onSelect?.([track.id], value)
																	}
																>
																	<span
																		className="demo-checkbox-box"
																		aria-hidden="true"
																	>
																		{selected.has(track.id) ? "✓" : ""}
																	</span>
																</Checkbox>
															</td>
															<td>{trackNumberLabel(release, track)}</td>
															<td>
																<a href={`#track/${track.id}`}>{track.title}</a>
																{track.recordingId ? null : (
																	<span className="demo-secondary">
																		Новая запись
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
																			<span className="demo-secondary">
																				Метаданные изменились
																			</span>
																		)}
																		<details>
																			<summary>
																				Путь и снимок публикации
																			</summary>
																			<p>{track.published.path}</p>
																			<p>
																				{track.published.tags.TITLE.join(" · ")}{" "}
																				· {track.published.readAt}
																			</p>
																		</details>
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
				<section className="demo-panel">
					<h2>Отсутствующие альбомы</h2>
					<p>
						Известная часть каталога провайдера. Выберите издание, чтобы
						посмотреть его треки. Просмотр не добавляет музыку в медиатеку и не
						скачивает файлы.
					</p>
					<div className="demo-preview-table">
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
												<span className="demo-secondary">
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
													className="catalogue-filter"
												>
													<Button>
														<SelectValue />
														<span aria-hidden="true">⌄</span>
													</Button>
													<Popover>
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
