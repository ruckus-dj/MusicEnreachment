import { useState } from "react";
import {
	desiredTags,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
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

export function ReleaseMetadataComparison({
	release,
	artists,
	files,
}: {
	release: OriginalRelease;
	artists?: CatalogArtist[];
	files: SourceFiles;
}) {
	const [trackId, setTrackId] = useState(release.tracks[0]?.id ?? "");
	const [scope, setScope] = useState<Scope>("release");
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>(
		undefined,
	);
	const track =
		release.tracks.find((item) => item.id === trackId) ?? release.tracks[0];
	if (!track) return null;
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
	const providerTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = fields[scope]
		.map((key) => {
			const values = [
				sourceTags[key] ?? [],
				providerTags?.[key] ?? null,
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
		<section className="demo-panel" aria-label="Сравнение метаданных">
			<h2>Сравнение метаданных</h2>
			{release.manual && (
				<p>
					Тип: {release.metadata?.type || "—"} · Исполнители:{" "}
					{release.credits
						?.map((credit) => credit.credit || credit.name)
						.filter(Boolean)
						.join(", ") || "не указаны"}
				</p>
			)}
			<div className="demo-object-actions">
				<label>
					Позиция
					<select
						aria-label="Позиция сравнения"
						value={track.id}
						onChange={(event) => {
							setTrackId(event.currentTarget.value);
							setFileOverride(undefined);
						}}
					>
						{release.tracks.map((item) => (
							<option key={item.id} value={item.id}>
								{item.title}
							</option>
						))}
					</select>
				</label>
				<label>
					Исходный файл
					<select
						aria-label="Исходный файл сравнения"
						value={fileId ?? ""}
						onChange={(event) =>
							setFileOverride(event.currentTarget.value || null)
						}
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
			</div>
			<div className="demo-object-actions" aria-label="Область тегов">
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
			<p className="demo-secondary">
				{file?.path ?? "Исходник не выбран"}
				<br />
				{track.published
					? `Публикация: ${track.published.path} · чтение ${track.published.readAt} (демо)`
					: "Публикация отсутствует; пустая колонка не подменяется текущими метаданными."}
			</p>
			<a href={`#tags/${track.id}/release`}>
				Открыть подробное сравнение альбома
			</a>
		</section>
	);
}
