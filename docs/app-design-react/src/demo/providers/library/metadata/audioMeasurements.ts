export const audioMeasurementKeys = [
	"replaygain_track_gain",
	"replaygain_track_peak",
	"replaygain_album_gain",
	"replaygain_album_peak",
	"r128_track_gain",
	"r128_album_gain",
] as const;

/** Replacing audio never transfers loudness measurements from the previous audio. */
export function replaceAudioMeasurements(
	currentTags: Record<string, string[]>,
	nextAudioTags: Record<string, string[]>,
): Record<string, string[]> {
	const result = structuredClone(currentTags);
	for (const key of audioMeasurementKeys) {
		const tag = key.toUpperCase();
		delete result[tag];
		if (nextAudioTags[tag]?.length) result[tag] = [...nextAudioTags[tag]];
	}
	return result;
}
