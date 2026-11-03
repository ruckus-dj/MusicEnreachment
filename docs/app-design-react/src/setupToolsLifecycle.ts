export const setupToolReleases = {
	ffmpeg: ["8.0.1", "8.0"],
	fpcalc: ["1.5.1"],
} as const;

export type SetupToolKind = keyof typeof setupToolReleases;
export type SetupToolState =
	| "available"
	| "downloading"
	| "failed"
	| "verified";

export const setupToolNames: Record<SetupToolKind, string> = {
	ffmpeg: "FFmpeg",
	fpcalc: "fpcalc",
};

export interface SetupToolProgress {
	release?: string;
	state: SetupToolState;
	active: boolean;
}

export const initialSetupToolProgress: Record<
	SetupToolKind,
	SetupToolProgress
> = {
	ffmpeg: { state: "available", active: false },
	fpcalc: { state: "available", active: false },
};

export function setupToolExecutables(kind: SetupToolKind): string[] {
	return kind === "ffmpeg" ? ["ffmpeg", "ffprobe"] : ["fpcalc"];
}

export function setupToolStateAfterAttempt(
	release: string,
	succeeds: boolean,
): SetupToolProgress {
	return succeeds
		? { release, state: "verified", active: true }
		: { release, state: "failed", active: false };
}
