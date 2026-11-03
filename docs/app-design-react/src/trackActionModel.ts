import {
	desiredTags,
	originalArtistName,
	originalFiles,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
	type CatalogArtist,
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
		providerTitle: `${track.title} · кэш провайдера обновлён`,
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
			codec: file.codec,
			sourceId: file.id,
			tags: replaceAudioMeasurements(
				track.published.tags,
				projectMetadataValues(track.audioMetadata?.[file.id] ?? {}, "audio")
					.tags,
			),
			readAt: "Только что · демо изменения качества аудио",
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

export function explicitlyRepublish(
	release: OriginalRelease,
	track: OriginalTrack,
	downgradeConfirmed: boolean,
	files: SourceFiles = originalFiles,
	artists?: CatalogArtist[],
): OriginalTrack {
	const file = track.sourceId ? files[track.sourceId] : undefined;
	if (
		!file ||
		(track.published?.codec === "FLAC" &&
			file.codec === "MP3" &&
			!downgradeConfirmed)
	)
		return track;
	const tags = desiredTags(release, track, artists);
	const coverHash = track.coverHash || null;
	const lyrics = track.lyrics || null;
	return {
		...track,
		published: {
			...track.published,
			tags,
			coverHash,
			lyrics,
			codec: file.codec,
			container: "MKA",
			sourceId: file.id,
			readAt: "Только что · явная перепубликация демо",
			path:
				track.published?.path ||
				`/srv/music/${originalArtistName(release.artistId, artists)}/${release.year} - ${release.title}/${String(track.position).padStart(2, "0")} - ${track.title}.mka`,
		},
		metadataBaseline: structuredClone(tags),
		metadataBaselineExtras: { coverHash, lyrics },
		observedFile: null,
	};
}
