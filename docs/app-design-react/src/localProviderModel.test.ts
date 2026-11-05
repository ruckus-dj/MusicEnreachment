import { expect, it } from "vitest";
import {
	applyPlan,
	createIncomingState,
	incomingFixtures,
	makeReleasePlan,
	planRows,
} from "./incomingModel";
import {
	createLocalProviderState,
	localProviderReleases,
} from "./localProviderModel";

it("adapts Local records into provider candidates and publishes with Local identity", () => {
	const local = createLocalProviderState();
	const artist = local.items.artists.find((item) => item.provider === "Local")!;
	const recording = local.items.recordings.find(
		(item) => item.provider === "Local",
	)!;
	recording.artistIds = [artist.id];
	recording.metadataValues = {
		artist: ["First Artist", "Second Artist"],
		musicbrainz_artistid: ["", ""],
		composer: ["Composer One", "Composer Two"],
		work: ["Suite"],
		movement: ["2"],
	};
	recording.credits = [
		{ artistId: "", creditName: "First Artist", joinPhrase: " & " },
		{ artistId: "", creditName: "Second Artist", joinPhrase: "" },
	];
	recording.genres = ["unknown-local-genre"];
	recording.genreNamesById = { "unknown-local-genre": "Uncatalogued genre" };
	const release = local.items.releases.find(
		(item) =>
			item.provider === "Local" &&
			item.tracks?.some((track) => track.recordingId === recording.id),
	)!;
	release.artistIds = [artist.id];
	release.release_date = "2004-03";
	release.metadataValues = {
		releasedate: ["2004-03"],
		album: ["Local album"],
		script: ["Latn"],
	};
	release.genres = ["unknown-release-genre"];
	release.genreNamesById = { "unknown-release-genre": "Local release genre" };
	release.credits = [
		{ artistId: "", creditName: "Release credit", joinPhrase: "" },
	];
	release.tracks = [
		{
			id: "stable-local-track",
			recordingId: recording.id,
			titleOverride: "",
			durationOverrideMs: "",
		},
	];
	const [candidate] = localProviderReleases(local).filter(
		(item) => item.id === release.id,
	);
	expect(candidate).toMatchObject({
		providerId: release.key,
		artistId: artist.id,
		year: "2004-03",
		metadataValues: {
			releasedate: ["2004-03"],
			album: ["Local album"],
			script: ["Latn"],
		},
		script: "Latn",
		metadataGenres: [
			{ id: "unknown-release-genre", name: "Local release genre" },
		],
		credits: [
			{
				name: "Release credit",
				credit: "Release credit",
				artistId: artist.id,
			},
		],
	});
	expect(candidate.tracks[0]).toMatchObject({
		localRecordingId: recording.id,
		recordingId: null,
		providerTrackId: null,
		recordingMetadata: recording.metadataValues,
		recordingCredits: recording.credits,
		recordingGenres: ["Uncatalogued genre"],
		recordingGenreRefs: [
			{ id: "unknown-local-genre", name: "Uncatalogued genre" },
		],
	});
	expect(candidate.tracks[0].recordingMetadata?.artist).toEqual([
		"First Artist",
		"Second Artist",
	]);
	expect(candidate.tracks[0].recordingMetadata?.musicbrainz_artistid).toEqual([
		"",
		"",
	]);
	expect(candidate.tracks[0].recordingMetadata).not.toHaveProperty("script");
	expect(candidate.tracks[0].recordingCredits).toHaveLength(2);
	expect(candidate.tracks[0].artistId).toBe(
		recording.artistIds?.[0] ?? artist.id,
	);

	const state = createIncomingState();
	const files = structuredClone(incomingFixtures.files);
	const existing = structuredClone(incomingFixtures.releases);
	const artists = [
		...incomingFixtures.artists,
		{ id: artist.id, name: artist.name ?? "", genre: "Локальный каталог" },
	];
	candidate.tracks[0].sourceId = "north-f1";
	candidate.tracks[0].sourceIds = ["north-f1"];
	const plan = makeReleasePlan(state, candidate, files);
	const rows = planRows(
		plan,
		files,
		[...existing, candidate],
		undefined,
		artists,
	);
	expect(rows[0].nextTags.MUSICBRAINZ_ALBUMID).toEqual([]);
	expect(rows[0].nextTags.MUSICBRAINZ_ARTISTID).toEqual([]);
	expect(rows[0].nextTags.ARTIST).toEqual(["First Artist", "Second Artist"]);
	expect(rows[0].nextTags.ALBUM).toEqual([candidate.title]);
	expect(
		applyPlan(plan, files, [...existing, candidate], undefined, false, artists),
	).toBe(1);
	expect(candidate.providerId).toBe(release.key);
	expect(candidate.tracks[0].published?.tags.ARTIST).toEqual([
		"First Artist",
		"Second Artist",
	]);
});

it("keeps Local track identities stable when tracks are reordered or deleted", () => {
	const local = createLocalProviderState();
	const release = local.items.releases.find(
		(item) => item.provider === "Local",
	)!;
	const recordingId = local.items.recordings.find(
		(item) => item.provider === "Local",
	)!.id;
	release.tracks = [
		{
			id: "local-track-first",
			recordingId,
			titleOverride: "First",
			durationOverrideMs: "",
		},
		{
			id: "local-track-second",
			recordingId,
			titleOverride: "Second",
			durationOverrideMs: "",
		},
	];
	const [initial] = localProviderReleases(local).filter(
		(item) => item.id === release.id,
	);
	release.tracks.reverse();
	const [reordered] = localProviderReleases(local).filter(
		(item) => item.id === release.id,
	);
	expect(reordered.tracks.map((track) => track.id)).toEqual([
		initial.tracks[1].id,
		initial.tracks[0].id,
	]);
	expect(reordered.tracks.map((track) => track.position)).toEqual([1, 2]);
	release.tracks.pop();
	const [deleted] = localProviderReleases(local).filter(
		(item) => item.id === release.id,
	);
	expect(deleted.tracks.map((track) => track.id)).toEqual([
		initial.tracks[1].id,
	]);
	expect(deleted.tracks[0].recordingId).toBeNull();
	expect(deleted.tracks[0].providerTrackId).toBeNull();
});

it("keeps a newly created Local record out of candidates until explicitly selected for matching", () => {
	const local = createLocalProviderState();
	const created = {
		id: "created-local-release",
		key: "local-release-key",
		provider: "Local",
		title: "New release",
		artistIds: [],
		tracks: [],
	};
	expect(
		localProviderReleases(local).some((item) => item.id === created.id),
	).toBe(false);
	local.items.releases.unshift(created);
	expect(
		localProviderReleases(local).find((item) => item.id === created.id),
	).toMatchObject({
		providerId: created.key,
		title: created.title,
		tracks: [],
	});
});

it("resolves non-Local references from the provider catalogs and preserves zero duration overrides", () => {
	const state = createLocalProviderState();
	const artist = state.items.artists.find((item) => item.provider !== "Local")!;
	const recording = state.items.recordings.find(
		(item) => item.provider !== "Local",
	)!;
	artist.name = "Provider artist metadata";
	recording.key = "native-recording-key";
	recording.artistIds = [artist.id];
	recording.duration_ms = 187_000;
	recording.metadataValues = { title: ["Provider recording metadata"] };
	recording.credits = [
		{
			artistId: artist.id,
			creditName: "Provider credit",
			joinPhrase: " feat. ",
		},
	];
	const release = state.items.releases.find(
		(item) => item.provider === "Local",
	)!;
	release.artistIds = [artist.id];
	release.tracks = [
		{
			id: "zero-override",
			recordingId: recording.id,
			titleOverride: "",
			durationOverrideMs: 0,
		},
		{
			id: "inherit-duration",
			recordingId: recording.id,
			titleOverride: "",
			durationOverrideMs: "",
		},
	];

	const candidate = localProviderReleases(state).find(
		(item) => item.id === release.id,
	)!;
	expect(candidate.artistId).toBe(artist.id);
	expect(candidate.credits).toContainEqual(
		expect.objectContaining({ artistId: artist.id }),
	);
	expect(candidate.tracks[0]).toMatchObject({
		duration: 0,
		artistId: artist.id,
		localRecordingId: recording.id,
		recordingTitle: "Provider recording metadata",
	});
	expect(candidate.tracks[0].recordingCredits).toEqual(recording.credits);
	expect(candidate.tracks[1].duration).toBe(187);
});
