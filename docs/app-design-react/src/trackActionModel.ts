import {
	originalFiles,
	sourceOnlyAudioTagKeys,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";
import { replaceAudioMeasurements } from "./metadata/audioMeasurements";
import { projectMetadataValues } from "./metadata/projection";

export function renameLocalTrack(
	track: OriginalTrack,
	title: string,
): OriginalTrack {
	return { ...track, title: title.trim() || track.title };
}

export function refreshProviderHint(track: OriginalTrack): OriginalTrack {
	return {
		...track,
		providerCacheVersion: (track.providerCacheVersion || 1) + 1,
	};
}

export function upgradeAudioOnly(
	track: OriginalTrack,
	files: SourceFiles = originalFiles,
): OriginalTrack {
	const file = track.sourceId ? files[track.sourceId] : undefined;
	if (track.published?.codec !== "MP3" || file?.codec !== "FLAC") return track;
	const replaceSourceEncoderTags = (tags: Record<string, string[]>) => {
		const next = { ...tags };
		for (const key of sourceOnlyAudioTagKeys) {
			delete next[key];
			if (Object.hasOwn(file.tags, key)) next[key] = [...file.tags[key]];
		}
		return next;
	};
	const audioMeasurements = projectMetadataValues(
		track.audioMetadata?.[file.id] ?? {},
		"audio",
	).tags;
	return {
		...track,
		published: {
			...track.published,
			readAt: new Date().toISOString(),
			codec: file.codec,
			sourceId: file.id,
			tags: replaceSourceEncoderTags(
				replaceAudioMeasurements(track.published.tags, audioMeasurements),
			),
		},
		metadataBaseline: replaceSourceEncoderTags(
			replaceAudioMeasurements(
				track.metadataBaseline ?? track.published.tags,
				audioMeasurements,
			),
		),
		metadataBaselineExtras: structuredClone(
			track.metadataBaselineExtras || {
				coverHash: track.published.coverHash || null,
				lyrics: track.published.lyrics || null,
			},
		),
	};
}

export function applyAudioUpgradePreview(
	releases: OriginalRelease[],
	selectedIds: Iterable<string>,
	files: SourceFiles = originalFiles,
): OriginalRelease[] {
	const selected = new Set(selectedIds);
	return structuredClone(releases).map((release) => ({
		...release,
		tracks: release.tracks.map((track) =>
			selected.has(track.id) ? upgradeAudioOnly(track, files) : track,
		),
	}));
}
