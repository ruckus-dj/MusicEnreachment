import { useState } from "react";
import {
	desiredTags,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";

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
} as const;
type Scope = keyof typeof fields;

export function TagsScreen({
	trackId,
	scope: initialScope = "track",
	releases,
	artists,
	files,
	onSelectFile,
	onPreview,
}: {
	trackId: string;
	scope?: Scope;
	releases: OriginalRelease[];
	artists: CatalogArtist[];
	files: SourceFiles;
	onSelectFile?: (trackId: string, fileId: string) => void;
	onPreview?: (ids: string[]) => void;
}) {
	const initial = releases
		.flatMap((release) => release.tracks.map((track) => ({ release, track })))
		.find(({ track }) => track.id === trackId);
	const [currentTrackId, setCurrentTrackId] = useState(trackId);
	const [scope, setScope] = useState<Scope>(initialScope);
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>(
		undefined,
	);
	const selected =
		releases
			.flatMap((release) => release.tracks.map((track) => ({ release, track })))
			.find(({ track }) => track.id === currentTrackId) ?? initial;
	if (!selected)
		return (
			<section>
				<h1>Теги не найдены</h1>
				<p>Позиция отсутствует в медиатеке.</p>
				<a href="#library">К медиатеке</a>
			</section>
		);
	const { release, track } = selected;
	const linkedFileIds = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const fileId =
		fileOverride === undefined
			? track.sourceId
			: fileOverride && linkedFileIds.includes(fileOverride)
				? fileOverride
				: null;
	const file = fileId ? files[fileId] : undefined;
	const provider = originalProviders
		.flatMap((item) =>
			item.tracks.map((candidate) => ({ release: item, track: candidate })),
		)
		.find(({ track: candidate }) => candidate.id === track.providerTrackId);
	const sourceTags = file?.tags ?? {};
	const remoteTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = fields[scope]
		.map((key) => {
			const values = [
				sourceTags[key] ?? [],
				remoteTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			const distinct = new Set(
				values.map((value) => JSON.stringify(value ?? [])),
			);
			return { key, values, different: distinct.size > 1 };
		})
		.filter((row) => !differencesOnly || row.different);
	const scopes: [Scope, string][] = [
		["artist", "Артист"],
		["release", "Альбом"],
		["track", "Трек"],
	];
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>Теги: {track.title}</h1>
					<p>
						Артист и альбом в опубликованной колонке — теги выбранного
						аудиофайла. Несовпадения не скрываются.
					</p>
				</div>
				<a href={`#release/${release.id}`}>К альбому</a>
			</header>
			<section className="demo-panel">
				<h2>Контекст сравнения</h2>
				<div className="demo-object-actions">
					<label>
						Файл / позиция
						<select
							aria-label="Файл / позиция"
							value={track.id}
							onChange={(event) => {
								setCurrentTrackId(event.currentTarget.value);
								setFileOverride(undefined);
							}}
						>
							{release.tracks.map((item) => (
								<option key={item.id} value={item.id}>
									{String(item.position).padStart(2, "0")} · {item.title} ·{" "}
									{item.published ? "опубликован" : "без публикации"}
								</option>
							))}
						</select>
					</label>
					<label>
						Исходный файл для сравнения
						<select
							aria-label="Исходный файл для сравнения"
							value={fileId ?? ""}
							onChange={(event) => {
								setFileOverride(event.currentTarget.value || null);
								if (event.currentTarget.value)
									onSelectFile?.(track.id, event.currentTarget.value);
							}}
						>
							<option value="">Исходник не выбран</option>
							{linkedFileIds.map(
								(id) =>
									files[id] && (
										<option key={id} value={id}>
											{files[id].name}
										</option>
									),
							)}
						</select>
					</label>
					<span className="demo-secondary">
						Кэш провайдера: 26.09.2026 10:20 · идентификаторы демонстрационные
					</span>
				</div>
				<div className="demo-object-actions">
					{scopes.map(([id, label]) => (
						<button
							key={id}
							type="button"
							aria-pressed={scope === id}
							onClick={() => setScope(id)}
						>
							{label}
						</button>
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
					{onPreview && (
						<button type="button" onClick={() => onPreview([track.id])}>
							Предпросмотр публикации
						</button>
					)}
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
								].map((heading) => (
									<th scope="col" key={heading}>
										{heading}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{rows.map(({ key, values, different }) => (
								<tr key={key}>
									<th scope="row" className={different ? "difference" : ""}>
										{key}
									</th>
									{values.map((value, index) => (
										<td key={`${key}:${index}`}>
											{value === null ? (
												<span className="demo-secondary">
													{index === 1 ? "Нет связи" : "Не опубликован"}
												</span>
											) : value.length ? (
												value.map((item, itemIndex) => (
													<span key={`${itemIndex}:${item}`}>
														{itemIndex > 0 && <br />}
														{item}
													</span>
												))
											) : (
												"—"
											)}
										</td>
									))}
								</tr>
							))}
						</tbody>
					</table>
				</div>
				<div className="demo-secondary">
					{file?.path ?? "Исходник не выбран"}
					<br />
					{track.published
						? `Публикация: ${track.published.path} · чтение ${track.published.readAt} (демо)`
						: "Публикация отсутствует; пустая колонка не подменяется текущими метаданными."}
				</div>
			</section>
			<p className="demo-migration-note">
				В колонке публикации показан отдельный снимок чтения существующего
				файла. Изменение локальных тегов не изменяет эту колонку до явной
				публикации.
			</p>
		</section>
	);
}

export type TagsScope = Scope;
export type TagsTrack = OriginalTrack;
