import { Checkbox } from "react-aria-components";
import { DeletionRequest, type DeletionOperation } from "./DeletionRequest";
import {
	metadataChanged,
	originalArtistName,
	originalProviders,
	type OriginalRelease,
} from "./catalogData";

export function ReleaseScreen({
	release,
	selected,
	onSelect,
	onPreview,
	onQueueDeletion,
}: {
	release: OriginalRelease;
	selected: Set<string>;
	onSelect: (ids: string[], value: boolean) => void;
	onPreview: (ids: string[]) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
}) {
	const ids = release.tracks.map((track) => track.id);
	const count = ids.filter((id) => selected.has(id)).length;
	const published = release.tracks.filter((track) => track.published).length;
	const provider = originalProviders.find(
		(item) => item.id === release.providerId,
	);
	const known =
		provider?.tracks.length && provider.tracks.length === release.tracks.length;
	const changed = release.tracks.filter((track) =>
		metadataChanged(release, track),
	).length;
	const sourceCount = release.tracks.reduce(
		(sum, track) =>
			sum + (track.sourceIds?.length ?? Number(Boolean(track.sourceId))),
		0,
	);
	const totalDuration = release.tracks.every((track) =>
		Number.isFinite(track.duration),
	)
		? release.tracks.reduce((sum, track) => sum + track.duration, 0)
		: null;
	const duration = (seconds: number) =>
		`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>{release.title || "Локальный релиз без названия"}</h1>
					<p>
						{originalArtistName(release.artistId)} ·{" "}
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
					aria-label={`Выбрать позиции альбома ${release.title}`}
					className="demo-checkbox"
					isSelected={ids.length > 0 && count === ids.length}
					isIndeterminate={count > 0 && count < ids.length}
					isDisabled={ids.length === 0}
					onChange={(value) => onSelect(ids, value)}
				>
					<span className="demo-checkbox-box" aria-hidden="true">
						{count === ids.length && ids.length ? "✓" : count ? "−" : ""}
					</span>
				</Checkbox>
				<button
					type="button"
					disabled={!ids.length}
					onClick={() => onPreview(ids)}
				>
					Предпросмотр публикации
				</button>
			</div>
			<section className="demo-panel">
				<h2>Список треков · конкретное издание</h2>
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
								<tr key={track.id}>
									<td>
										<label className="demo-checkbox">
											<input
												type="checkbox"
												aria-label={`Выбрать ${track.title}`}
												checked={selected.has(track.id)}
												onChange={(event) =>
													onSelect([track.id], event.currentTarget.checked)
												}
											/>
										</label>
									</td>
									<td>
										{track.displayedNumber ||
											String(track.mediumPosition || track.position).padStart(
												2,
												"0",
											)}
									</td>
									<td>
										<a href={`#track/${track.id}`}>{track.title}</a>
									</td>
									<td>{duration(track.duration)}</td>
									<td>
										{track.sourceIds?.length || Number(Boolean(track.sourceId))}
									</td>
									<td>
										{track.published ? (
											<>
												<span>
													{track.published.container} · {track.published.codec}
												</span>
												{metadataChanged(release, track) && (
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
			<p className="demo-migration-note">
				Ручной черновик и сравнение метаданных на уровне издания ещё не
				перенесены.
			</p>
		</section>
	);
}
