import { DeletionRequest, type DeletionOperation } from "./DeletionRequest";
import {
	metadataChanged,
	originalArtistName,
	originalProviders,
	originalFiles,
	trackNumberLabel,
	type OriginalRelease,
	type CatalogArtist,
	type SourceFiles,
} from "./catalogData";
import { ReleaseMetadataComparison } from "./ReleaseMetadataComparison";
import { Button, Checkbox } from "./ui";
import { availabilityRowClass, type CatalogFilter } from "./availabilityRows";

export function ReleaseScreen({
	release,
	artists,
	files = originalFiles,
	filter = "all",
	selected,
	onSelect,
	onPreview,
	onQueueDeletion,
}: {
	release: OriginalRelease;
	artists?: CatalogArtist[];
	files?: SourceFiles;
	filter?: CatalogFilter;
	selected: Set<string>;
	onSelect: (ids: string[], value: boolean) => void;
	onPreview: (ids: string[]) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
}) {
	const ids = release.tracks.map((track) => track.id);
	const hasAudio = (track: OriginalRelease["tracks"][number]) =>
		Boolean(track.published) ||
		[
			...new Set([
				...(track.sourceIds ?? []),
				...(track.sourceId ? [track.sourceId] : []),
			]),
		].some((id) => files[id]?.available && files[id]?.locations?.length);
	const published = release.tracks.filter((track) => track.published).length;
	const provider = originalProviders.find(
		(item) => item.id === release.providerId,
	);
	const known =
		provider?.tracks.length && provider.tracks.length === release.tracks.length;
	const changed = release.tracks.filter((track) =>
		metadataChanged(release, track, artists),
	).length;
	const sourceCount = release.tracks.reduce(
		(sum, track) =>
			sum + (track.sourceIds?.length ?? Number(Boolean(track.sourceId))),
		0,
	);
	const totalDuration = release.tracks.every((track) =>
		Number.isFinite(track.duration),
	)
		? release.tracks.reduce((sum, track) => sum + (track.duration ?? 0), 0)
		: null;
	const duration = (seconds: number) =>
		`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
	return (
		<section className="catalogue-object">
			<header className="demo-object-header">
				<div>
					<h1>{release.title || "Локальный релиз без названия"}</h1>
					<p>
						{originalArtistName(release.artistId, artists)} ·{" "}
						{release.year || "Дата не указана"} ·{" "}
						{release.country || "Страна не указана"} ·{" "}
						{release.label || "Локальный"} · {release.catalog || "—"}
					</p>
				</div>
				<a href="#library">К медиатеке</a>
			</header>
			<div className="demo-preview-counts">
				<span>
					{release.providerId
						? `MusicBrainz · ${release.providerId}`
						: "Локальный релиз"}
				</span>
				<span>{ids.length} позиций</span>
				<span>{sourceCount} связей с исходниками</span>
				<span>
					{known
						? `${published}/${provider.tracks.length} · ${published === provider.tracks.length ? "Полностью опубликовано" : published ? "Частично опубликовано" : "Не опубликовано"}`
						: `${published} опубликовано · полнота неизвестна`}
				</span>
				{changed > 0 && <span>Метаданные изменились у {changed} позиций</span>}
			</div>
			<div className="demo-object-actions">
				<Checkbox
					aria-label={`Выбрать все позиции релиза ${release.title}`}
					className="demo-checkbox"
					isSelected={ids.length > 0 && ids.every((id) => selected.has(id))}
					isIndeterminate={
						ids.some((id) => selected.has(id)) &&
						!ids.every((id) => selected.has(id))
					}
					isDisabled={!ids.length}
					onChange={(value) => onSelect(ids, value)}
				>
					<span className="demo-checkbox-box" aria-hidden="true">
						{ids.length > 0 && ids.every((id) => selected.has(id))
							? "✓"
							: ids.some((id) => selected.has(id))
								? "−"
								: ""}
					</span>
					<span className="sr-only">Выбрать все позиции релиза</span>
				</Checkbox>
				<Button isDisabled={!ids.length} onPress={() => onPreview(ids)}>
					Предпросмотр публикации
				</Button>
			</div>
			<section className="demo-panel">
				<h2>
					Список треков ·{" "}
					{release.manual
						? `${release.media?.length ?? 0} носителя`
						: "конкретное издание"}
				</h2>
				{totalDuration !== null && <p>{duration(totalDuration)} всего</p>}
				<div className="demo-preview-table">
					<table>
						<thead>
							<tr>
								{[
									"Выбрать",
									"№",
									"Композиция",
									"Длительность",
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
							{release.tracks.map((track) => (
								<tr
									key={track.id}
									className={availabilityRowClass(track, filter, hasAudio)}
								>
									<td>
										<Checkbox
											className="demo-checkbox"
											aria-label={`Выбрать ${track.title}`}
											isSelected={selected.has(track.id)}
											onChange={(value) => onSelect([track.id], value)}
										>
											<span className="demo-checkbox-box" aria-hidden="true">
												{selected.has(track.id) ? "✓" : ""}
											</span>
										</Checkbox>
									</td>
									<td>{trackNumberLabel(release, track)}</td>
									<td>
										<a href={`#track/${track.id}`}>{track.title}</a>
									</td>
									<td>
										{track.duration === null ? "—" : duration(track.duration)}
									</td>
									<td>
										{track.sourceIds?.length || Number(Boolean(track.sourceId))}
									</td>
									<td>
										{track.published ? (
											<>
												<span>
													{track.published.container} · {track.published.codec}
												</span>
												{metadataChanged(release, track, artists) && (
													<span className="demo-secondary">
														Метаданные изменились
													</span>
												)}
												{track.observedFile && (
													<span className="demo-secondary">
														Файл изменён вне приложения
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
							))}
						</tbody>
					</table>
				</div>
				{!ids.length && <p>Позиции не добавлены.</p>}
			</section>
			{onQueueDeletion && (
				<DeletionRequest release={release} onQueue={onQueueDeletion} />
			)}
			<ReleaseMetadataComparison
				release={release}
				artists={artists}
				files={files}
			/>
		</section>
	);
}
