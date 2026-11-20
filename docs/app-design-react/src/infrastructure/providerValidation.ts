export type MusicBrainzConfiguration = {
	mode: "" | "public" | "self-hosted";
	url?: string;
	throttle?: boolean;
	delay?: string;
};

export function isValidMusicBrainzBaseURL(value: string): boolean {
	try {
		const url = new URL(value.trim());
		return (
			(url.protocol === "http:" || url.protocol === "https:") &&
			Boolean(url.hostname)
		);
	} catch {
		return false;
	}
}

export function musicBrainzConfigurationFingerprint(
	configuration: MusicBrainzConfiguration,
): string {
	return JSON.stringify([
		configuration.mode,
		configuration.mode === "self-hosted"
			? (configuration.url?.trim() ?? "")
			: "",
		configuration.mode === "self-hosted"
			? (configuration.throttle ?? false)
			: false,
		configuration.mode === "self-hosted"
			? (configuration.delay?.trim() ?? "")
			: "",
	]);
}

export function isValidMusicBrainzConfiguration(
	configuration: MusicBrainzConfiguration,
): boolean {
	return (
		configuration.mode === "public" ||
		(configuration.mode === "self-hosted" &&
			isValidMusicBrainzBaseURL(configuration.url ?? ""))
	);
}
