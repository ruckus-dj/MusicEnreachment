import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import {
	LibraryPositionTagEvidenceFrame,
	LibraryPositionTagEvidenceView,
} from "../../../ui/features/library/LibraryPositionTagEvidenceView";
import {
	type CatalogArtist,
	desiredTags,
	type OriginalRelease,
	type OriginalTrack,
	originalArtists,
	originalProviders,
	positionScalarOverrideTags,
	recordingScalarOverrideTags,
	releaseScalarOverrideTags,
	type SourceFiles,
} from "./catalogData";
import { projectMetadataValues } from "./metadata/projection";
import {
	creditTagKeys,
	scopedCreditComparisonTags,
} from "./metadata/scopedArtistCredits";
import { projectCompositionTags } from "./metadata/scopedComposition";
import {
	contributorProjectionIntent,
	contributorTagKeys,
	projectScopedContributorTags,
} from "./metadata/scopedContributors";

export type TagScope = "artist" | "release" | "track";

export function TagsComparison({
	release,
	track,
	providers = originalProviders,
	artists = originalArtists,
	files,
	fileId,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	providers?: OriginalRelease[];
	artists?: CatalogArtist[];
	files: SourceFiles;
	fileId?: string | null;
}) {
	const { locale } = useI18n();
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [showAll, setShowAll] = useState(false);
	const [inspection, setInspection] = useState<{
		fileId: string;
		locationId: string;
	}>();
	const file = fileId ? files[fileId] : undefined;
	const locations = file?.locations ?? [];
	const location =
		locations.length === 1
			? locations[0]
			: file?.id === inspection?.fileId
				? locations.find(({ id }) => id === inspection?.locationId)
				: undefined;
	const boundRelease = providers.find(
		(candidate) =>
			candidate.id === release.providerId ||
			(candidate.providerId && candidate.providerId === release.providerId),
	);
	const providerTrack = boundRelease?.tracks.find(
		({ id }) => id === track.providerTrackId,
	);
	const providerName =
		boundRelease?.provider ??
		(track.providerTrackId ? release.provider || "MusicBrainz" : undefined);
	const provider = !track.providerTrackId
		? { status: "unlinked" as const }
		: boundRelease && providerTrack
			? {
					name: providerName,
					status: "saved" as const,
					tags: desiredTags(boundRelease, providerTrack, originalArtists, null),
				}
			: { name: providerName, status: "unavailable" as const };
	const overrides = {
		...projectMetadataValues(release.comparisonOverrides ?? {}).tags,
		...releaseScalarOverrideTags(release),
		...projectMetadataValues({
			...(fileId ? (track.audioComparisonOverrides?.[fileId] ?? {}) : {}),
			...(track.recordingComparisonOverrides ?? {}),
		}).tags,
		...recordingScalarOverrideTags(track, "comparison"),
		...projectMetadataValues(track.positionComparisonOverrides ?? {}).tags,
		...positionScalarOverrideTags(track, "comparison"),
		...projectCompositionTags({
			works:
				track.positionComposition?.works ?? track.recordingComposition?.works,
			movement:
				track.positionComposition?.movement ??
				track.recordingComposition?.movement,
		}),
	};
	const positionTitleOverride = track.positionComparisonOverrides?.title;
	const contributors = contributorProjectionIntent(
		track.positionContributors,
		track.recordingContributors,
		"comparison",
	);
	if (contributors) {
		for (const key of contributorTagKeys()) delete overrides[key];
		Object.assign(overrides, projectScopedContributorTags(contributors));
	}
	const trackCredit = track.positionArtistCredit ?? track.recordingArtistCredit;
	if (trackCredit) {
		for (const key of creditTagKeys("ARTIST")) delete overrides[key];
		Object.assign(
			overrides,
			scopedCreditComparisonTags(trackCredit, artists, "ARTIST"),
		);
	}
	if (release.releaseArtistCredit) {
		for (const key of creditTagKeys("ALBUMARTIST")) delete overrides[key];
		Object.assign(
			overrides,
			scopedCreditComparisonTags(
				release.releaseArtistCredit,
				artists,
				"ALBUMARTIST",
			),
		);
	}
	if (positionTitleOverride) overrides.TITLE = [...positionTitleOverride];
	return (
		<LibraryPositionTagEvidenceFrame
			locale={locale}
			hasSource={Boolean(file)}
			locations={locations.map(({ id, path, rootId }) => ({
				id,
				label: path,
				rootId,
			}))}
			locationId={location?.id}
			onLocationChange={(locationId) => {
				if (file) setInspection({ fileId: file.id, locationId });
			}}
		>
			<LibraryPositionTagEvidenceView
				locale={locale}
				locationPath={location?.path}
				source={
					location && file ? { state: "saved", tags: file.tags } : undefined
				}
				current={
					track.published
						? { state: "saved", tags: track.published.tags }
						: { state: "absent" }
				}
				comparison={{ state: "saved", provider, overrides }}
				showAll={showAll}
				onShowAllChange={setShowAll}
				differencesOnly={differencesOnly}
				onDifferencesChange={setDifferencesOnly}
			/>
		</LibraryPositionTagEvidenceFrame>
	);
}
