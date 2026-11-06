import {
	publicationSortTagCollisions,
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
} from "./catalogData";
import { projectArtistCreditGroup } from "./metadata/artistProjection";
import { publicationMessage } from "./publicationMessages";
import type { Locale } from "./i18n/types";

export function publicationArtistNotices(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[],
	locale: Locale,
) {
	const recordingCredits = track.recordingCredits?.length
		? track.recordingCredits
		: track.artistId
			? [
					{
						artistId: track.artistId,
						creditName:
							artists.find((artist) => artist.id === track.artistId)?.name ??
							"",
					},
				]
			: [];
	const releaseCredits = release.credits?.length
		? release.credits
				.map((credit) => ({
					artistId: credit.artistId ?? "",
					creditName: credit.credit || credit.name || "",
				}))
				.filter((credit) => credit.creditName)
		: release.artistId
			? [
					{
						artistId: release.artistId,
						creditName:
							artists.find((artist) => artist.id === release.artistId)?.name ??
							"",
					},
				]
			: [];
	const collisions = publicationSortTagCollisions(release, track, artists);
	const recording = projectArtistCreditGroup(recordingCredits, artists);
	const releaseProjection = projectArtistCreditGroup(releaseCredits, artists);
	return [
		...(collisions.recording
			? [publicationMessage(locale, "recordingSortCollision")]
			: []),
		...(collisions.release
			? [publicationMessage(locale, "releaseSortCollision")]
			: []),
		...(recording.missingMbid
			? [publicationMessage(locale, "recordingArtistMissingMbid")]
			: []),
		...(recording.nameCollision
			? [publicationMessage(locale, "recordingArtistNameCollision")]
			: []),
		...(releaseProjection.missingMbid
			? [publicationMessage(locale, "releaseArtistMissingMbid")]
			: []),
		...(releaseProjection.nameCollision
			? [publicationMessage(locale, "releaseArtistNameCollision")]
			: []),
	];
}
