import { originalReleases } from "../providers/library/catalogData";
import { fixturePublicationDestination } from "../providers/publication/publicationOutput";

/**
 * Fixed matching recheck scenario for the operations demo.
 *
 * The failed Signals publication is the unfinished member of a recheck over
 * the canonical release. The other positions were replaced before that
 * failure, so they are reported as committed. Paths come from the fixture
 * publications under the fixture output root; no extra identities or
 * provenance are invented.
 */
const recheckReleaseId = "signals";

export const unfinishedMatchingRecheckTrackId = "signals-t6";

const outputRoot = `${fixturePublicationDestination.outputDirectory.replace(/[\\/]+$/, "")}/`;
const recheckRelease = originalReleases.find(
	(release) => release.id === recheckReleaseId,
);

export const matchingRecheckAbandonFixture = {
	targetHref: `#track/${unfinishedMatchingRecheckTrackId}`,
	positions: (recheckRelease?.tracks ?? []).flatMap((track) => {
		const path = track.published?.path;
		if (!path) return [];
		return [
			{
				operationId: `recheck-${track.id}`,
				relativePath: path.startsWith(outputRoot)
					? path.slice(outputRoot.length)
					: path,
				committed: track.id !== unfinishedMatchingRecheckTrackId,
			},
		];
	}),
};
