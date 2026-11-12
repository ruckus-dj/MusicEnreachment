import {
	resolveCatalogGenreName,
	resolveCatalogGenreValue,
} from "./genres/catalogue";
import type { Locale } from "./i18n/types";
import { projectArtistCreditGroup } from "./metadata/artistProjection";
import type { MetadataGenre, MetadataValues } from "./metadata/projection";
import {
	metadataManagedTagKeys,
	metadataTagKeys,
	metadataValidationMessage,
	projectMetadataValues,
	validateMetadataIssues,
	validateMetadataValues,
} from "./metadata/projection";
import type { MetadataScope } from "./metadata/registry";
import { fieldByKey, navidromeFields } from "./metadata/registry";
import original from "./original-fixtures.json";

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
	observedFile?: {
		mtime?: string;
		state?: string;
		rootAvailable?: boolean;
	} | null;
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
	mbid?: string;
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
	locations?: {
		id: string;
		rootId: string;
		path: string;
		size?: number;
		mtime?: string;
	}[];
	sha256?: string | null;
	path: string;
	codec: string;
	bits?: number | null;
	sampleRate?: number | null;
	bitrate?: number | null;
}
export const sourceOnlyAudioTagKeys = [
	"ENCODEDBY",
	"ENCODING",
	"ENCODERSETTINGS",
] as const;
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

import {
	choosePublicationOutput,
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";

export function applyMetadataPreview(
	releases: OriginalRelease[],
	selected: Set<string>,
	files: SourceFiles = originalFiles,
	artists: CatalogArtist[] = originalArtists,
	destination: PublicationDestination = fixturePublicationDestination,
): OriginalRelease[] {
	const next = structuredClone(releases);
	for (const release of next)
		for (const track of release.tracks) {
			if (!selected.has(track.id)) continue;
			if (publicationMetadataIssues(release, track).length) continue;
			const file = track.sourceId ? files[track.sourceId] : undefined;
			if (!track.published && !(file?.available && file.locations?.length))
				continue;
			if (track.published && !metadataChanged(release, track, artists))
				continue;
			const tags = effectivePublicationTags(release, track, artists, files);
			const coverHash = track.coverHash || null;
			const lyrics = track.lyrics || null;
			track.published = track.published
				? { ...track.published, tags, coverHash, lyrics }
				: {
						tags,
						coverHash,
						lyrics,
						codec: file?.codec ?? "",
						container: choosePublicationOutput(file!, {
							...destination,
							artist: originalArtistName(release.artistId, artists),
							year: release.year,
							release: release.title,
							position: track.position,
							title: track.title,
						}).container,
						sourceId: track.sourceId,
						path: choosePublicationOutput(file!, {
							...destination,
							artist: originalArtistName(release.artistId, artists),
							year: release.year,
							release: release.title,
							position: track.position,
							title: track.title,
						}).path,
						readAt: "Только в памяти макета",
					};
			track.metadataBaseline = structuredClone(tags);
			track.metadataBaselineExtras = { coverHash, lyrics };
		}
	return next;
}

function explicitGenreValues(
	values: MetadataValues | undefined,
): string[] | undefined {
	if (!values || !Object.hasOwn(values, "genre")) return undefined;
	const entries = values.genre;
	if (!Array.isArray(entries)) return undefined;
	if (!entries.some(Boolean)) return [];
	return entries.map((value) => resolveCatalogGenreValue(value));
}

/**
 * Resolves GENRE per scope before falling back to less specific scopes:
 * recording override, recording genres, release override, release genres,
 * then the ordered recording-artist union.
 */
export function publicationGenreFallback(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
): string[] {
	const recordingOverride = explicitGenreValues(track.recordingMetadata);
	if (recordingOverride !== undefined) return recordingOverride;
	const recordingGenres = track.recordingGenreRefs?.length
		? track.recordingGenreRefs
		: (track.recordingGenres ?? []).map((name) => ({ name }));
	if (recordingGenres.length)
		return recordingGenres.map((genre) => resolveCatalogGenreName(genre));
	const releaseOverride = explicitGenreValues(release.metadataValues);
	if (releaseOverride !== undefined) return releaseOverride;
	if (release.metadataGenres?.length)
		return release.metadataGenres.map((genre) =>
			resolveCatalogGenreName(genre),
		);
	const artist = artists.find((item) => item.id === track.artistId);
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
	const values: string[] = [];
	const seen = new Set<string>();
	for (const credit of recordingCredits) {
		const linkedArtist = artists.find((item) => item.id === credit.artistId);
		for (const genre of linkedArtist?.genres ?? []) {
			const name = resolveCatalogGenreName(genre);
			if (!name) continue;
			const identity = genre.id ? `id:${genre.id}` : `name:${name}`;
			if (seen.has(identity)) continue;
			seen.add(identity);
			values.push(name);
		}
	}
	return values;
}

export function desiredTags(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
	audioSourceId: string | null = track.sourceId,
): Record<string, string[]> {
	const artist = artists.find((item) => item.id === track.artistId);
	const albumArtist = artists.find((item) => item.id === release.artistId);
	const result: Record<string, string[]> = {
		TITLE: track.title ? [track.title] : [],
		ARTIST: artist?.name ? [artist.name] : [],
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
		GENRE: publicationGenreFallback(release, track, artists),
		MUSICBRAINZ_ARTISTID: [],
		MUSICBRAINZ_ALBUMID:
			release.manual ||
			release.provider === "Local" ||
			!release.providerId ||
			!musicBrainzUUID.test(release.providerId)
				? []
				: [release.providerId],
		MUSICBRAINZ_TRACKID:
			!track.recordingId || !musicBrainzUUID.test(track.recordingId)
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
	const recordingArtistProjection = projectArtistCreditGroup(
		recordingCredits,
		artists,
	);
	const releaseArtistProjection = projectArtistCreditGroup(
		releaseCredits,
		artists,
	);
	const sortNameForCredit = (artistId: string) => {
		const linkedArtist = artists.find((item) => item.id === artistId);
		return linkedArtist?.sortName || linkedArtist?.name || "";
	};
	const recordingSorts = recordingCredits.map((credit) =>
		sortNameForCredit(credit.artistId),
	);
	const releaseSorts = releaseCredits.map((credit) =>
		sortNameForCredit(credit.artistId),
	);
	const recordingSortCollision = hasDistinctArtistSortCollision(
		recordingCredits.map((credit, index) => ({
			artistId: credit.artistId,
			sortName: recordingSorts[index],
		})),
	);
	const releaseSortCollision = hasDistinctArtistSortCollision(
		releaseCredits.map((credit, index) => ({
			artistId: credit.artistId,
			sortName: releaseSorts[index],
		})),
	);
	result.ARTIST = recordingCredits.length
		? [
				recordingCredits
					.map(
						(credit, index) =>
							recordingArtistProjection.names[index] + credit.joinPhrase,
					)
					.join(""),
			]
		: [];
	result.ARTISTSORT =
		recordingSorts.length &&
		recordingSorts.every(Boolean) &&
		!recordingSortCollision
			? [
					recordingCredits
						.map(
							(credit, index) =>
								recordingSorts[index] + (credit.joinPhrase ?? ""),
						)
						.join(""),
				]
			: [];
	result.ARTISTSSORT = recordingSortCollision ? [] : recordingSorts;
	result.ARTISTS = recordingArtistProjection.names;
	result.MUSICBRAINZ_ARTISTID =
		recordingArtistProjection.missingMbid ||
		recordingArtistProjection.nameCollision
			? []
			: recordingArtistProjection.mbids;
	result.ALBUMARTIST = releaseCredits.length
		? [
				releaseCredits
					.map(
						(credit, index) =>
							releaseArtistProjection.names[index] + credit.joinPhrase,
					)
					.join(""),
			]
		: [];
	result.ALBUMARTISTS = releaseArtistProjection.names;
	result.MUSICBRAINZ_ALBUMARTISTID =
		releaseArtistProjection.missingMbid || releaseArtistProjection.nameCollision
			? []
			: releaseArtistProjection.mbids;
	result.ALBUMARTISTSORT =
		releaseSorts.length && releaseSorts.every(Boolean) && !releaseSortCollision
			? [
					releaseCredits
						.map((credit, index) => releaseSorts[index] + credit.joinPhrase)
						.join(""),
				]
			: [];
	result.ALBUMARTISTSSORT = releaseSortCollision ? [] : releaseSorts;
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
		...(audioSourceId
			? valuesForScope(track.audioMetadata?.[audioSourceId] ?? {}, "audio")
			: {}),
	};
	// Genre overrides are resolved per scope by publicationGenreFallback.
	delete explicitValues.genre;
	const projected = projectMetadataValues(explicitValues).tags;
	for (const [key, values] of Object.entries(projected)) result[key] = values;
	const projectedArtistNames = (
		credits: { creditName: string; joinPhrase?: string }[],
		names: string[],
	) => names.some((name, index) => name !== credits[index]?.creditName);
	if (projectedArtistNames(recordingCredits, recordingArtistProjection.names)) {
		if (projected.ARTIST?.length)
			result.ARTIST = [
				recordingCredits
					.map(
						(credit, index) =>
							recordingArtistProjection.names[index] + credit.joinPhrase,
					)
					.join(""),
			];
		if (projected.ARTISTS?.length)
			result.ARTISTS = recordingArtistProjection.names;
	}
	if (
		recordingArtistProjection.missingMbid ||
		recordingArtistProjection.nameCollision
	)
		result.MUSICBRAINZ_ARTISTID = [];
	if (projectedArtistNames(releaseCredits, releaseArtistProjection.names)) {
		if (projected.ALBUMARTIST?.length)
			result.ALBUMARTIST = [
				releaseCredits
					.map(
						(credit, index) =>
							releaseArtistProjection.names[index] + credit.joinPhrase,
					)
					.join(""),
			];
		if (projected.ALBUMARTISTS?.length)
			result.ALBUMARTISTS = releaseArtistProjection.names;
	}
	if (
		releaseArtistProjection.missingMbid ||
		releaseArtistProjection.nameCollision
	)
		result.MUSICBRAINZ_ALBUMARTISTID = [];
	// Resolve catalog identities without recasing unrelated legacy/user values.
	const genreReferences = track.recordingGenreRefs ?? [];
	result.GENRE = (result.GENRE ?? []).map((value, index) => {
		const reference = genreReferences[index];
		return reference && reference.name === value
			? resolveCatalogGenreName(reference)
			: resolveCatalogGenreValue(value);
	});
	// The recording title cannot overwrite a release position's explicit title.
	result.TITLE = track.title ? [track.title] : [];
	return result;
}

function hasDistinctArtistSortCollision(
	credits: { artistId: string; sortName: string }[],
): boolean {
	const artistBySortName = new Map<string, string>();
	for (const { artistId, sortName } of credits) {
		if (!artistId || !sortName) continue;
		const previousArtistId = artistBySortName.get(sortName);
		if (previousArtistId && previousArtistId !== artistId) return true;
		artistBySortName.set(sortName, artistId);
	}
	return false;
}

export function publicationSortTagCollisions(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
): { recording: boolean; release: boolean } {
	const linkedSortName = (artistId: string) => {
		const artist = artists.find((item) => item.id === artistId);
		return artist?.sortName || artist?.name || "";
	};
	const recordingCredits =
		track.recordingCredits ??
		(track.artistId
			? [{ artistId: track.artistId, creditName: "", joinPhrase: "" }]
			: []);
	const releaseCredits = release.credits?.length
		? release.credits
				.map((credit) => ({
					artistId: credit.artistId ?? "",
					creditName: credit.credit || credit.name || "",
				}))
				.filter((credit) => credit.creditName)
		: release.artistId
			? [{ artistId: release.artistId }]
			: [];
	const collides = (credits: { artistId: string }[]) =>
		hasDistinctArtistSortCollision(
			credits.map(({ artistId }) => ({
				artistId,
				sortName: linkedSortName(artistId),
			})),
		);
	return {
		recording: collides(recordingCredits),
		release: collides(releaseCredits),
	};
}

/** Metadata-only updates keep audio-scoped tags attached to the published audio. */
export function effectivePublicationTags(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
	files: SourceFiles = originalFiles,
): Record<string, string[]> {
	const tags = desiredTags(
		release,
		track,
		artists,
		track.published?.sourceId ?? track.sourceId,
	);
	if (!track.published) {
		const sourceFile = track.sourceId ? files[track.sourceId] : undefined;
		if (sourceFile) {
			for (const key of sourceOnlyAudioTagKeys) {
				if (Object.hasOwn(sourceFile.tags, key))
					tags[key] = [...sourceFile.tags[key]];
			}
		}
		return tags;
	}
	for (const key of sourceOnlyAudioTagKeys) {
		delete tags[key];
		if (Object.hasOwn(track.published.tags, key))
			tags[key] = [...track.published.tags[key]];
	}
	for (const field of navidromeFields.filter(
		(item) => item.scope === "audio",
	)) {
		delete tags[field.tag];
		if (Object.hasOwn(track.published.tags, field.tag))
			tags[field.tag] = [...track.published.tags[field.tag]];
	}
	return tags;
}

/** Release genre is a recording-scope override; other fields keep their registered scope. */
function scopeValidationEntries(
	values: MetadataValues,
	scope: MetadataScope,
): [MetadataValues, MetadataScope][] {
	if (scope !== "release" || !Object.hasOwn(values, "genre"))
		return [[values, scope]];
	const { genre, ...releaseValues } = values;
	return [
		[releaseValues, scope],
		[{ genre }, "recording"],
	];
}

export function publicationMetadataIssues(
	release: OriginalRelease,
	track: OriginalTrack,
	audioSourceId: string | null = track.published ? null : track.sourceId,
): string[] {
	const scopes = [
		[release.metadataValues ?? {}, "release"],
		[track.recordingMetadata ?? {}, "recording"],
		[track.positionMetadata ?? {}, "track"],
		[
			audioSourceId ? (track.audioMetadata?.[audioSourceId] ?? {}) : {},
			"audio",
		],
	] as const;
	return [
		...new Set(
			scopes.flatMap(([values, scope]) =>
				scopeValidationEntries(values, scope).flatMap(
					([scopedValues, scopedScope]) =>
						validateMetadataValues(scopedValues, scopedScope),
				),
			),
		),
	];
}

/** Locale-reactive presentation of publication metadata validation issues. */
export function publicationMetadataIssueMessages(
	release: OriginalRelease,
	track: OriginalTrack,
	locale: Locale,
	audioSourceId: string | null = track.published ? null : track.sourceId,
): string[] {
	const scopes = [
		[release.metadataValues ?? {}, "release"],
		[track.recordingMetadata ?? {}, "recording"],
		[track.positionMetadata ?? {}, "track"],
		[
			audioSourceId ? (track.audioMetadata?.[audioSourceId] ?? {}) : {},
			"audio",
		],
	] as const;
	return [
		...new Set(
			scopes.flatMap(([values, scope]) =>
				scopeValidationEntries(values, scope).flatMap(
					([scopedValues, scopedScope]) =>
						validateMetadataIssues(scopedValues, scopedScope).map((issue) =>
							metadataValidationMessage(issue, locale),
						),
				),
			),
		),
	];
}

export function metadataChanged(
	release: OriginalRelease,
	track: OriginalTrack,
	artists: CatalogArtist[] = originalArtists,
): boolean {
	if (!track.published) return false;
	const tags = effectivePublicationTags(release, track, artists);
	const baseline = track.metadataBaseline ?? track.published.tags;
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
			...((track.published?.sourceId ?? track.sourceId)
				? (track.audioMetadata?.[
						(track.published?.sourceId ?? track.sourceId) as string
					] ?? {})
				: {}),
		}),
	]);
	return (
		[...managed].some(
			(key) =>
				JSON.stringify(tags[key] ?? []) !== JSON.stringify(baseline[key] ?? []),
		) ||
		(track.coverHash ?? null) !==
			((track.metadataBaselineExtras
				? track.metadataBaselineExtras.coverHash
				: track.published.coverHash) ?? null) ||
		(track.lyrics ?? null) !==
			((track.metadataBaselineExtras
				? track.metadataBaselineExtras.lyrics
				: track.published.lyrics) ?? null)
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
