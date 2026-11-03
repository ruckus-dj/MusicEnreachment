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
import { TrackMetadataEditors } from "./metadata/TrackMetadataEditors";
import { navidromeFields } from "./metadata/registry";
import {
	catalogData,
	desiredTags,
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
	onRecordingChange,
	genresLoaded = true,
	onLoadGenres,
	recordingImpact,
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
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
	recordingImpact?: string[];
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
					...navidromeFields
						.filter(
							({ tag }) =>
								normalizeTagValues(local[tag] ?? []) !==
								normalizeTagValues(track.metadataBaseline?.[tag] ?? []),
						)
						.map((field) => field.tag),
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
	const changed = Boolean(
		track.published && track.metadataBaseline && changes.length,
	);
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
			{(onMutate || onRecordingChange) && (
				<TrackMetadataEditors
					track={track}
					artists={artists ?? catalogData.artists}
					files={files}
					genresLoaded={genresLoaded}
					onLoadGenres={onLoadGenres}
					onMutate={onMutate ?? (() => undefined)}
					onRecordingChange={onRecordingChange}
					recordingImpact={recordingImpact}
				/>
			)}
			<div className={catalogueStyles.objectColumns}>
				<section className={catalogueStyles.panel}>
					<details className={catalogueStyles.trackDetails}>
						<summary>
							Аудио и публикация ·{" "}
							{file
								? `${file.codec} · ${duration(file.duration)}`
								: "исходник не выбран"}{" "}
							·{" "}
							{track.published
								? `${track.published.container} · ${track.published.codec}`
								: "не опубликована"}
						</summary>
						<h2>Исходник для публикации</h2>
						{sourceIds.length ? (
							<RadioGroup
								aria-label="Исходник для публикации"
								value={track.sourceId ?? ""}
								onChange={onSource}
							>
								{sourceIds.map((id) => {
									const source = files[id];
									return source ? (
										<Radio
											key={id}
											value={id}
											className={catalogueStyles.source}
										>
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
					</details>
				</section>
				<section className={catalogueStyles.panel}>
					<h2>Публикация</h2>
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
					<details className={catalogueStyles.trackDetails}>
						<summary>Состояние и технические сведения</summary>
						<dl className={catalogueStyles.facts}>
							<dt>Композиция</dt>
							<dd>
								{track.recordingTitle ||
									providerTrack?.title ||
									(track.recordingId
										? "Связанная композиция"
										: "Без связи с провайдером")}
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
							<dt>Формат исходника</dt>
							<dd>
								{file
									? `${file.codec} · ${duration(file.duration)} · ${file.size}`
									: "Не выбран"}
							</dd>
							<dt>Формат публикации</dt>
							<dd>
								{track.published
									? `${track.published.container} · ${track.published.codec}`
									: "Не опубликована"}
							</dd>
						</dl>
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
							публикации.
						</p>
					</details>
				</section>
			</div>
			<details className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
				<summary className="cursor-pointer font-semibold">
					Сравнить теги файла и публикации
				</summary>
				<TagsComparison
					releases={[release]}
					artists={artists}
					files={files}
					scope="track"
					initialTrackId={track.id}
					onPreview={onMetadataPreview}
					allowScopeChange
				/>
			</details>
		</section>
	);
}
