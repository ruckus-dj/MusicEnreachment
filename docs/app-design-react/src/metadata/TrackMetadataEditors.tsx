import { MetadataEditor } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";
import type { CatalogArtist, OriginalTrack, SourceFiles } from "../catalogData";
import { originalProviders } from "../catalogData";
import { useI18n } from "../i18n/I18nProvider";
import { metadataEditorMessages } from "./editorMessages";

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
	const { locale } = useI18n();
	const m = (
		key: keyof typeof metadataEditorMessages.en,
		values: Record<string, string | number> = {},
	) =>
		metadataEditorMessages[locale][key].replace(
			/\{([^}]+)\}/g,
			(_, name: string) => String(values[name] ?? `{${name}}`),
		);
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
			label: m("recordingDisambiguation"),
			value: extended.disambiguation ?? "",
		},
	];
	const titleBaseline = recordingTitleBaseline(track);
	const positionExtras = [
		{
			key: "titleOverride",
			label: m("positionTitleOverride"),
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
			aria-label={`${m("positionDetails")} ${track.id}`}
		>
			<MetadataEditor
				label={m("recordingDetails")}
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
			<details className="rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
				<summary className="cursor-pointer font-semibold">
					{m("positionSummary", {
						title: positionExtras[0].value || track.title,
						number: position.track?.[0] ?? "",
						disc: position.disc?.[0] ?? "1",
					})}
				</summary>
				<div className="mt-3">
					<MetadataEditor
						label={m("positionDetails")}
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
									Number.isFinite(medium) && medium > 0
										? medium
										: current.medium,
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
				</div>
			</details>
			<section className="min-w-0" aria-label={m("audioMetadata")}>
				<details className="rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
					<summary className="cursor-pointer font-semibold">
						{m("volumeEncoding")} ·{" "}
						{track.sourceId
							? (files[track.sourceId]?.name ?? track.sourceId)
							: m("noSelectedSource")}
					</summary>
					<div className="mt-3">
						{track.sourceId && audioSources.includes(track.sourceId) ? (
							<MetadataEditor
								key={track.sourceId}
								label={`${files[track.sourceId]?.name ?? track.sourceId} · ${m("selectedForPublication")}`}
								scope="audio"
								values={mergedValues(track.audioMetadata?.[track.sourceId])}
								onSave={(values) =>
									onMutate((current) => ({
										...current,
										audioMetadata: {
											...current.audioMetadata,
											[track.sourceId as string]: values,
										},
									}))
								}
							/>
						) : (
							<p className="m-0 text-sm text-[var(--muted)]">
								{m("noLinkedVariant")}
							</p>
						)}
						{track.sourceId && !audioSources.includes(track.sourceId) && (
							<p className="m-0 text-sm text-[var(--muted)]">
								{m("selectedFileUnavailable")}
							</p>
						)}
						{audioSources.some((id) => id !== track.sourceId) && (
							<details className="mt-3">
								<summary className="cursor-pointer text-sm font-semibold">
									{m("otherAudioVariants", {
										count: audioSources.filter((id) => id !== track.sourceId)
											.length,
									})}
								</summary>
								<div className="mt-3 grid min-w-0 gap-3">
									{audioSources
										.filter((id) => id !== track.sourceId)
										.map((sourceId) => (
											<MetadataEditor
												key={sourceId}
												label={`${files[sourceId]?.name ?? sourceId} · ${m("readOnly")}`}
												scope="audio"
												values={mergedValues(track.audioMetadata?.[sourceId])}
												readOnly
												onSave={() => undefined}
											/>
										))}
								</div>
							</details>
						)}
					</div>
				</details>
			</section>
		</div>
	);
}
