import { expect, it } from "vitest";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "../catalogData";
import { desiredTags, effectivePublicationTags } from "../catalogData";
import { publicationMetadataDiff } from "../publicationMetadataDiff";
import { projectArtistCreditGroup } from "./artistProjection";

const artists: CatalogArtist[] = [
	{ id: "a", name: "Shared", mbid: "11111111-1111-4111-8111-111111111111" },
	{
		id: "b",
		name: "Shared",
		disambiguation: "Boston",
		mbid: "22222222-2222-4222-8222-222222222222",
	},
	{ id: "c", name: "Third", mbid: "33333333-3333-4333-8333-333333333333" },
	{ id: "missing", name: "Missing" },
];

const release: OriginalRelease = {
	id: "release",
	title: "Album",
	artistId: "a",
	year: "2020",
	country: "",
	catalog: "",
	label: "",
	providerId: "44444444-4444-4444-8444-444444444444",
	provider: "MusicBrainz",
	tracks: [],
};
const track: OriginalTrack = {
	id: "track",
	title: "Track",
	position: 1,
	duration: null,
	artistId: "a",
	recordingId: "55555555-5555-4555-8555-555555555555",
	sourceId: null,
	published: null,
};

it("projects complete MBID lists and keeps release and recording identifiers", () => {
	const credits = ["a", "b"].map((artistId) => ({
		artistId,
		creditName: "Shared",
	}));
	const projected = projectArtistCreditGroup(credits, artists);
	expect(projected.mbids).toEqual([artists[0].mbid, artists[1].mbid]);
	expect(projected.missingMbid).toBe(false);
	const tags = desiredTags(
		{
			...release,
			credits: credits.map((credit) => ({
				artistId: credit.artistId,
				credit: credit.creditName,
			})),
		},
		{
			...track,
			recordingCredits: credits.map((credit) => ({
				...credit,
				joinPhrase: "",
			})),
		},
		artists,
	);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual(projected.mbids);
	expect(tags.MUSICBRAINZ_ALBUMARTISTID).toEqual(projected.mbids);
	expect(tags.MUSICBRAINZ_ALBUMID).toEqual([release.providerId]);
	expect(tags.MUSICBRAINZ_TRACKID).toEqual([track.recordingId]);
});

it.each([
	0, 1, 2,
])("omits the entire MBID group when member %i lacks its MBID", (missingIndex) => {
	const ids = ["a", "b", "c"];
	const withoutMbid = artists.map((artist) => ({ ...artist }));
	const missing = withoutMbid.find((artist) => artist.id === ids[missingIndex]);
	if (missing) delete missing.mbid;
	const credits = ids.map((artistId) => ({ artistId, creditName: artistId }));
	expect(projectArtistCreditGroup(credits, withoutMbid).mbids).toEqual(
		ids.map((id) => withoutMbid.find((artist) => artist.id === id)?.mbid ?? ""),
	);
	expect(projectArtistCreditGroup(credits, withoutMbid).missingMbid).toBe(true);
	const tags = desiredTags(
		{
			...release,
			credits: credits.map(({ artistId, creditName }) => ({
				artistId,
				credit: creditName,
			})),
		},
		{
			...track,
			recordingCredits: credits.map((credit) => ({
				...credit,
				joinPhrase: "",
			})),
		},
		withoutMbid,
	);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual([]);
	expect(tags.MUSICBRAINZ_ALBUMARTISTID).toEqual([]);
	expect(tags.MUSICBRAINZ_ALBUMID).toEqual([release.providerId]);
	expect(tags.MUSICBRAINZ_TRACKID).toEqual([track.recordingId]);
});

it("qualifies duplicate display names without mutating stored artist records", () => {
	const source = structuredClone(artists.slice(0, 2));
	const credits = source.map((artist) => ({
		artistId: artist.id,
		creditName: artist.name,
	}));
	const projected = projectArtistCreditGroup(credits, source);
	expect(projected.names).toEqual(["Shared", "Shared (Boston)"]);
	expect(projected.nameCollision).toBe(false);
	expect(source).toEqual(artists.slice(0, 2));
});

it("reports a residual duplicate-name collision and treats repeated same identities as one artist", () => {
	const sameIdentity = projectArtistCreditGroup(
		[
			{ artistId: "a", creditName: "Shared" },
			{ artistId: "a", creditName: "Shared" },
		],
		artists,
	);
	expect(sameIdentity.nameCollision).toBe(false);
	const unresolved = projectArtistCreditGroup(
		[
			{ artistId: "a", creditName: "Shared" },
			{ artistId: "b", creditName: "Shared" },
		],
		artists.map(({ disambiguation: _disambiguation, ...artist }) => artist),
	);
	expect(unresolved.nameCollision).toBe(true);
	expect(unresolved.mbids).toHaveLength(2);
});

it("detects collisions from final projected names", () => {
	const credits = [
		{ artistId: "a", creditName: "Shared" },
		{ artistId: "b", creditName: "Shared" },
	];
	const sameDisambiguation = artists.slice(0, 2).map((artist) => ({
		...artist,
		disambiguation: "Band",
	}));
	const residual = projectArtistCreditGroup(credits, sameDisambiguation);
	expect(residual.names).toEqual(["Shared (Band)", "Shared (Band)"]);
	expect(residual.nameCollision).toBe(true);

	const oneSided = projectArtistCreditGroup(credits, [
		{ ...artists[0], disambiguation: "Band" },
		{ ...artists[1], disambiguation: undefined },
	]);
	expect(oneSided.names).toEqual(["Shared (Band)", "Shared"]);
	expect(oneSided.nameCollision).toBe(false);

	const aliasCollision = projectArtistCreditGroup(
		[
			{ artistId: "a", creditName: "Guest" },
			{ artistId: "c", creditName: "Guest" },
		],
		artists,
	);
	expect(aliasCollision.nameCollision).toBe(true);
});

it("keeps album and recording artist MBID groups independent", () => {
	const tags = desiredTags(
		{
			...release,
			credits: [
				{ artistId: "a", name: "Shared" },
				{ artistId: "missing", name: "Missing" },
			],
		},
		{
			...track,
			recordingCredits: [
				{ artistId: "a", creditName: "Shared", joinPhrase: "" },
				{ artistId: "b", creditName: "Shared", joinPhrase: "" },
			],
		},
		artists,
	);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual([artists[0].mbid, artists[1].mbid]);
	expect(tags.MUSICBRAINZ_ALBUMARTISTID).toEqual([]);
	const preview = effectivePublicationTags(
		{
			...release,
			credits: [
				{ artistId: "a", name: "Shared" },
				{ artistId: "missing", name: "Missing" },
			],
		},
		{
			...track,
			recordingCredits: [
				{ artistId: "a", creditName: "Shared", joinPhrase: "" },
				{ artistId: "b", creditName: "Shared", joinPhrase: "" },
			],
			published: {
				tags: {
					MUSICBRAINZ_ALBUMARTISTID: [artists[0].mbid!],
					MUSICBRAINZ_ARTISTID: [],
				},
				codec: "FLAC",
				container: "FLAC",
				sourceId: null,
				path: "track.flac",
				readAt: "fixture",
				coverHash: null,
				lyrics: null,
			},
		},
		artists,
	);
	expect(preview.MUSICBRAINZ_ALBUMARTISTID).toEqual([]);
});

it("preserves a valid recording MBID for a local release", () => {
	const tags = desiredTags(
		{ ...release, manual: true, provider: "Local", providerId: null },
		track,
		artists,
	);
	expect(tags.MUSICBRAINZ_ALBUMID).toEqual([]);
	expect(tags.MUSICBRAINZ_TRACKID).toEqual([track.recordingId]);
});

it("keeps artist-group MBID omission and collision qualification authoritative over stale metadata", () => {
	const incompleteArtists = artists.map((artist) => ({ ...artist }));
	delete incompleteArtists[1].mbid;
	const oldIds = [artists[0].mbid!, artists[1].mbid!];
	const releaseWithCredits = {
		...release,
		credits: [
			{ artistId: "a", name: "Shared" },
			{ artistId: "b", name: "Shared" },
		],
	};
	const trackWithOverrides: OriginalTrack = {
		...track,
		recordingCredits: [
			{ artistId: "a", creditName: "Shared", joinPhrase: " & " },
			{ artistId: "b", creditName: "Shared", joinPhrase: "" },
		],
		recordingMetadata: {
			musicbrainz_artistid: oldIds,
			artist: ["Stale combined name"],
			artists: ["Stale first", "Stale second"],
		},
		published: {
			tags: { MUSICBRAINZ_ARTISTID: oldIds },
			codec: "FLAC",
			container: "FLAC",
			sourceId: null,
			path: "track.flac",
			readAt: "fixture",
			coverHash: null,
			lyrics: null,
		},
		metadataBaseline: { MUSICBRAINZ_ARTISTID: oldIds },
	};
	const tags = effectivePublicationTags(
		releaseWithCredits,
		trackWithOverrides,
		incompleteArtists,
	);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual([]);
	expect(tags.ARTISTS).toEqual(["Shared", "Shared (Boston)"]);
	expect(tags.ARTIST).toEqual(["Shared & Shared (Boston)"]);
	const diff = publicationMetadataDiff(
		releaseWithCredits,
		trackWithOverrides,
		incompleteArtists,
	);
	expect(
		diff
			.flatMap((group) => group.changes)
			.find((change) => change.key === "MUSICBRAINZ_ARTISTID"),
	).toMatchObject({ before: oldIds, after: [] });
});

it("preserves intentional empty artist tag overrides when the identity group is complete", () => {
	const tags = desiredTags(
		{
			...release,
			credits: [
				{ artistId: "a", name: "Shared" },
				{ artistId: "b", name: "Shared" },
			],
		},
		{
			...track,
			recordingCredits: [
				{ artistId: "a", creditName: "Shared", joinPhrase: "" },
				{ artistId: "b", creditName: "Shared", joinPhrase: "" },
			],
			recordingMetadata: {
				artist: [],
				artists: [],
				musicbrainz_artistid: [],
			},
		},
		artists,
	);
	expect(tags.ARTIST).toEqual([]);
	expect(tags.ARTISTS).toEqual([]);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual([]);
});
