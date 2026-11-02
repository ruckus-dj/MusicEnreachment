import originalFixtures from "./original-fixtures.json";

export type LocalProviderType = "artists" | "releases" | "recordings";
export type LocalProviderTrack = {
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
	if (
		!Number.isFinite(milliseconds) ||
		!Number.isSafeInteger(milliseconds)
	)
		return Number.NaN;
	return milliseconds;
}
