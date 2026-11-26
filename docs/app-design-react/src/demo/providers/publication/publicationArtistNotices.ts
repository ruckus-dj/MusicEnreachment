import { publicationMessage } from "../../../ui/features/publication/publicationMessages";
import type { Locale } from "../../../ui/i18n/types";
import {
	type CatalogArtist,
	type OriginalRelease,
	type OriginalTrack,
	publicationSortTagCollisions,
} from "../library/catalogData";
import { projectArtistCreditGroup } from "../library/metadata/artistProjection";
import {
	positionCreditValues,
	releaseCreditValues,
} from "../library/metadata/scopedArtistCredits";

export function publicationArtistNotices(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[],
	locale: Locale,
) {
	const collisions = publicationSortTagCollisions(release, track, artists);
	const recording = projectArtistCreditGroup(
		positionCreditValues(track, artists),
		artists,
	);
	const releaseProjection = projectArtistCreditGroup(
		releaseCreditValues(release, artists),
		artists,
	);
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
