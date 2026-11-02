import {
	Button,
	Disclosure,
	DisclosurePanel,
	Heading,
	Radio,
	RadioGroup,
} from "./ui";
import { TrackActions } from "./TrackActions";
import { TagsComparison } from "./TagsComparison";
import { artistReleaseHref } from "./artistReleaseRoute";
import type { DeletionOperation } from "./DeletionRequest";
import { catalogueStyles } from "./catalogueStyles";
import {
	catalogData,
	desiredTags,
	metadataChanged,
	originalArtistName,
	originalFiles,
	originalProviders,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
	type CatalogArtist,
} from "./catalogData";

const duration = (seconds: number) =>
	`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
export function TrackScreen({
	release,
	track,
	onSource,
	onPreview,
	onMetadataPreview = onPreview,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
	artists,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onSource: (id: string) => void;
	onPreview: () => void;
	onMetadataPreview?: (ids: string[]) => void;
	onMutate?: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	artists?: CatalogArtist[];
}) {
	const sourceIds = track.sourceIds ?? (track.sourceId ? [track.sourceId] : []);
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const provider = originalProviders.find((item) =>
		item.tracks.some((candidate) => candidate.id === track.providerTrackId),
	);
	const providerTrack = provider?.tracks.find(
		(candidate) => candidate.id === track.providerTrackId,
	);
	const changed = metadataChanged(release, track, artists);
	const local = desiredTags(release, track, artists);
	const normalizeTagValues = (values: string[]) =>
		values
			.join("\0")
			.normalize("NFKC")
			.toLocaleLowerCase()
			.replace(/\s+/g, " ")
			.trim();
	const changes =
		track.published && track.metadataBaseline
			? [
					...[
						"TITLE",
						"ARTIST",
						"ALBUM",
						"ALBUMARTIST",
						"DATE",
						"COUNTRY",
						"LABEL",
						"CATALOGNUMBER",
						"TRACKNUMBER",
						"DISCNUMBER",
						"GENRE",
					].filter(
						(key) =>
							normalizeTagValues(local[key] ?? []) !==
							normalizeTagValues(track.metadataBaseline?.[key] ?? []),
					),
					...((track.coverHash || null) !==
					(track.metadataBaselineExtras?.coverHash || null)
						? ["Встроенная обложка"]
						: []),
					...((track.lyrics || null) !==
					(track.metadataBaselineExtras?.lyrics || null)
						? ["Текст песни"]
						: []),
				]
			: [];
	return (
		<section className={catalogueStyles.object}>
			<header className={catalogueStyles.objectHeader}>
				<div>
					<h1>
						{String(track.position).padStart(2, "0")} · {track.title}
					</h1>
					<p>
						{originalArtistName(track.artistId, artists)} / {release.title} ·
						позиция конкретного издания
					</p>
				</div>
				{artistReleaseHref(release, artists ?? catalogData.artists) ? (
					<a
						href={
							artistReleaseHref(release, artists ?? catalogData.artists) ??
							undefined
						}
					>
						К артисту и изданию
					</a>
				) : (
					<a href="#library">К медиатеке</a>
				)}
			</header>
			<div className={catalogueStyles.objectColumns}>
				<section className={catalogueStyles.panel}>
					<h2>Связанные аудиоварианты</h2>
					{sourceIds.length ? (
						<RadioGroup
							aria-label="Исходник для публикации"
							value={track.sourceId ?? ""}
							onChange={onSource}
						>
							{sourceIds.map((id) => {
								const source = files[id];
								return source ? (
									<Radio key={id} value={id} className={catalogueStyles.source}>
										<span
											aria-hidden="true"
											className={catalogueStyles.radioDot}
										/>
										<span>
											<strong>{source.name}</strong>
											<span className={catalogueStyles.secondaryInline}>
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
				<section className={catalogueStyles.panel}>
					<h2>Состояние позиции</h2>
					<dl className={catalogueStyles.facts}>
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
						<dt>Формат публикации</dt>
						<dd>
							{track.published
								? `${track.published.container} · ${track.published.codec}`
								: "Не опубликована"}
						</dd>
						<dt>Метаданные</dt>
						<dd>
							{changed
								? `Изменились: ${changes.join(", ")}`
								: "Совпадают со снимком публикации"}
						</dd>
						<dt>Файл на диске</dt>
						<dd>
							{track.observedFile
								? "Файл изменён вне приложения"
								: "Изменения не отмечены"}
						</dd>
					</dl>
					<details className={catalogueStyles.trackDetails}>
						<summary>Пути и технические сведения</summary>
						<dl className={catalogueStyles.facts}>
							<dt>Путь исходника</dt>
							<dd>{file?.path ?? "Не выбран"}</dd>
							<dt>Идентификатор композиции</dt>
							<dd>{track.recordingId ?? "Нет связи"}</dd>
							<dt>Подсказка провайдера</dt>
							<dd>{track.providerTitle || providerTrack?.title || "Нет"}</dd>
							{track.observedFile?.mtime && (
								<>
									<dt>Внешнее изменение файла · время наблюдения</dt>
									<dd>{track.observedFile.mtime}</dd>
								</>
							)}
						</dl>
					</details>
					{track.published && (
						<Disclosure className={catalogueStyles.trackPublication}>
							<Heading>
								<Button slot="trigger">Текущая публикация</Button>
							</Heading>
							<DisclosurePanel>
								<dl className={catalogueStyles.facts}>
									<dt>Путь</dt>
									<dd>{track.published.path}</dd>
									<dt>Снимок тегов</dt>
									<dd>
										{Object.entries(track.published.tags)
											.map(([key, values]) => `${key}: ${values.join(" · ")}`)
											.join("; ")}
									</dd>
									<dt>Прочитано</dt>
									<dd>{track.published.readAt}</dd>
								</dl>
							</DisclosurePanel>
						</Disclosure>
					)}
					<p>
						Локальные изменения сравниваются с последним успешным состоянием
						публикации; версия кэша и наблюдение за файлом не подменяют снимок
						публикации.
					</p>
					<h3>Публикация</h3>
					<div className={catalogueStyles.actions}>
						{track.published && (
							<Button onPress={() => onMetadataPreview([track.id])}>
								Обновить метаданные
							</Button>
						)}
						<Button onPress={onPreview}>Предпросмотр публикации</Button>
					</div>
					{onMutate && (
						<TrackActions
							release={release}
							track={track}
							onMutate={onMutate}
							files={files}
							artists={artists}
							onQueueDeletion={onQueueDeletion}
						/>
					)}
				</section>
			</div>
			<TagsComparison
				releases={[release]}
				artists={artists}
				files={files}
				scope="track"
				initialTrackId={track.id}
				onPreview={onMetadataPreview}
				allowScopeChange
			/>
		</section>
	);
}
