import { MetadataEditor } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";
import type { CatalogArtist, OriginalTrack, SourceFiles } from "../catalogData";
import { originalProviders } from "../catalogData";

type TrackWithExtras = OriginalTrack & { disambiguation?: string };

function mergedValues(
	...values: (MetadataValues | undefined)[]
): MetadataValues {
	return Object.assign({}, ...values.filter(Boolean));
}

function recordingTitleBaseline(track: OriginalTrack): string | undefined {
	return (
		track.recordingTitle ??
		track.recordingMetadata?.title?.[0] ??
		(track.recordingId
			? originalProviders
					.flatMap((release) => release.tracks)
					.find((candidate) => candidate.recordingId === track.recordingId)
					?.title
			: undefined)
	);
}

function recordingValues(track: OriginalTrack): MetadataValues {
	const values = { ...(track.recordingMetadata ?? {}) };
	if (!("title" in values))
		values.title = [recordingTitleBaseline(track) ?? track.title];
	if (!("genre" in values)) values.genre = [...(track.recordingGenres ?? [])];
	return values;
}

function creditsFor(
	track: OriginalTrack,
	values: MetadataValues,
	artists: CatalogArtist[],
): MetadataCredit[] {
	if (track.recordingCredits) return track.recordingCredits;
	const names = values.artist ?? values.artists ?? [];
	if (!names.length && track.artistId) {
		const artist = artists.find((candidate) => candidate.id === track.artistId);
		if (artist)
			return [
				{
					artistId: artist.id,
					creditName: artist.name,
					joinPhrase: "",
				},
			];
	}
	return names.map((creditName, index) => ({
		artistId: "",
		mbid: values.musicbrainz_artistid?.[index] || undefined,
		creditName,
		joinPhrase: "",
	}));
}

/** Editors for recording-owned, release-position-owned, and per-file audio tags. */
export function TrackMetadataEditors({
	track,
	artists,
	files,
	genresLoaded,
	onLoadGenres,
	onMutate,
	onRecordingChange,
	recordingImpact = [],
}: {
	track: OriginalTrack;
	artists: CatalogArtist[];
	files: SourceFiles;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
	recordingImpact?: string[];
}) {
	const recording = recordingValues(track);
	const position: MetadataValues = {
		track: [
			track.displayedNumber ?? String(track.mediumPosition ?? track.position),
		],
		disc: [String(track.medium ?? 1)],
		...track.positionMetadata,
	};
	const audioSources = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const extended = track as TrackWithExtras;
	const recordingExtras = [
		{
			key: "disambiguation",
			label: "Уточнение композиции",
			value: extended.disambiguation ?? "",
		},
	];
	const titleBaseline = recordingTitleBaseline(track);
	const positionExtras = [
		{
			key: "titleOverride",
			label: "Название позиции (переопределение)",
			value:
				track.positionTitleOverride ??
				(titleBaseline !== undefined && track.title !== titleBaseline
					? track.title
					: ""),
		},
	];

	return (
		<div
			className="grid min-w-0 gap-3"
			aria-label={`Метаданные позиции ${track.id}`}
		>
			<MetadataEditor
				label="Сведения о композиции"
				scope="recording"
				values={recording}
				artists={artists.map(({ id, name, sortName }) => ({
					id,
					name,
					sortName,
				}))}
				credits={creditsFor(track, recording, artists)}
				genres={
					(track.recordingGenreRefs ??
						track.recordingGenres?.map((name) => ({ name })) ??
						[]) as MetadataGenre[]
				}
				genresLoaded={genresLoaded}
				onLoadGenres={onLoadGenres}
				impact={recordingImpact}
				extraFields={recordingExtras}
				requiredKeys={["title"]}
				onSave={(values, details) => {
					const title = values.title?.[0] ?? "";
					const titleChanged = title !== recording.title?.[0];
					const update = (current: OriginalTrack): OriginalTrack => {
						const explicitPositionTitle =
							current.positionTitleOverride ||
							current.positionMetadata?.title?.some(Boolean);
						const baseline = recordingTitleBaseline(current);
						const updatePositionTitle =
							titleChanged &&
							!explicitPositionTitle &&
							baseline !== undefined &&
							current.title === baseline;
						return {
							...current,
							recordingTitle: title,
							...(updatePositionTitle ? { title } : {}),
							recordingMetadata: values,
							...(details?.credits
								? { recordingCredits: details.credits }
								: {}),
							...(details?.genres
								? {
										recordingGenreRefs: details.genres as MetadataGenre[],
										recordingGenres: details.genres
											.map((genre) => genre.name ?? genre.id ?? "")
											.filter(Boolean),
									}
								: {}),
							...(details?.extraValues?.disambiguation !== undefined
								? { disambiguation: details.extraValues.disambiguation }
								: {}),
						};
					};
					(onRecordingChange ?? onMutate)(update);
				}}
			/>
			<MetadataEditor
				label="Позиция в издании"
				scope="track"
				values={position}
				extraFields={positionExtras}
				requiredKeys={["track"]}
				onSave={(values, details) => {
					const displayedNumber = values.track?.[0] ?? "";
					const medium = Number(values.disc?.[0] ?? "1");
					onMutate((current) => ({
						...current,
						displayedNumber,
						mediumPosition:
							Number(displayedNumber) ||
							current.mediumPosition ||
							current.position,
						medium:
							Number.isFinite(medium) && medium > 0 ? medium : current.medium,
						positionMetadata: values,
						...(details?.extraValues?.titleOverride !== undefined
							? {
									positionTitleOverride: details.extraValues.titleOverride,
									title:
										details.extraValues.titleOverride ||
										recordingTitleBaseline(current) ||
										current.title,
								}
							: {}),
					}));
				}}
			/>
			<section
				className="grid min-w-0 gap-3"
				aria-label="Метаданные аудио для публикации"
			>
				<h2 className="m-0 text-sm font-semibold">
					Метаданные аудио для публикации
				</h2>
				{audioSources.length ? (
					audioSources.map((sourceId) => {
						const source = files[sourceId];
						const isSelected = sourceId === track.sourceId;
						return (
							<MetadataEditor
								key={sourceId}
								label={`${source?.name ?? sourceId}${isSelected ? " · выбран для публикации" : " · не выбран"}`}
								scope="audio"
								values={mergedValues(track.audioMetadata?.[sourceId])}
								readOnly={!isSelected}
								onSave={(values) =>
									onMutate((current) => ({
										...current,
										audioMetadata: {
											...current.audioMetadata,
											[sourceId]: values,
										},
									}))
								}
							/>
						);
					})
				) : (
					<p className="m-0 text-sm text-[var(--muted)]">
						Нет связанных аудиовариантов.
					</p>
				)}
				{track.sourceId && !audioSources.includes(track.sourceId) && (
					<p className="m-0 text-sm text-[var(--muted)]">
						Выбранный для публикации файл отсутствует среди связанных вариантов;
						метаданные файла не изменены.
					</p>
				)}
			</section>
		</div>
	);
}
