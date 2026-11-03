import type { CatalogArtist, OriginalRelease } from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { artistDeletionPlan } from "./ArtistDeletionRequest";

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
