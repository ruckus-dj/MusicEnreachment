import {
	originalFiles,
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
	return {
		...track,
		published: {
			...track.published,
			readAt: new Date().toISOString(),
			codec: file.codec,
			sourceId: file.id,
			tags: replaceAudioMeasurements(
				track.published.tags,
				projectMetadataValues(track.audioMetadata?.[file.id] ?? {}, "audio")
					.tags,
			),
		},
		metadataBaseline: replaceAudioMeasurements(
			track.metadataBaseline ?? track.published.tags,
			projectMetadataValues(track.audioMetadata?.[file.id] ?? {}, "audio").tags,
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
