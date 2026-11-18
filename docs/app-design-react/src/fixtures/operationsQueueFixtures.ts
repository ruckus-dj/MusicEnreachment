import {
	originalArtistName,
	originalFiles,
	originalReleases,
	trackNumberLabel,
} from "../catalogData";
import fixtureData from "../original-fixtures.json";

/**
 * Deterministic scale sample for the prototype operations queue.
 *
 * The corpus cycles canonical catalog objects so the Queue view, filters,
 * pagination and the range total can be reviewed without a demo "generate
 * queue" control. Labels and links are derived from existing fixture
 * identities only: no raw internal IDs are displayed and no provenance is
 * invented. Objects repeat by design; rows remain stable across renders.
 */

export type QueuedOperationKind =
	| "publication"
	| "analysis"
	| "removal"
	| "matching";

export type QueuedOperationLabel =
	| "publishPositions"
	| "analyzeSource"
	| "removeLibrary"
	| "matchIncoming";

export interface QueuedOperationFixture {
	id: string;
	kind: QueuedOperationKind;
	operation: QueuedOperationLabel;
	target: string;
	targetDetail?: string;
	targetHref?: string;
	createdAt: number;
}

interface QueueObject {
	kind: QueuedOperationKind;
	operation: QueuedOperationLabel;
	target: string;
	targetDetail?: string;
	targetHref?: string;
}

interface FixtureIncomingGroup {
	id: string;
	title: string;
	artistId: string;
}

const incomingGroups =
	(fixtureData as unknown as { incomingGroups?: FixtureIncomingGroup[] })
		.incomingGroups ?? [];

const queueObjects: QueueObject[] = [];

for (const release of originalReleases) {
	const artist = originalArtistName(release.artistId);
	for (const track of release.tracks) {
		queueObjects.push({
			kind: "publication",
			operation: "publishPositions",
			target: `${release.title} · ${trackNumberLabel(release, track)} · ${track.title}`,
			targetDetail: artist,
			targetHref: `#track/${track.id}`,
		});
	}
	queueObjects.push({
		kind: "removal",
		operation: "removeLibrary",
		target: release.title,
		targetDetail: artist,
		targetHref: `#release/${release.id}`,
	});
}

for (const file of Object.values(originalFiles)) {
	queueObjects.push({
		kind: "analysis",
		operation: "analyzeSource",
		target: file.name,
		targetDetail:
			[file.tags.ARTIST?.[0], file.tags.TITLE?.[0]]
				.filter(Boolean)
				.join(" · ") || undefined,
	});
}

for (const group of incomingGroups) {
	queueObjects.push({
		kind: "matching",
		operation: "matchIncoming",
		target: group.title,
		targetDetail: originalArtistName(group.artistId),
		targetHref: `#match/${group.id}`,
	});
}

const queueSize = 2400;
// Fixed baseline so the queue timeline is identical on every render.
const baseline = Date.UTC(2026, 10, 18, 10, 0);

export const queuedOperationFixtures: QueuedOperationFixture[] = Array.from(
	{ length: queueSize },
	(_, index) => {
		const object = queueObjects[index % queueObjects.length];
		return {
			id: `queue-${index + 1}`,
			...object,
			createdAt: baseline - index * 60_000,
		};
	},
);
