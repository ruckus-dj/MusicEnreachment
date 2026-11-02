import original from "./original-fixtures.json";

export interface OriginalTrack {
	id: string;
	title: string;
	position: number;
	duration: number;
	artistId: string;
	recordingId?: string | null;
	providerTrackId?: string | null;
	recordingTitle?: string;
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
	displayedNumber?: string;
	mediumPosition?: number;
	medium?: number;
}

export interface OriginalRelease {
	id: string;
	title: string;
	artistId: string;
	year: string;
	country: string;
	catalog: string;
	label: string;
	providerId: string | null;
	manual?: boolean;
	credits?: { credit?: string; name?: string }[];
	tracks: OriginalTrack[];
}

export const originalReleases: OriginalRelease[] = original.releases;
export const recoveryFixture = original.recoveryFixture;
export const originalArtistName = (id: string) =>
	original.artists.find((artist) => artist.id === id)?.name ??
	"Артист не указан";
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
): OriginalRelease[] {
	const next = structuredClone(releases);
	for (const release of next)
		for (const track of release.tracks) {
			if (!selected.has(track.id)) continue;
			const file = track.sourceId ? files[track.sourceId] : undefined;
			if (!track.published && !(file?.available && file.locations?.length))
				continue;
			if (track.published && !metadataChanged(release, track)) continue;
			const tags = desiredTags(release, track);
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
						path: `/srv/music/${originalArtistName(release.artistId)}/${release.year} - ${release.title}/${String(track.position).padStart(2, "0")} - ${track.title}.mka`,
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
): Record<string, string[]> {
	const artist = original.artists.find((item) => item.id === track.artistId);
	const albumArtist = original.artists.find(
		(item) => item.id === release.artistId,
	);
	return {
		TITLE: track.title ? [track.title] : [],
		ARTIST: artist?.name ? [artist.name] : [],
		ARTISTSORT: artist?.name ? [artist.name] : [],
		ALBUM: release.title ? [release.title] : [],
		ALBUMARTIST: release.credits?.length
			? release.credits
					.map((credit) => credit.credit || credit.name || "")
					.filter(Boolean)
			: albumArtist
				? [albumArtist.name]
				: [],
		DATE: [release.year],
		COUNTRY: release.country ? [release.country] : [],
		LABEL: release.label ? [release.label] : [],
		CATALOGNUMBER: release.catalog ? [release.catalog] : [],
		TRACKNUMBER: [
			String(track.displayedNumber || track.mediumPosition || track.position),
		],
		DISCNUMBER: [String(track.medium ?? 1)],
		GENRE: track.recordingGenres ?? [],
		MUSICBRAINZ_ARTISTID: release.manual
			? []
			: artist
				? [`demo-artist-${artist.id}`]
				: [],
		MUSICBRAINZ_ALBUMID: release.providerId ? [release.providerId] : [],
		MUSICBRAINZ_TRACKID: release.manual
			? []
			: track.recordingId
				? [track.recordingId]
				: [],
		COMMENT: [],
	};
}

const normalized = (value: string) =>
	value.normalize("NFKC").toLocaleLowerCase().replace(/\s+/g, " ").trim();
export function metadataChanged(
	release: OriginalRelease,
	track: OriginalTrack,
): boolean {
	if (!track.published || !track.metadataBaseline) return false;
	const tags = desiredTags(release, track);
	return (
		[
			"TITLE",
			"ARTIST",
			"ALBUM",
			"ALBUMARTIST",
			"DATE",
			"COUNTRY",
			"LABEL",
			"CATALOGNUMBER",
			"TRACKNUMBER",
			"DISCNUMBER",
			"GENRE",
		].some(
			(key) =>
				normalized(tags[key].join("\0")) !==
				normalized((track.metadataBaseline?.[key] ?? []).join("\0")),
		) ||
		(track.coverHash || null) !==
			(track.metadataBaselineExtras?.coverHash || null) ||
		(track.lyrics || null) !== (track.metadataBaselineExtras?.lyrics || null)
	);
}
export const catalogData = {
	artists: original.artists,
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
