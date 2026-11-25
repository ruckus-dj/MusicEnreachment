import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import { catalogueMessage } from "../../../ui/features/library/catalogueMessages";
import {
	type LibraryReleaseOverviewModel,
	LibraryReleaseWorkspaceView,
} from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import { MetadataEditorFocusView } from "../../../ui/features/library/metadata/MetadataEditorFocusView";
import type {
	ReleaseOverviewCredit,
	ReleaseOverviewMedia,
} from "../../../ui/features/library/ReleaseOverviewView";
import {
	ReleasePublicationDemo,
	type ReleasePublicationDemoProvider,
} from "../publication/ReleasePublicationDemo";
import { availabilityRowClass } from "./availabilityRows";
import {
	type CatalogArtist,
	metadataChanged,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
	trackNumberLabel,
} from "./catalogData";
import { type DeletionOperation, DeletionRequest } from "./DeletionRequest";
import { libraryRootHref } from "./libraryRoute";
import { audioProperties } from "./metadata/audioProperties";
import { ReleaseMetadataEditor } from "./metadata/ReleaseMetadataEditor";

export type ReleaseOverviewScreenProps = {
	release: OriginalRelease;
	artists: CatalogArtist[];
	files: SourceFiles;
	selected?: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onUpdateRelease?: (release: OriginalRelease) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	publication?: ReleasePublicationDemoProvider;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
	/** Release-scope tag links focus the metadata editor region on arrival. */
	initialTagFocus?: boolean;
};

const sourceIds = (track: OriginalTrack) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];

/**
 * Release overview demo provider. It resolves release facts, media grouping and
 * selection into the typed overview model and mounts the shared edition
 * workspace. The existing metadata, publication and deletion actors stay narrow
 * slots. The exact route keeps the provider separate from the copy-ready view.
 */
export function ReleaseOverviewScreen({
	release: givenRelease,
	artists,
	files,
	selected = new Set(),
	onSelect,
	onUpdateRelease,
	onQueueDeletion,
	publication,
	genresLoaded = false,
	onLoadGenres,
	initialTagFocus = false,
}: ReleaseOverviewScreenProps) {
	const { locale } = useI18n();
	const m = (
		key: Parameters<typeof catalogueMessage>[1],
		values?: Record<string, string | number>,
	) => catalogueMessage(locale, key, values);
	const [localRelease, setLocalRelease] = useState(givenRelease);
	const release = onUpdateRelease ? givenRelease : localRelease;
	const artist = artists.find((item) => item.id === release.artistId);
	const hasAudio = (track: OriginalTrack) =>
		Boolean(track.published) ||
		sourceIds(track).some(
			(id) => files[id]?.available && files[id]?.locations?.length,
		);
	const trackRows = release.tracks.map((track) => ({
		id: track.id,
		numberLabel: trackNumberLabel(release, track),
		title: track.title,
		href: `#track/${track.id}`,
		durationMs: track.duration == null ? null : track.duration * 1000,
		properties: audioProperties(
			track.sourceId ? files[track.sourceId] : undefined,
		),
		hasAudio: hasAudio(track),
		sourceCount: sourceIds(track).length,
		publication: track.published
			? {
					state: "published" as const,
					container: track.published.container,
					codec: track.published.codec,
				}
			: { state: "not-published" as const },
		isNewRecording: !track.recordingId,
		fileEvidence: track.observedFile ?? undefined,
		metadataChanged: track.published
			? metadataChanged(release, track, artists)
			: false,
		filterClass: availabilityRowClass(track, "all", hasAudio),
		selection: {
			isSelected: selected.has(track.id),
			isDisabled: !onSelect,
			onChange: (value: boolean) => onSelect?.([track.id], value),
		},
	}));
	const trackIds = release.tracks.map((track) => track.id);
	const everySelected = release.tracks.every((track) => selected.has(track.id));
	const someSelected = release.tracks.some((track) => selected.has(track.id));
	const creditFor = (
		artistId: string | null | undefined,
		fallbackName?: string | null,
	): ReleaseOverviewCredit | null => {
		const resolved = artistId
			? artists.find((item) => item.id === artistId)
			: undefined;
		const name = fallbackName || resolved?.name;
		if (!name) return null;
		return {
			name,
			...(resolved
				? { href: `#artist/${encodeURIComponent(resolved.id)}` }
				: {}),
			joinPhrase: null,
		};
	};
	const explicitCredits = (release.credits ?? []).flatMap(
		(credit, index, all) => {
			const entry = creditFor(
				credit.artistId,
				credit.credit || credit.name || null,
			);
			return entry
				? [
						{
							...entry,
							joinPhrase:
								credit.joinPhrase ?? (index < all.length - 1 ? ", " : ""),
							role: credit.role ?? null,
						},
					]
				: [];
		},
	);
	const fallbackCredit = creditFor(release.artistId);
	const credentials: ReleaseOverviewCredit[] = explicitCredits.length
		? explicitCredits
		: fallbackCredit
			? [fallbackCredit]
			: [];
	const mediaRecords = release.media ?? [];
	const singleKnownMedium = mediaRecords.length === 1;
	const media: ReleaseOverviewMedia[] = mediaRecords.map((medium, index) => {
		const number = index + 1;
		return {
			key: `${release.id}:medium:${number}`,
			number,
			title: medium.title,
			format: medium.format,
			tracks: trackRows.filter((track) => {
				const source = release.tracks.find((item) => item.id === track.id);
				if (source?.medium != null) return source.medium === number;
				// Local provider records a known single format without per-track
				// medium values; keep that known membership at projection.
				return singleKnownMedium && number === 1;
			}),
		};
	});
	const assignedTrackIds = new Set(
		media.flatMap((medium) => medium.tracks.map((track) => track.id)),
	);
	const fallbackTracks = media.length ? undefined : trackRows;
	const unassignedTracks = media.length
		? trackRows.filter((track) => !assignedTrackIds.has(track.id))
		: undefined;
	const durationTotal =
		release.tracks.length > 0 &&
		release.tracks.every((track) => Number.isFinite(track.duration))
			? release.tracks.reduce((sum, track) => sum + (track.duration ?? 0), 0)
			: null;
	const ariaLabel = `${release.title} · ${release.year} · ${release.catalog || "—"}`;
	const saveRelease = (updated: OriginalRelease) => {
		if (onUpdateRelease) onUpdateRelease(updated);
		else setLocalRelease(updated);
	};

	const overview: LibraryReleaseOverviewModel = {
		libraryHref: libraryRootHref(),
		artistHref: artist ? `#artist/${encodeURIComponent(artist.id)}` : undefined,
		heading: {
			title: release.title,
			disambiguation: release.disambiguation,
			year: release.year,
			country: release.country,
			label: release.label,
			catalog: release.catalog,
			providerText:
				release.provider === "Local"
					? "Local"
					: release.providerId
						? `${release.provider || "MusicBrainz"} · ${release.providerId}`
						: m("localRelease"),
			palette: release.color || artist?.color,
			artwork: {},
			counts: {
				positions: release.tracks.length,
				published: release.tracks.filter((track) => track.published).length,
				available: release.tracks.filter(hasAudio).length,
			},
			durationSeconds: durationTotal,
			selection: {
				isSelected: release.tracks.length > 0 && everySelected,
				isIndeterminate: someSelected && !everySelected,
				isDisabled: !release.tracks.length || !onSelect,
				onChange: (value) => onSelect?.(trackIds, value),
			},
		},
		credentials,
		media,
		fallbackTracks,
		unassignedTracks,
		metadataEditor: (
			<MetadataEditorFocusView focus={initialTagFocus}>
				<ReleaseMetadataEditor
					key={release.id}
					release={release}
					artists={artists}
					genresLoaded={genresLoaded}
					onLoadGenres={onLoadGenres}
					local={!onUpdateRelease}
					onSave={saveRelease}
				/>
			</MetadataEditorFocusView>
		),
		publication: publication ? (
			<ReleasePublicationDemo
				key={`publication-${release.id}`}
				release={release}
				provider={publication}
			/>
		) : undefined,
		deletion: onQueueDeletion ? (
			<DeletionRequest
				key={`deletion-${release.id}`}
				iconOnly
				triggerLabel={`${m("deleteAlbum")} ${ariaLabel}`}
				release={release}
				onQueue={onQueueDeletion}
			/>
		) : undefined,
		// Real-only cover and provider refresh capabilities stay unbound until
		// their controllers exist; do not invent current cover or source data.
	};
	return (
		<LibraryReleaseWorkspaceView
			locale={locale}
			selection={{ kind: "edition" }}
			overview={overview}
			positions={[]}
		/>
	);
}
