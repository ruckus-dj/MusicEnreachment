import {
	applyPlan,
	type IncomingPlan,
	type IncomingReleases,
	selectedSourceId,
	usableFile,
} from "../incoming/incomingModel";
import type { OriginalTrack } from "../library/catalogData";
import { demoPathInside } from "../sources/demoPathValidation";
import {
	type DemoPublicationCatalog,
	publicationPathOwned,
} from "./libraryPublicationDemo";
import { retainedOutputIssue } from "./publicationOutput";

type ContextualPublicationCatalog = DemoPublicationCatalog & {
	providers: IncomingReleases;
};

export function retainedCurrentUnavailable(
	observation: OriginalTrack["observedFile"],
) {
	return (
		observation?.rootAvailable === false ||
		["changed", "drift", "missing", "output-unavailable"].includes(
			observation?.state ?? "",
		)
	);
}

export type ContextualPublicationPreparation = {
	releaseId: string;
	positionId: string;
	plan: IncomingPlan;
	evidence: string;
	result: OriginalTrack;
};

export function prepareContextualPublication(
	catalog: ContextualPublicationCatalog,
	plan: IncomingPlan,
): ContextualPublicationPreparation | null {
	const { positionId, localReleaseId: releaseId } = plan;
	if (
		!releaseId ||
		!positionId ||
		plan.mode !== "local" ||
		plan.groupId ||
		plan.inventoryVersion !== catalog.inventoryVersion ||
		catalog.hasCustomTemplate
	)
		return null;
	const release = catalog.releases.find(({ id }) => id === releaseId);
	const assignment = plan.assignments[positionId];
	const sourceId = selectedSourceId(assignment);
	const source = sourceId ? catalog.files[sourceId] : undefined;
	if (
		!release ||
		!assignment?.included ||
		!sourceId ||
		!source ||
		!usableFile(source)
	)
		return null;
	const { tracks, ...releaseFields } = release;
	const target = plan.target.tracks.find(({ id }) => id === positionId);
	const current = tracks.find(({ id }) => id === positionId);
	// The plan's own source is the stale-authority fence. An explicitly chosen
	// alternative changes only this assignment, so it must still be a linked
	// source of the live position.
	if (!target || !current || current.sourceId !== target.sourceId) return null;
	const linkedSourceIds = new Set([
		...(current.sourceIds ?? []),
		...(current.sourceId ? [current.sourceId] : []),
	]);
	if (!linkedSourceIds.has(sourceId)) return null;
	const published = current.published;
	if (published) {
		if (
			!plan.retainCurrentPath ||
			published.sourceId !== sourceId ||
			retainedCurrentUnavailable(current.observedFile) ||
			retainedOutputIssue(published, source, catalog.destination)
		)
			return null;
	} else if (plan.retainCurrentPath || plan.force) return null;
	// Forced intent rebuilds the same source; it never becomes an audio
	// replacement for a different variant.
	if (plan.force && published?.sourceId !== sourceId) return null;
	const projected = [structuredClone(release)];
	const exactPlan = structuredClone({
		...plan,
		assignments: { [positionId]: assignment },
	});
	const changed = applyPlan(
		exactPlan,
		catalog.files,
		projected,
		catalog.providers,
		false,
		catalog.artists,
		catalog.destination,
		positionId,
	);
	const result = projected[0].tracks.find(({ id }) => id === positionId);
	if (
		changed !== 1 ||
		!result?.published ||
		!demoPathInside(
			catalog.destination.outputDirectory,
			result.published.path,
		) ||
		publicationPathOwned(
			catalog,
			{ release_id: releaseId, position_id: positionId },
			result.published.path,
		)
	)
		return null;
	return {
		releaseId,
		positionId,
		plan: exactPlan,
		result,
		evidence: JSON.stringify([
			catalog.destination.outputDirectory,
			catalog.destination.format,
			releaseFields,
			current,
			source,
			result,
		]),
	};
}

export function settleContextualPublication(
	catalog: ContextualPublicationCatalog,
	prepared: ContextualPublicationPreparation,
): OriginalTrack | null {
	const live = prepareContextualPublication(catalog, prepared.plan);
	if (
		!live ||
		live.releaseId !== prepared.releaseId ||
		live.positionId !== prepared.positionId ||
		live.evidence !== prepared.evidence
	)
		return null;
	return structuredClone(prepared.result);
}

export function commitContextualPublication(
	releases: IncomingReleases,
	target: Pick<ContextualPublicationPreparation, "releaseId" | "positionId">,
	result: OriginalTrack,
): IncomingReleases {
	return releases.map((release) =>
		release.id !== target.releaseId
			? release
			: {
					...release,
					tracks: release.tracks.map((track) =>
						track.id === target.positionId ? result : track,
					),
				},
	);
}
