import originalFixtures from "./original-fixtures.json";
import type { OriginalRelease } from "./catalogData";
import type { MetadataCredit } from "./metadata/model";
import type { MetadataValues } from "./metadata/projection";

export type LocalProviderType = "artists" | "releases" | "recordings";
export type LocalProviderNoticeDescriptor =
	| { key: "recordCreated" }
	| { key: "recordUpdated" };
export type LocalProviderTrack = {
	id: string;
	recordingId: string;
	titleOverride: string;
	durationOverrideMs: number | "" | null;
	durationInput?: string;
	metadataValues?: MetadataValues;
	credits?: MetadataCredit[];
	genres?: string[];
	genreNamesById?: Record<string, string>;
};
export type LocalProviderRecord = {
	id: string;
	key: string;
	provider: string;
	name?: string;
	sortName?: string;
	title?: string;
	release_group_title?: string;
	disambiguation?: string;
	genres?: string[];
	genreNamesById?: Record<string, string>;
	metadataValues?: MetadataValues;
	credits?: MetadataCredit[];
	script?: string;
	tags?: string;
	artistIds?: string[];
	duration_ms?: string | number | "" | null;
	explicit?: string;
	isrcs?: string;
	country_code?: string;
	release_date?: string;
	label?: string;
	catalog_number?: string;
	status?: string;
	packaging?: string;
	barcode?: string;
	primaryType?: string;
	secondaryTypes?: string[] | string;
	durationInput?: string;
	language?: string;
	tracks?: LocalProviderTrack[];
};
export type LocalProviderState = {
	type: LocalProviderType;
	selectedId: string | null;
	query: string;
	editing: boolean;
	creating: boolean;
	draft: LocalProviderRecord | null;
	items: Record<LocalProviderType, LocalProviderRecord[]>;
	/** Legacy strings remain accepted for callers that own a Local-provider state. */
	notice: string | LocalProviderNoticeDescriptor;
};

export const localProviderFixtures = originalFixtures.localProvider as Record<
	LocalProviderType,
	LocalProviderRecord[]
>;

export function createLocalProviderState(): LocalProviderState {
	const items = structuredClone(localProviderFixtures);
	for (const release of items.releases) {
		for (const track of release.tracks ?? []) track.id ||= crypto.randomUUID();
	}
	return {
		type: "artists",
		selectedId: "local-artist-vale",
		query: "",
		editing: false,
		creating: false,
		draft: null,
		items,
		notice: "",
	};
}

/** Adapt Local-provider records into matching candidates without inventing MBIDs. */
export function localProviderReleases(
	state: LocalProviderState,
): OriginalRelease[] {
	// Local releases may intentionally reference records owned by another provider.
	// Resolve those identities from the complete state catalogs; only the release
	// itself must be Local to enter this adapter.
	const artists = state.items.artists;
	const recordings = state.items.recordings;
	return state.items.releases
		.filter((release) => release.provider === "Local")
		.map((release) => {
			const artistIds = release.artistIds ?? [];
			const artistId = artistIds[0] ?? null;
			const credits = artistIds.flatMap((id) => {
				const artist = artists.find((item) => item.id === id);
				return artist ? [{ name: artist.name, artistId: artist.id }] : [];
			});
			const metadataCredits = release.credits?.length
				? release.credits.map((credit, index) => ({
						name: credit.creditName,
						credit: credit.creditName,
						artistId: credit.artistId || artistIds[index] || null,
						joinPhrase: credit.joinPhrase,
					}))
				: credits;
			const candidate = {
				id: release.id,
				provider: "Local",
				title:
					release.metadataValues?.album?.[0] ??
					release.title ??
					release.release_group_title ??
					"",
				artistId,
				year: release.release_date ?? "",
				country: release.country_code ?? "",
				catalog: release.catalog_number ?? "",
				label: release.label ?? "",
				providerId: release.key,
				credits: metadataCredits,
				metadataValues: structuredClone(release.metadataValues ?? {}),
				metadataGenres: (release.genres ?? []).map((id) => ({
					id,
					name: release.genreNamesById?.[id] ?? id,
				})),
				tracks: (release.tracks ?? []).map((item, index) => {
					const recording = recordings.find(
						(candidate) => candidate.id === item.recordingId,
					);
					const localRecording = recording?.provider === "Local";
					const duration =
						item.durationOverrideMs === "" || item.durationOverrideMs == null
							? recording?.duration_ms
							: item.durationOverrideMs;
					const recordingTitle =
						recording?.metadataValues?.title?.[0] ?? recording?.title ?? "";
					return {
						id: `${release.id}-${item.id}`,
						title: item.titleOverride || recordingTitle,
						recordingTitle,
						positionTitleOverride: item.titleOverride,
						position: index + 1,
						duration:
							duration == null || duration === ""
								? null
								: Number(duration) / 1000,
						artistId: recording?.artistIds?.[0] ?? artistId,
						recordingMetadata: structuredClone(recording?.metadataValues ?? {}),
						recordingCredits: structuredClone(
							recording?.credits ??
								recording?.artistIds?.map((artistId) => ({
									artistId,
									creditName:
										artists.find((artist) => artist.id === artistId)?.name ??
										"",
									joinPhrase: "",
								})) ??
								[],
						),
						recordingGenres: (recording?.genres ?? []).map(
							(id) => recording?.genreNamesById?.[id] ?? id,
						),
						recordingGenreRefs: (recording?.genres ?? []).map((id) => ({
							id,
							name: recording?.genreNamesById?.[id] ?? id,
						})),
						positionMetadata: structuredClone(item.metadataValues ?? {}),
						recordingId:
							recording?.provider === "MusicBrainz" ? recording.key : null,
						localRecordingId: localRecording ? recording.id : null,
						providerTrackId: null,
						sourceId: null,
						published: null,
					};
				}),
			};
			return Object.assign(candidate, {
				script: release.script ?? release.metadataValues?.script?.[0],
			});
		});
}

export function formatLocalDuration(milliseconds: unknown): string {
	if (milliseconds === "" || milliseconds == null) return "";
	const value = Number(milliseconds);
	return `${Math.floor(value / 60_000)}:${String(Math.floor((value % 60_000) / 1000)).padStart(2, "0")}`;
}

export function parseLocalDuration(value: string): number | null {
	const input = value.trim();
	if (!input) return null;
	const seconds = /^\d+$/.test(input)
		? Number(input)
		: (() => {
				const match = /^(\d+):(\d{1,2})$/.exec(input);
				if (!match || Number(match[2]) > 59) return Number.NaN;
				return Number(match[1]) * 60 + Number(match[2]);
			})();
	const milliseconds = seconds * 1000;
	if (!Number.isFinite(milliseconds) || !Number.isSafeInteger(milliseconds))
		return Number.NaN;
	return milliseconds;
}
