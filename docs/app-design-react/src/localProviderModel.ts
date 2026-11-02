import originalFixtures from "./original-fixtures.json";
import type { OriginalRelease } from "./catalogData";

export type LocalProviderType = "artists" | "releases" | "recordings";
export type LocalProviderTrack = {
	id: string;
	recordingId: string;
	titleOverride: string;
	durationOverrideMs: number | "" | null;
	durationInput?: string;
};
export type LocalProviderRecord = {
	id: string;
	key: string;
	provider: string;
	name?: string;
	title?: string;
	release_group_title?: string;
	disambiguation?: string;
	genres?: string[];
	genreNamesById?: Record<string, string>;
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
	notice: string;
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
	const localArtists = state.items.artists.filter(
		(item) => item.provider === "Local",
	);
	const localRecordings = state.items.recordings.filter(
		(item) => item.provider === "Local",
	);
	return state.items.releases
		.filter((release) => release.provider === "Local")
		.map((release) => {
			const artistIds = release.artistIds ?? [];
			const artistId = artistIds[0] ?? null;
			const credits = artistIds.flatMap((id) => {
				const artist = localArtists.find((item) => item.id === id);
				return artist ? [{ name: artist.name, artistId: artist.id }] : [];
			});
			return {
				id: release.id,
				provider: "Local",
				title: release.title ?? release.release_group_title ?? "",
				artistId,
				year: release.release_date ?? "",
				country: release.country_code ?? "",
				catalog: release.catalog_number ?? "",
				label: release.label ?? "",
				providerId: release.key,
				credits,
				tracks: (release.tracks ?? []).map((item, index) => {
					const recording = localRecordings.find(
						(candidate) => candidate.id === item.recordingId,
					);
					const duration = item.durationOverrideMs || recording?.duration_ms;
					return {
						id: `${release.id}-${item.id}`,
						title: item.titleOverride || recording?.title || "",
						position: index + 1,
						duration:
							duration == null || duration === ""
								? null
								: Number(duration) / 1000,
						artistId: recording?.artistIds?.[0] ?? artistId,
						recordingId: null,
						localRecordingId: recording?.id ?? item.recordingId,
						providerTrackId: null,
						sourceId: null,
						published: null,
					};
				}),
			};
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
