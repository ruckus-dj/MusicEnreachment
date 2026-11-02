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
	const release = local.items.releases.find(
		(item) =>
			item.provider === "Local" &&
			item.tracks?.some((track) => track.recordingId === recording.id),
	)!;
	release.artistIds = [artist.id];
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
		credits: [{ name: artist.name, artistId: artist.id }],
	});
	expect(candidate.tracks[0]).toMatchObject({
		localRecordingId: recording.id,
		recordingId: null,
		providerTrackId: null,
	});
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
	expect(rows[0].nextTags.ARTIST).toEqual([artist.name]);
	expect(rows[0].nextTags.ALBUM).toEqual([candidate.title]);
	expect(
		applyPlan(plan, files, [...existing, candidate], undefined, false, artists),
	).toBe(1);
	expect(candidate.providerId).toBe(release.key);
	expect(candidate.tracks[0].published?.tags.ARTIST).toEqual([artist.name]);
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
