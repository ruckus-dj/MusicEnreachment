import fixture from "./original-fixtures.json";
import type { OriginalRelease, OriginalTrack } from "./catalogData";

export type ManualArtistDraft = {
	name: string;
	credit: string;
	role: string;
	artistId: string | null;
};

export type ManualPositionDraft = {
	number: string;
	title: string;
	duration: string;
	recordingMode: "new" | "existing";
	recordingId: string;
	newRecordingTitle: string;
	sourceIds: string[];
	selectedSourceId: string;
	pendingSourceId?: string;
};

export type ManualMediumDraft = {
	title: string;
	format: string;
	positions: ManualPositionDraft[];
};

export type ManualReleaseDraft = {
	title: string;
	metadata: {
		date: string;
		country: string;
		label: string;
		catalogNumber: string;
		type: string;
	};
	artists: ManualArtistDraft[];
	media: ManualMediumDraft[];
	coverId: string;
};

export type ManualArtist = {
	id: string;
	name: string;
	genre?: string;
	color?: string;
};
export type ManualRecording = {
	id: string;
	title: string;
	artists: string;
	duration: string;
};
export type ManualSource = {
	id: string;
	name: string;
	kind: string;
	path: string;
};
export type ManualCover = {
	id: string;
	title: string;
	source: string;
	mark: string;
	style: string;
};

const manualData = fixture.manualReleaseDemoData;
export const manualArtists: ManualArtist[] = manualData.artists;
export const manualRecordings: ManualRecording[] = manualData.recordings;
export const manualSources: ManualSource[] = manualData.sources;
export const manualCovers: ManualCover[] = manualData.covers;

export const emptyManualPosition = (): ManualPositionDraft => ({
	number: "",
	title: "",
	duration: "",
	recordingMode: "new",
	recordingId: "",
	newRecordingTitle: "",
	sourceIds: [],
	selectedSourceId: "",
	pendingSourceId: "",
});

export const emptyManualMedium = (): ManualMediumDraft => ({
	title: "",
	format: "",
	positions: [],
});

export function createManualReleaseDemoDraft(): ManualReleaseDraft {
	const release: OriginalRelease | undefined = fixture.releases.find(
		(item) => item.id === "north",
	);
	if (!release)
		throw new Error("Missing Northern Lines manual-release fixture");
	const artist = manualArtists.find((item) => item.id === release.artistId);
	return {
		title: "Blue Hour",
		metadata: {
			date: release.year,
			country: release.country,
			label: release.label,
			catalogNumber: release.catalog,
			type: "Альбом",
		},
		artists: artist
			? [
					{
						name: artist.name,
						credit: artist.name,
						role: "основной артист",
						artistId: artist.id,
					},
				]
			: [],
		media: [
			{
				title: "Сторона A",
				format: "Демо-носитель",
				positions: release.tracks.slice(0, 2).map((track) => ({
					number: String(track.position),
					title: track.title,
					duration: formatDuration(track.duration),
					recordingMode: track.recordingId ? "existing" : "new",
					recordingId: track.recordingId ?? "",
					newRecordingTitle: track.title,
					sourceIds:
						track.sourceIds ?? (track.sourceId ? [track.sourceId] : []),
					selectedSourceId: track.sourceId ?? "",
				})),
			},
		],
		coverId: "cover-north",
	};
}

const formatDuration = (seconds: number | null) =>
	seconds === null
		? ""
		: `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
const parseDuration = (value: string): number | null => {
	const match = /^(\d+):(\d{1,2})$/.exec(value.trim());
	return match && Number(match[2]) < 60
		? Number(match[1]) * 60 + Number(match[2])
		: null;
};

const normalized = (value: string) =>
	value.normalize("NFKC").toLocaleLowerCase().replace(/\s+/g, " ").trim();

export function createManualRelease(
	draft: ManualReleaseDraft,
	sequence: number,
	artists: ManualArtist[],
): { release: OriginalRelease; artists: ManualArtist[] } {
	const id = `manual-${sequence}`;
	const nextArtists = [...artists];
	const credits = draft.artists.map((credit, index) => {
		const name = credit.name.trim();
		if (!name) return { ...credit, artistId: null };
		const existing =
			nextArtists.find(
				(artist) => normalized(artist.name) === normalized(name),
			) ??
			manualArtists.find(
				(artist) => normalized(artist.name) === normalized(name),
			);
		const artistId =
			credit.artistId || existing?.id || `${id}-artist-${index + 1}`;
		if (!existing && !credit.artistId)
			nextArtists.push({ id: artistId, name, genre: "Демо", color: "slate" });
		return { ...credit, artistId };
	});
	const artistId = credits.find((credit) => credit.artistId)?.artistId ?? null;
	let positionIndex = 0;
	const tracks: OriginalTrack[] = draft.media.flatMap((medium, mediumIndex) =>
		medium.positions.map((position, mediumPositionIndex) => {
			positionIndex += 1;
			return {
				id: `${id}-m${mediumIndex + 1}-p${mediumPositionIndex + 1}`,
				position: positionIndex,
				mediumPosition: mediumPositionIndex + 1,
				displayedNumber: position.number,
				medium: mediumIndex + 1,
				title: position.title,
				duration: parseDuration(position.duration),
				artistId,
				recordingId:
					position.recordingMode === "existing"
						? position.recordingId || null
						: null,
				recordingTitle:
					position.recordingMode === "existing"
						? (manualRecordings.find((item) => item.id === position.recordingId)
								?.title ?? "")
						: position.newRecordingTitle,
				localRecordingId:
					position.recordingMode === "existing"
						? null
						: `${id}-recording-${mediumIndex + 1}-${mediumPositionIndex + 1}`,
				sourceIds: [...position.sourceIds],
				sourceId: position.selectedSourceId || null,
				providerTrackId: null,
				published: null,
			};
		}),
	);
	const cover = manualCovers.find((item) => item.id === draft.coverId);
	return {
		release: {
			id,
			title: draft.title,
			artistId,
			year: draft.metadata.date,
			country: draft.metadata.country,
			label: draft.metadata.label,
			catalog: draft.metadata.catalogNumber,
			providerId: null,
			manual: true,
			credits,
			metadata: { ...draft.metadata },
			media: structuredClone(draft.media),
			coverId: draft.coverId,
			color:
				cover?.style === "blue"
					? "slate"
					: cover?.style === "gold"
						? "sand"
						: "clay",
			tracks,
		},
		artists: nextArtists,
	};
}
