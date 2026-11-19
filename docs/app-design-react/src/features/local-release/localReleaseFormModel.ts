export type LocalReleaseArtist = {
	id: string;
	name?: string;
	provider: string;
};

export type LocalReleaseCredit = {
	artistId?: string;
	creditName: string;
	joinPhrase: string;
};

export type LocalRecordingCredit = LocalReleaseCredit & { artistId: string };
export type LocalRecordingCreditSelection = Omit<
	LocalRecordingCredit,
	"joinPhrase"
>;

export function buildLocalRecordingCredits(
	selection: readonly LocalRecordingCreditSelection[],
): LocalRecordingCredit[] {
	return selection.map((credit, index) => ({
		...credit,
		joinPhrase: index + 1 < selection.length ? " / " : "",
	}));
}

export type LocalReleaseRecording = {
	id: string;
	key?: string;
	provider: string;
	title?: string;
	duration_ms?: string | number | "" | null;
	credits?: LocalReleaseCredit[];
};

export type LocalReleaseTrack = {
	id: string;
	recordingId: string;
	titleOverride: string;
	durationOverrideMs: number | "" | null;
	durationInput?: string;
};

export type LocalReleaseTrackDraft = Omit<LocalReleaseTrack, "durationInput">;

export type LocalReleaseDraft = {
	title: string;
	release_group_title: string;
	artistIds: string[];
	primaryType?: string;
	language?: string;
	script?: string;
	tracks: LocalReleaseTrackDraft[];
};

export type LocalRecordingDraft = {
	title: string;
	duration_ms?: number;
	credits?: LocalRecordingCredit[];
};

export type FindLocalArtists = (
	query: string,
	signal?: AbortSignal,
) => Promise<readonly LocalReleaseArtist[]>;

export type SaveLocalArtist = (data: {
	name: string;
}) => LocalReleaseArtist | Promise<LocalReleaseArtist>;

export type OutcomeUncertainError = Error & { outcomeUncertain?: boolean };

export function isOutcomeUncertain(error: unknown): boolean {
	return (
		typeof error === "object" &&
		error !== null &&
		(error as { outcomeUncertain?: unknown }).outcomeUncertain === true
	);
}

export function formatLocalCredits(
	credits:
		| readonly { creditName: string; joinPhrase: string }[]
		| null
		| undefined,
): string {
	return (credits ?? [])
		.map((credit) => `${credit.creditName}${credit.joinPhrase}`)
		.join("");
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
