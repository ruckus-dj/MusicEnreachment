import { type ReactNode, useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import { catalogueMessage } from "../../../ui/features/library/catalogueMessages";
import type { LibraryReleaseOverviewModel } from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import {
	libraryArtistAddressHref,
	libraryPositionHref,
} from "../../../ui/features/library/libraryDetailRoute";
import type {
	ReleaseOverviewCredit,
	ReleaseOverviewMedia,
} from "../../../ui/features/library/ReleaseOverviewView";
import type { Locale } from "../../../ui/i18n/types";
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
import {
	type DemoReleaseCoverEditorModel,
	type DemoReleaseCoverState,
	useReleaseCoverEditorDemoModel,
} from "./useReleaseCoverEditorDemoModel";

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
	/**
	 * Edition-only aggregate selection/publication and deletion. A host that
	 * retains this overview behind an active position passes `false`;
	 * the metadata editor stays mounted so its draft survives same-release
	 * navigation.
	 * @default true
	 */
	editionControls?: boolean;
	/** Saved demo cover Files keyed by the host; omitted for local state. */
	coverState?: DemoReleaseCoverState;
	/** Records a locally saved cover state without touching release data. */
	onCoverStateChange?: (state: DemoReleaseCoverState) => void;
};

/** Resolved facts and narrow actors for one release overview projection. */
export type ReleaseOverviewDemoModelInput = {
	locale: Locale;
	release: OriginalRelease;
	artists: CatalogArtist[];
	files: SourceFiles;
	selected: Set<string>;
	onSelect?: (ids: string[], value: boolean) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	publication?: ReleasePublicationDemoProvider;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	saveRelease: (release: OriginalRelease) => void;
	/** Uncontrolled local release: the demo owns the saved record. */
	local: boolean;
	editionControls: boolean;
	/** Browser-only cover controller and its selected preview. */
	coverEditor?: DemoReleaseCoverEditorModel;
};

const sourceIds = (track: OriginalTrack) => [
	...new Set([
		...(track.sourceIds ?? []),
		...(track.sourceId ? [track.sourceId] : []),
	]),
];

/**
 * Project one release into the retained overview model. This stays free of
 * React state so the host can reuse the same overview composition while a
 * position route owns the workspace selection.
 */
export function buildReleaseOverviewDemoModel({
	locale,
	release,
	artists,
	files,
	selected,
	onSelect,
	onQueueDeletion,
	publication,
	genresLoaded,
	onLoadGenres,
	saveRelease,
	local,
	editionControls,
	coverEditor,
}: ReleaseOverviewDemoModelInput): LibraryReleaseOverviewModel {
	const m = (
		key: Parameters<typeof catalogueMessage>[1],
		values?: Record<string, string | number>,
	) => catalogueMessage(locale, key, values);
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
		href: libraryPositionHref(release.id, track.id),
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
			...(resolved ? { href: libraryArtistAddressHref(resolved.id) } : {}),
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

	return {
		libraryHref: libraryRootHref(),
		artistHref: artist ? libraryArtistAddressHref(artist.id) : undefined,
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
			artwork: coverEditor?.selectedImageSrc
				? {
						imageSrc: coverEditor.selectedImageSrc,
						imageAlt: coverEditor.selectedImageAlt,
					}
				: {},
			counts: {
				positions: release.tracks.length,
				published: release.tracks.filter((track) => track.published).length,
				available: release.tracks.filter(hasAudio).length,
			},
			durationSeconds: durationTotal,
			selection: editionControls
				? {
						isSelected: release.tracks.length > 0 && everySelected,
						isIndeterminate: someSelected && !everySelected,
						isDisabled: !release.tracks.length || !onSelect,
						onChange: (value) => onSelect?.(trackIds, value),
					}
				: undefined,
		},
		credentials,
		media,
		fallbackTracks,
		unassignedTracks,
		metadataEditor: {
			bind: (children: ReactNode, visible: boolean) => (
				<ReleaseMetadataEditor
					key={release.id}
					release={release}
					artists={artists}
					genresLoaded={genresLoaded}
					onLoadGenres={onLoadGenres}
					local={local}
					visible={visible}
					onSave={saveRelease}
				>
					{children}
				</ReleaseMetadataEditor>
			),
		},
		publication:
			editionControls && publication ? (
				<ReleasePublicationDemo
					key={`publication-${release.id}`}
					release={release}
					provider={publication}
				/>
			) : undefined,
		deletion:
			editionControls && onQueueDeletion ? (
				<DeletionRequest
					key={`deletion-${release.id}`}
					iconOnly
					triggerLabel={`${m("deleteAlbum")} ${ariaLabel}`}
					release={release}
					onQueue={onQueueDeletion}
				/>
			) : undefined,
		// The demo cover editor binds only browser File state; provider refresh
		// remains an unbound real-only capability.
		cover: coverEditor?.source,
	};
}

/**
 * Release overview demo provider. The host mounts one instance per release id
 * and keeps it mounted while that release's positions are selected, so the
 * metadata actor owns one draft across same-release navigation. Edition-only
 * aggregate publication and deletion disappear for a retained overview; the
 * metadata editor itself stays mounted.
 */
export function useReleaseOverviewDemoModel({
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
	editionControls = true,
	coverState,
	onCoverStateChange,
}: ReleaseOverviewScreenProps): LibraryReleaseOverviewModel {
	const { locale } = useI18n();
	const [localRelease, setLocalRelease] = useState(givenRelease);
	const release = onUpdateRelease ? givenRelease : localRelease;
	const coverEditor = useReleaseCoverEditorDemoModel({
		releaseId: release.id,
		state: coverState,
		onChange: onCoverStateChange,
	});
	const saveRelease = (updated: OriginalRelease) => {
		if (onUpdateRelease) onUpdateRelease(updated);
		else setLocalRelease(updated);
	};

	return buildReleaseOverviewDemoModel({
		locale,
		release,
		artists,
		files,
		selected,
		onSelect,
		onQueueDeletion,
		publication,
		genresLoaded,
		onLoadGenres,
		saveRelease,
		local: !onUpdateRelease,
		editionControls,
		coverEditor,
	});
}
