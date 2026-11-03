import original from "./original-fixtures.json";
import type { MetadataGenre, MetadataValues } from "./metadata/projection";
import { fieldByKey } from "./metadata/registry";
import {
	metadataManagedTagKeys,
	metadataTagKeys,
	projectMetadataValues,
} from "./metadata/projection";

const musicBrainzUUID =
	/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export interface OriginalTrack {
	id: string;
	title: string;
	position: number;
	duration: number | null;
	artistId: string | null;
	recordingId?: string | null;
	providerTrackId?: string | null;
	recordingTitle?: string;
	localRecordingId?: string | null;
	providerTitle?: string;
	providerCacheVersion?: number;
	recovered?: boolean;
	observedFile?: { mtime?: string; state?: string } | null;
	sourceId: string | null;
	sourceIds?: string[];
	published: null | {
		tags: Record<string, string[]>;
		codec: string;
		container: string;
		sourceId: string | null;
		path: string;
		readAt: string;
		coverHash: string | null;
		lyrics: string | null;
	};
	metadataBaseline?: Record<string, string[]>;
	metadataBaselineExtras?: { coverHash: string | null; lyrics: string | null };
	coverHash?: string | null;
	lyrics?: string | null;
	recordingGenres?: string[];
	recordingGenreRefs?: MetadataGenre[];
	recordingMetadata?: MetadataValues;
	positionMetadata?: MetadataValues;
	audioMetadata?: Record<string, MetadataValues>;
	recordingCredits?: import("./metadata/model").MetadataCredit[];
	displayedNumber?: string;
	mediumPosition?: number;
	medium?: number;
	positionTitleOverride?: string;
}

export interface OriginalRelease {
	id: string;
	releaseGroupId?: string;
	title: string;
	artistId: string | null;
	year: string;
	country: string;
	catalog: string;
	label: string;
	providerId: string | null;
	provider?: string;
	manual?: boolean;
	credits?: {
		credit?: string;
		name?: string;
		role?: string;
		artistId?: string | null;
		joinPhrase?: string;
	}[];
	metadataValues?: MetadataValues;
	metadataGenres?: MetadataGenre[];
	metadata?: {
		date: string;
		country: string;
		label: string;
		catalogNumber: string;
		type: string;
	};
	media?: { title: string; format: string; positions: unknown[] }[];
	coverId?: string;
	color?: string;
	tracks: OriginalTrack[];
}

export function trackNumberLabel(
	release: Pick<OriginalRelease, "media"> & {
		tracks: Pick<OriginalTrack, "medium">[];
	},
	track: Pick<
		OriginalTrack,
		"displayedNumber" | "medium" | "mediumPosition" | "position"
	>,
): string {
	if (track.displayedNumber) return track.displayedNumber;
	const multiple =
		(release.media?.length ?? 0) > 1 ||
		new Set(release.tracks.map((item) => item.medium).filter(Boolean)).size > 1;
	return multiple && track.mediumPosition
		? `${track.medium}.${String(track.mediumPosition).padStart(2, "0")}`
		: String(track.mediumPosition || track.position).padStart(2, "0");
}

export const originalReleases: OriginalRelease[] = original.releases;
export const recoveryFixture = original.recoveryFixture;
export type CatalogArtist = {
	id: string;
	name: string;
	sortName?: string;
	disambiguation?: string;
	genres?: { id?: string; name?: string }[];
	/** Legacy display value for fixtures that predate canonical genre references. */
	genre?: string;
	/** Optional demo-only artist tags, edited as comma-separated values. */
	tags?: string[];
	color?: string;
};
// Explicit demo data matching the existing Japan-edition publication fixture.
// This is not a display-name fallback or a metadata resolution policy.
const demoSortNames: Partial<Record<string, string>> = { mira: "Mira Vale" };
export const originalArtists: CatalogArtist[] = original.artists.map(
	(artist) => ({
		...artist,
		sortName: demoSortNames[artist.id],
	}),
);
export const originalArtistName = (
	id: string | null,
	artists: CatalogArtist[] = originalArtists,
) => artists.find((artist) => artist.id === id)?.name ?? "Артист не указан";
export interface SourceFile {
	id: string;
	name: string;
	duration: number;
	size: string;
	tags: Record<string, string[]>;
	available: boolean;
	locations?: { id: string; rootId: string; path: string }[];
	sha256?: string | null;
	path: string;
	codec: string;
}
export type SourceFiles = Record<string, SourceFile>;
export const originalFiles: SourceFiles = original.files;
export const originalProviders: OriginalRelease[] =
	original.providerReleases.map((release) => ({
		...release,
		tracks: release.tracks.map((track) => ({
			...track,
			sourceId: track.sourceId ?? null,
			published: track.published ?? null,
		})),
	}));

export function applyMetadataPreview(
	releases: OriginalRelease[],
	selected: Set<string>,
	files: SourceFiles = originalFiles,
	artists: CatalogArtist[] = originalArtists,
): OriginalRelease[] {
	const next = structuredClone(releases);
	for (const release of next)
		for (const track of release.tracks) {
			if (!selected.has(track.id)) continue;
			const file = track.sourceId ? files[track.sourceId] : undefined;
			if (!track.published && !(file?.available && file.locations?.length))
				continue;
			if (track.published && !metadataChanged(release, track, artists))
				continue;
			const tags = desiredTags(release, track, artists);
			const coverHash = track.coverHash || null;
			const lyrics = track.lyrics || null;
			track.published = track.published
				? { ...track.published, tags, coverHash, lyrics }
				: {
						tags,
						coverHash,
						lyrics,
						codec: file?.codec ?? "",
						container: "MKA",
						sourceId: track.sourceId,
						path: `/srv/music/${originalArtistName(release.artistId, artists)}/${release.year} - ${release.title}/${String(track.position).padStart(2, "0")} - ${track.title}.mka`,
						readAt: "Только в памяти макета",
					};
			track.metadataBaseline = structuredClone(tags);
			track.metadataBaselineExtras = { coverHash, lyrics };
		}
	return next;
}

export function desiredTags(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
): Record<string, string[]> {
	const artist = artists.find((item) => item.id === track.artistId);
	const albumArtist = artists.find((item) => item.id === release.artistId);
	const result: Record<string, string[]> = {
		TITLE: track.title ? [track.title] : [],
		ARTIST: artist?.name ? [artist.name] : [],
		// Single-artist demo: use only the explicit artist sort name, without
		// contextual resolution or a display-name fallback.
		ARTISTSORT: artist?.sortName ? [artist.sortName] : [],
		ALBUM: release.title ? [release.title] : [],
		ALBUMARTIST: release.credits?.length
			? release.credits
					.map((credit) => credit.credit || credit.name || "")
					.filter(Boolean)
			: albumArtist
				? [albumArtist.name]
				: [],
		DATE: [],
		RELEASEDATE: [release.metadata?.date || release.year],
		COUNTRY: release.country ? [release.country] : [],
		LABEL: release.label ? [release.label] : [],
		CATALOGNUMBER: release.catalog ? [release.catalog] : [],
		TRACKNUMBER: [
			String(track.displayedNumber || track.mediumPosition || track.position),
		],
		DISCNUMBER: [String(track.medium ?? 1)],
		GENRE: track.recordingGenres ?? [],
		MUSICBRAINZ_ARTISTID: [],
		MUSICBRAINZ_ALBUMID:
			release.manual ||
			release.provider === "Local" ||
			!release.providerId ||
			!musicBrainzUUID.test(release.providerId)
				? []
				: [release.providerId],
		MUSICBRAINZ_TRACKID:
			release.manual ||
			release.provider === "Local" ||
			!track.recordingId ||
			!musicBrainzUUID.test(track.recordingId)
				? []
				: [track.recordingId],
		COMMENT: [],
	};
	const recordingCredits =
		track.recordingCredits ??
		(artist
			? [
					{
						artistId: artist.id,
						creditName: artist.name,
						joinPhrase: "",
					},
				]
			: []);
	const releaseCredits = release.credits?.length
		? release.credits
				.map((credit) => ({
					artistId: credit.artistId ?? "",
					creditName: credit.credit || credit.name || "",
					joinPhrase: credit.joinPhrase ?? "",
				}))
				.filter((credit) => credit.creditName)
		: albumArtist
			? [
					{
						artistId: albumArtist.id,
						creditName: albumArtist.name,
						joinPhrase: "",
					},
				]
			: [];
	const recordingSorts = recordingCredits.map(
		(credit) =>
			artists.find((item) => item.id === credit.artistId)?.sortName ?? "",
	);
	const releaseSorts = releaseCredits.map(
		(credit) =>
			artists.find((item) => item.id === credit.artistId)?.sortName ?? "",
	);
	result.ARTIST = recordingCredits.length
		? [
				recordingCredits
					.map((credit) => credit.creditName + credit.joinPhrase)
					.join(""),
			]
		: [];
	result.ARTISTSORT =
		recordingSorts.length && recordingSorts.every(Boolean)
			? [
					recordingCredits
						.map(
							(credit, index) =>
								recordingSorts[index] + (credit.joinPhrase ?? ""),
						)
						.join(""),
				]
			: [];
	result.ARTISTSSORT = recordingSorts;
	result.ARTISTS = recordingCredits.map((credit) => credit.creditName);
	result.ALBUMARTIST = releaseCredits.length
		? [
				releaseCredits
					.map((credit) => credit.creditName + credit.joinPhrase)
					.join(""),
			]
		: [];
	result.ALBUMARTISTS = releaseCredits.map((credit) => credit.creditName);
	result.ALBUMARTISTSORT =
		releaseSorts.length && releaseSorts.every(Boolean)
			? [
					releaseCredits
						.map((credit, index) => releaseSorts[index] + credit.joinPhrase)
						.join(""),
				]
			: [];
	result.ALBUMARTISTSSORT = releaseSorts;
	const valuesForScope = (values: MetadataValues, scope: string) =>
		Object.fromEntries(
			Object.entries(values).filter(
				([key]) => fieldByKey(key)?.scope === scope,
			),
		) as MetadataValues;
	const explicitValues: MetadataValues = {
		...valuesForScope(release.metadataValues ?? {}, "release"),
		...valuesForScope(track.recordingMetadata ?? {}, "recording"),
		...valuesForScope(track.positionMetadata ?? {}, "track"),
		...(track.sourceId
			? valuesForScope(track.audioMetadata?.[track.sourceId] ?? {}, "audio")
			: {}),
	};
	const projected = projectMetadataValues(explicitValues).tags;
	for (const [key, values] of Object.entries(projected)) result[key] = values;
	// The recording title cannot overwrite a release position's explicit title.
	result.TITLE = track.title ? [track.title] : [];
	return result;
}

export function metadataChanged(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
): boolean {
	if (!track.published || !track.metadataBaseline) return false;
	const tags = desiredTags(release, track, artists);
	const managed = new Set([
		"TITLE",
		"ARTIST",
		"ARTISTSORT",
		"ARTISTSSORT",
		"ALBUM",
		"ALBUMARTIST",
		"ALBUMARTISTSORT",
		"ALBUMARTISTSSORT",
		"DATE",
		"COUNTRY",
		"LABEL",
		"CATALOGNUMBER",
		"TRACKNUMBER",
		"DISCNUMBER",
		"GENRE",
		...metadataManagedTagKeys(),
		...metadataTagKeys({
			...(release.metadataValues ?? {}),
			...(track.recordingMetadata ?? {}),
			...(track.positionMetadata ?? {}),
			...(track.sourceId ? (track.audioMetadata?.[track.sourceId] ?? {}) : {}),
		}),
	]);
	return (
		[...managed].some(
			(key) =>
				JSON.stringify(tags[key] ?? []) !==
				JSON.stringify(track.metadataBaseline?.[key] ?? []),
		) ||
		(track.coverHash || null) !==
			(track.metadataBaselineExtras?.coverHash || null) ||
		(track.lyrics || null) !== (track.metadataBaselineExtras?.lyrics || null)
	);
}
export const catalogData = {
	artists: originalArtists,
	absentGroups: original.absentGroups,
	releases: originalReleases.map((release) => ({
		...release,
		tracks: release.tracks.map((track) => ({
			...track,
			sourceIds: track.sourceIds ?? [],
			published: Boolean(track.published),
		})),
	})),
	files: Object.fromEntries(
		Object.entries(original.files).map(([id, file]) => [
			id,
			{
				available: Boolean(file.available),
				located: Boolean(file.locations?.length),
			},
		]),
	),
};
