import { useState } from "react";
import { Button, Radio, RadioGroup } from "react-aria-components";
import {
	desiredTags,
	metadataChanged,
	originalArtistName,
	originalFiles,
	originalProviders,
	type OriginalRelease,
	type OriginalTrack,
} from "./catalogData";

const duration = (seconds: number) =>
	`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
const fields = {
	artist: ["ARTIST", "ARTISTSORT", "MUSICBRAINZ_ARTISTID"],
	release: [
		"ALBUM",
		"ALBUMARTIST",
		"DATE",
		"COUNTRY",
		"LABEL",
		"CATALOGNUMBER",
		"MUSICBRAINZ_ALBUMID",
	],
	track: [
		"TITLE",
		"ARTIST",
		"TRACKNUMBER",
		"DISCNUMBER",
		"GENRE",
		"MUSICBRAINZ_TRACKID",
		"COMMENT",
	],
};

export function TrackScreen({
	release,
	track,
	onSource,
	onPreview,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onSource: (id: string) => void;
	onPreview: () => void;
}) {
	const [scope, setScope] = useState<keyof typeof fields>("track");
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const sourceIds = track.sourceIds ?? (track.sourceId ? [track.sourceId] : []);
	const file = track.sourceId ? originalFiles[track.sourceId] : undefined;
	const provider = originalProviders.find((item) =>
		item.tracks.some((candidate) => candidate.id === track.providerTrackId),
	);
	const providerTrack = provider?.tracks.find(
		(candidate) => candidate.id === track.providerTrackId,
	);
	const remote =
		provider && providerTrack ? desiredTags(provider, providerTrack) : {};
	const local = desiredTags(release, track);
	const changed = metadataChanged(release, track);
	const rows = fields[scope]
		.map((key) => ({
			key,
			values: [
				file?.tags[key] ?? [],
				remote[key] ?? [],
				local[key] ?? [],
				track.published?.tags[key] ?? [],
			],
		}))
		.filter(
			(row) =>
				!differencesOnly ||
				new Set(row.values.map((value) => JSON.stringify(value))).size > 1,
		);
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>
						{String(track.position).padStart(2, "0")} · {track.title}
					</h1>
					<p>
						{originalArtistName(track.artistId)} / {release.title} · позиция
						конкретного издания
					</p>
				</div>
				<a href="#library">К медиатеке</a>
			</header>
			<div className="demo-object-columns">
				<section className="demo-panel">
					<h2>Связанные аудиоварианты</h2>
					{sourceIds.length ? (
						<RadioGroup
							aria-label="Исходник для публикации"
							value={track.sourceId ?? ""}
							onChange={onSource}
						>
							{sourceIds.map((id) => {
								const source = originalFiles[id];
								return source ? (
									<Radio key={id} value={id} className="demo-source">
										<span aria-hidden="true" className="demo-radio-dot" />
										<span>
											<strong>{source.name}</strong>
											<span className="demo-secondary">
												{source.codec} · {duration(source.duration)} ·{" "}
												{source.size}
											</span>
										</span>
									</Radio>
								) : null;
							})}
						</RadioGroup>
					) : (
						<p>Связанных вариантов нет.</p>
					)}
					<p>
						Переключатель явно выбирает один исходник для публикации. Текущая
						публикация не меняется автоматически.
					</p>
				</section>
				<section className="demo-panel">
					<h2>Состояние позиции</h2>
					<dl className="demo-facts">
						<dt>Формат исходника</dt>
						<dd>
							{file
								? `${file.codec} · ${duration(file.duration)} · ${file.size}`
								: "Не выбран"}
						</dd>
						<dt>Композиция</dt>
						<dd>
							{track.recordingTitle ||
								providerTrack?.title ||
								(track.recordingId
									? "Связанная композиция"
									: "Без связи с провайдером")}
						</dd>
						<dt>Текущая публикация</dt>
						<dd>
							{track.published
								? `${track.published.container} · ${track.published.codec} · ${track.published.readAt}`
								: "Не опубликована"}
						</dd>
						<dt>Метаданные</dt>
						<dd>
							{changed ? "Изменились" : "Совпадают со снимком публикации"}
						</dd>
						<dt>Файл на диске</dt>
						<dd>
							{track.observedFile
								? "Файл изменён вне приложения"
								: "Изменения не отмечены"}
						</dd>
					</dl>
					<details>
						<summary>Пути и технические сведения</summary>
						<dl className="demo-facts">
							<dt>Путь исходника</dt>
							<dd>{file?.path ?? "Не выбран"}</dd>
							<dt>Идентификатор композиции</dt>
							<dd>{track.recordingId ?? "Нет связи"}</dd>
							<dt>Подсказка провайдера</dt>
							<dd>{track.providerTitle || providerTrack?.title || "Нет"}</dd>
							<dt>Путь и снимок публикации</dt>
							<dd>
								{track.published
									? `${track.published.path} · ${track.published.tags.TITLE.join(" · ")} · ${track.published.readAt}`
									: "Публикации нет"}
							</dd>
						</dl>
					</details>
					<p>
						Локальные изменения сравниваются с последним успешным состоянием
						публикации; версия кэша и наблюдение за файлом не подменяют снимок
						публикации.
					</p>
					<h3>Публикация</h3>
					<div className="demo-object-actions">
						{track.published && (
							<Button onPress={onPreview}>Обновить метаданные</Button>
						)}
						<Button onPress={onPreview}>Предпросмотр публикации</Button>
					</div>
					<p className="demo-migration-note">
						Редактирование, обновление кэша, улучшение аудио, явная
						перепубликация и удаление ещё не перенесены; эти действия остаются в
						исходном прототипе.
					</p>
				</section>
			</div>
			<section className="demo-panel">
				<h2>Сравнение тегов</h2>
				<div className="demo-object-actions">
					{(
						[
							["artist", "Артист"],
							["release", "Альбом"],
							["track", "Трек"],
						] as const
					).map(([id, label]) => (
						<Button
							key={id}
							aria-pressed={scope === id}
							onPress={() => setScope(id)}
						>
							{label}
						</Button>
					))}
					<label>
						<input
							type="checkbox"
							checked={differencesOnly}
							onChange={(event) =>
								setDifferencesOnly(event.currentTarget.checked)
							}
						/>{" "}
						Только отличия
					</label>
				</div>
				<div className="demo-preview-table">
					<table>
						<thead>
							<tr>
								{[
									"Поле",
									"Исходный файл",
									"Провайдер · кэш",
									"Локальная медиатека",
									"Сейчас в публикации",
								].map((label) => (
									<th key={label} scope="col">
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{rows.map((row) => (
								<tr key={row.key}>
									<th scope="row">{row.key}</th>
									{row.values.map((value, index) => (
										<td key={`${row.key}:${index}`}>
											{value.join(" · ") || "—"}
										</td>
									))}
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</section>
		</section>
	);
}
