import { useMemo, useState } from "react";
import { Checkbox } from "react-aria-components";
import { ArtistDeletionRequest } from "./ArtistDeletionRequest";
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

type Availability = "all" | "available" | "missing";
type Publication = "all" | "published" | "unpublished";

const releaseFields = (release: OriginalRelease) =>
	release.credits?.some((credit) => credit.artistId) ?? false;

export function ArtistScreen({
	artistId,
	artists,
	releases,
	files,
	selected = new Set(),
	onSelect,
	onPreview,
	onDeleteArtist,
	onQueueDeletion,
}: {
	artistId: string;
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	files: SourceFiles;
	selected?: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onPreview?: (ids: string[]) => void;
	onDeleteArtist?: (artistId: string) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
}) {
	const artist = artists.find((item) => item.id === artistId);
	const [query, setQuery] = useState("");
	const [availability, setAvailability] = useState<Availability>("all");
	const [publication, setPublication] = useState<Publication>("all");
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
	const matchesAvailability = (track: OriginalTrack) =>
		availability === "all" ||
		(availability === "available" ? hasAudio(track) : !hasAudio(track));
	const matchesPublication = (track: OriginalTrack) =>
		publication === "all" ||
		(publication === "published" ? Boolean(track.published) : !track.published);
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
	const audioStatus = (release: OriginalRelease) => {
		const complete = providerComplete(release);
		const values = release.tracks.map(hasAudio);
		if (!complete) return "неизвестно";
		if (values.every(Boolean)) return "полностью";
		if (values.some(Boolean)) return "частично";
		return "нет";
	};
	const publicationStatus = (release: OriginalRelease) => {
		const complete = providerComplete(release);
		const values = release.tracks.map((track) => Boolean(track.published));
		if (!complete) return "неизвестно";
		if (values.every(Boolean)) return "полностью";
		if (values.some(Boolean)) return "частично";
		return "нет";
	};
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>{artist.name}</h1>
					<p>
						{artistReleases.length} изданий · {tracks.length} позиций ·{" "}
						{counts((track) => Boolean(track.published))} опубликовано ·{" "}
						{filteredMissingGroups.length} альбомов без издания
					</p>
				</div>
				<div className="demo-object-actions">
					<Checkbox
						aria-label={`Выбрать все позиции артиста ${artist.name}`}
						className="demo-checkbox"
						isSelected={ids.length > 0 && selectedCount === ids.length}
						isIndeterminate={selectedCount > 0 && selectedCount < ids.length}
						isDisabled={!ids.length || !onSelect}
						onChange={(value) => onSelect?.(ids, value)}
					>
						<span className="demo-checkbox-box" aria-hidden="true">
							{selectedCount === ids.length && ids.length
								? "✓"
								: selectedCount
									? "−"
									: ""}
						</span>
					</Checkbox>
					<button
						type="button"
						disabled={!selectedCount || !onPreview}
						onClick={() => onPreview?.(ids.filter((id) => selected.has(id)))}
					>
						Предпросмотр {selectedCount} выбранных позиций
					</button>
					{onQueueDeletion ? (
						<ArtistDeletionRequest
							artistId={artist.id}
							artists={artists}
							releases={releases}
							onQueue={onQueueDeletion}
						/>
					) : (
						<button
							type="button"
							disabled={!onDeleteArtist}
							onClick={() => onDeleteArtist?.(artist.id)}
						>
							Удалить артиста
						</button>
					)}
					<a href="#library">К медиатеке</a>
				</div>
			</header>
			<div className="demo-preview-counts">
				<span>
					Исходники:{" "}
					{counts(
						(track) =>
							(track.sourceIds?.length ?? Number(Boolean(track.sourceId))) > 0,
					)}
				</span>
				<span>Есть аудио: {counts(hasAudio)}</span>
				<span>Опубликовано: {counts((track) => Boolean(track.published))}</span>
				<span>
					Аудио по изданиям: полностью{" "}
					{
						artistReleases.filter(
							(release) => audioStatus(release) === "полностью",
						).length
					}{" "}
					· частично{" "}
					{
						artistReleases.filter(
							(release) => audioStatus(release) === "частично",
						).length
					}{" "}
					· нет{" "}
					{
						artistReleases.filter((release) => audioStatus(release) === "нет")
							.length
					}{" "}
					· неизвестно{" "}
					{
						artistReleases.filter(
							(release) => audioStatus(release) === "неизвестно",
						).length
					}
				</span>
				<span>
					Публикация по изданиям: полностью{" "}
					{
						artistReleases.filter(
							(release) => publicationStatus(release) === "полностью",
						).length
					}{" "}
					· частично{" "}
					{
						artistReleases.filter(
							(release) => publicationStatus(release) === "частично",
						).length
					}{" "}
					· нет{" "}
					{
						artistReleases.filter(
							(release) => publicationStatus(release) === "нет",
						).length
					}{" "}
					· неизвестно{" "}
					{
						artistReleases.filter(
							(release) => publicationStatus(release) === "неизвестно",
						).length
					}
				</span>
			</div>
			<div className="demo-object-actions" aria-label="Фильтры медиатеки">
				<label>
					Наличие
					<select
						aria-label="Наличие"
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
					Публикация
					<select
						aria-label="Публикация"
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
				<label>
					Поиск альбомов без издания
					<input
						type="search"
						aria-label="Поиск в медиатеке"
						placeholder="Артист, альбом, трек…"
						value={query}
						onChange={(event) => setQuery(event.currentTarget.value)}
					/>
				</label>
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
					<button
						type="button"
						onClick={() => onPreview?.(ids.filter((id) => selected.has(id)))}
						disabled={!onPreview}
					>
						Предпросмотр {selectedCount} выбранных
					</button>
					<button
						type="button"
						onClick={() => onSelect?.(ids, false)}
						disabled={!onSelect}
					>
						Снять выбор артиста
					</button>
				</div>
			)}
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
					<details className="demo-panel" key={release.id} open={index === 0}>
						<summary>
							<strong>{release.title}</strong> · {release.year} ·{" "}
							{release.tracks.length} позиций · {release.label || "Локальный"} ·{" "}
							{release.catalog}
							<span className="demo-secondary">
								{" "}
								·{" "}
								{complete && provider
									? `${pubCount}/${provider.tracks.length} опубликовано`
									: `${pubCount} опубликовано · полнота неизвестна`}
								{changes ? ` · Метаданные изменились у ${changes} позиций` : ""}
							</span>
						</summary>
						<div className="demo-object-actions">
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
									{release.tracks.every((track) => selected.has(track.id)) &&
									release.tracks.length
										? "✓"
										: release.tracks.some((track) => selected.has(track.id))
											? "−"
											: ""}
								</span>
							</Checkbox>
							<a href={`#release/${release.id}`}>Открыть альбом</a>
							<button
								type="button"
								disabled={!release.tracks.length || !onPreview}
								onClick={() =>
									onPreview?.(release.tracks.map((track) => track.id))
								}
							>
								Предпросмотр публикации
							</button>
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
										const matching =
											matchesAvailability(track) && matchesPublication(track);
										return (
											<tr
												key={track.id}
												className={
													availability === "all" && publication === "all"
														? ""
														: matching
															? "filter-match"
															: "filter-muted"
												}
											>
												<td>
													<input
														type="checkbox"
														aria-label={`Выбрать ${track.title}`}
														checked={selected.has(track.id)}
														disabled={!onSelect}
														onChange={(event) =>
															onSelect?.(
																[track.id],
																event.currentTarget.checked,
															)
														}
													/>
												</td>
												<td>
													{track.displayedNumber ||
														String(
															track.mediumPosition || track.position,
														).padStart(2, "0")}
												</td>
												<td>
													<a href={`#track/${track.id}`}>{track.title}</a>
													{availability !== "all" && (
														<span className="demo-secondary">
															{matchesAvailability(track)
																? "По фильтру наличия"
																: "Вне фильтра наличия"}
														</span>
													)}
												</td>
												<td>{hasAudio(track) ? "Да" : "Нет"}</td>
												<td>{sourceIds.length}</td>
												<td>
													{track.published ? (
														<>
															{track.published.container} ·{" "}
															{track.published.codec}
															{metadataChanged(release, track, artists) && (
																<span className="demo-secondary">
																	Метаданные изменились
																</span>
															)}
															<details>
																<summary>Путь и снимок публикации</summary>
																<p>{track.published.path}</p>
																<p>
																	{track.published.tags.TITLE.join(" · ")} ·{" "}
																	{track.published.readAt}
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
					</details>
				);
			})}
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
												<select
													aria-label={`Выберите издание группы ${group.title}`}
													value={absentEdition[group.id] ?? ""}
													onChange={(event) =>
														setAbsentEdition((value) => ({
															...value,
															[group.id]: event.currentTarget.value,
														}))
													}
												>
													<option value="">Выберите конкретное издание</option>
													{group.editions.map((edition) => (
														<option key={edition.id} value={edition.id}>
															{edition.year} · {edition.country} ·{" "}
															{edition.format} · {edition.titles.length} поз. ·{" "}
															{edition.disambiguation}
														</option>
													))}
												</select>
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
