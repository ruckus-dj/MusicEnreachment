import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import {
	LibraryPositionTagEvidenceFrame,
	LibraryPositionTagEvidenceView,
} from "../../../ui/features/library/LibraryPositionTagEvidenceView";
import {
	desiredTags,
	type OriginalRelease,
	type OriginalTrack,
	originalArtists,
	originalProviders,
	type SourceFiles,
} from "./catalogData";
import { projectMetadataValues } from "./metadata/projection";

export type TagScope = "artist" | "release" | "track";

export function TagsComparison({
	release,
	track,
	providers = originalProviders,
	files,
	fileId,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	providers?: OriginalRelease[];
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
	const { tags: overrides } = projectMetadataValues({
		...(release.comparisonOverrides ?? {}),
		...(fileId ? (track.audioComparisonOverrides?.[fileId] ?? {}) : {}),
		...(track.recordingComparisonOverrides ?? {}),
		...(track.positionComparisonOverrides ?? {}),
	});
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
