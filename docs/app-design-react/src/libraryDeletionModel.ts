import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { artistDeletionPlan } from "./ArtistDeletionRequest";
import {
	type ManagedPathObservation,
	type ManagedPublicationDeletionResult,
	resolveManagedPublicationDeletion,
} from "./managedPublicationDeletionModel";

/** The demo applies only the identities and publication paths confirmed by the operator. */
export function completeLibraryDeletion(
	operation: DeletionOperation,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
): { releases: OriginalRelease[]; artists: CatalogArtist[] } | null {
	const trackIds = new Set(operation.libraryTrackIds ?? []);
	const releaseIds = new Set(operation.libraryReleaseIds ?? []);
	if (!trackIds.size && !releaseIds.size && !operation.libraryArtistId)
		return null;
	const tracks = releases.flatMap((release) => release.tracks);
	if ([...trackIds].some((id) => !tracks.some((track) => track.id === id)))
		return null;
	if (
		[...releaseIds].some((id) => !releases.some((release) => release.id === id))
	)
		return null;
	if (
		operation.libraryArtistId &&
		!artists.some((artist) => artist.id === operation.libraryArtistId)
	)
		return null;
	if (operation.libraryArtistId) {
		const current = artistDeletionPlan(
			operation.libraryArtistId,
			artists,
			releases,
		);
		if (
			!current ||
			JSON.stringify([...(current.libraryReleaseIds ?? [])].sort()) !==
				JSON.stringify([...releaseIds].sort()) ||
			JSON.stringify([...(current.libraryTrackIds ?? [])].sort()) !==
				JSON.stringify([...trackIds].sort())
		)
			return null;
	}
	// New dependencies require a fresh confirmation, not a widened deletion scope.
	if (
		releases.some(
			(release) =>
				releaseIds.has(release.id) &&
				release.tracks.some((track) => !trackIds.has(track.id)),
		)
	)
		return null;
	const actualPaths = tracks
		.filter((track) => trackIds.has(track.id))
		.flatMap((track) => (track.published ? [track.published.path] : []))
		.sort();
	const confirmedPaths = [...operation.paths].sort();
	if (
		JSON.stringify(actualPaths) !== JSON.stringify(confirmedPaths) ||
		actualPaths.length !== operation.publications
	)
		return null;
	if (operation.kind === "publication") {
		return {
			artists,
			releases: releases.map((release) => ({
				...release,
				tracks: release.tracks.map((track) =>
					trackIds.has(track.id)
						? {
								...track,
								published: null,
								metadataBaseline: undefined,
								metadataBaselineExtras: undefined,
							}
						: track,
				),
			})),
		};
	}
	return {
		artists: artists.filter(
			(artist) => artist.id !== operation.libraryArtistId,
		),
		releases: releases
			.filter((release) => !releaseIds.has(release.id))
			.map((release) => ({
				...release,
				tracks: release.tracks.filter((track) => !trackIds.has(track.id)),
			})),
	};
}

/** Explicit demo evidence for one confirmed managed path; no filesystem access. */
export interface ManagedPathDeletionEvidence {
	state: ManagedPathObservation;
	deleteSucceeded: boolean;
}

/** Missing is conclusive only when the output root was confirmed available. */
export function managedDeletionObservation(
	observation: OriginalTrack["observedFile"],
): ManagedPathObservation {
	// The fixed fixture's registered publication is the only default observation.
	if (!observation) return "present";
	if (observation.rootAvailable === false) return "unknown";
	if (observation.state === "missing")
		return observation.rootAvailable === true ? "absent" : "unknown";
	if (["changed", "drift"].includes(observation.state ?? "")) return "replaced";
	if (["available", "unchanged"].includes(observation.state ?? ""))
		return "present";
	return "unknown";
}

/** A missing path entry is unknown and can never justify removing rows. */
export interface ManagedDeletionEvidence {
	paths: Partial<Record<string, ManagedPathDeletionEvidence>>;
	metadataCommitSucceeded: boolean;
}

export type ManagedLibraryDeletionOutcome =
	| {
			status: "completed";
			releases: OriginalRelease[];
			artists: CatalogArtist[];
	  }
	| {
			status: Exclude<ManagedPublicationDeletionResult, "completed">;
	  };

/**
 * Validates the confirmed scope first, then interprets explicit per-path
 * evidence and the metadata commit outcome. Rows are released only when every
 * confirmed managed path is safe and the metadata commit succeeded.
 */
export function completeManagedLibraryDeletion(
	operation: DeletionOperation,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
	evidence: ManagedDeletionEvidence,
): ManagedLibraryDeletionOutcome {
	const next = completeLibraryDeletion(operation, releases, artists);
	if (!next) return { status: "stale" };
	let retry = !evidence.metadataCommitSucceeded;
	for (const path of operation.paths) {
		const observed = evidence.paths[path];
		const resolution = resolveManagedPublicationDeletion(
			observed?.state ?? "unknown",
			observed?.deleteSucceeded ?? false,
			evidence.metadataCommitSucceeded,
		);
		if (resolution === "conflict") return { status: "conflict" };
		if (resolution === "retry") retry = true;
	}
	return retry
		? { status: "retry" }
		: { status: "completed", releases: next.releases, artists: next.artists };
}
