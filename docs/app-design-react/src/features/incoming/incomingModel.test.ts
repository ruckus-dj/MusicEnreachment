import { expect, it } from "vitest";
import {
	applyPlan,
	assignFile,
	createIncomingState,
	getDraft,
	incomingFixtures,
	makePlan,
	makeReleasePlan,
	planRows,
	providerReleases,
	providerTags,
	removeRootFromIncomingInventory,
	selectedSourceId,
	sourceIds,
	unassignFile,
} from "./incomingModel";

const fresh = () => ({
	state: createIncomingState(),
	files: structuredClone(incomingFixtures.files),
	releases: structuredClone(incomingFixtures.releases),
});

it("normalizes optional source associations without dropping the legacy fileId", () => {
	expect(sourceIds(undefined)).toEqual([]);
	expect(sourceIds({})).toEqual([]);
	expect(sourceIds({ fileId: "legacy" })).toEqual(["legacy"]);
	expect(sourceIds({ sourceIds: [], fileId: "legacy" })).toEqual([]);
	expect(sourceIds({ sourceIds: ["a", "b"], fileId: "legacy" })).toEqual([
		"a",
		"b",
	]);
});

it("freezes only the current selected source for contextual release publication", () => {
	const { state, files, releases } = fresh();
	const release = releases.find((item) => item.id === "north")!;
	const track = release.tracks[0];
	const selected = track.sourceId!;
	const plan = makeReleasePlan(state, release, files, track.id);
	expect(plan.inventoryVersion).toBe(state.inventoryVersion);
	expect(plan.assignments[track.id].selectedSourceId).toBe(selected);
	expect(plan.assignments[track.id].included).toBe(true);
	expect(
		Object.values(plan.assignments).filter((item) => item.included),
	).toHaveLength(1);
	expect(planRows(plan, files, releases)[0].file?.id).toBe(selected);
	expect(planRows(plan, files, releases)[1].assignment?.included).toBe(false);
	const manualArtist = { id: "manual-test-artist", name: "New Manual Artist" };
	plan.target.tracks[0].artistId = manualArtist.id;
	release.tracks[0].artistId = manualArtist.id;
	expect(
		planRows(plan, files, releases, undefined, [manualArtist])[0].nextTags
			.ARTIST,
	).toEqual([manualArtist.name]);
});

it("resolves a new manual artist from the supplied registry for tags and path", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	const plan = makePlan(state, files, releases)!;
	const localRelease = releases.find((item) => item.id === "north")!;
	const artist = { id: "manual-path-artist", name: "New Manual Artist" };
	plan.target.artistId = artist.id;
	localRelease.artistId = artist.id;
	for (const targetTrack of plan.target.tracks)
		targetTrack.artistId = artist.id;
	for (const track of localRelease.tracks) track.artistId = artist.id;
	const track = localRelease.tracks[0];
	plan.assignments = {
		[plan.target.tracks[0].id]: plan.assignments[plan.target.tracks[0].id],
	};
	plan.assignments[plan.target.tracks[0].id].included = true;
	track.published = null;

	expect(applyPlan(plan, files, releases, undefined, false, [artist])).toBe(1);
	const published = releases.find((item) => item.id === localRelease.id)
		?.tracks[0].published;
	expect(published?.tags.ARTIST).toEqual([artist.name]);
	expect(published?.path).toContain(`/srv/music/${artist.name}/`);
});

it("projects local publication audio tags from the selected replacement source", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	state.groups.find((group) => group.id === "north")!.mode = "local";
	const plan = makePlan(state, files, releases)!;
	const sourceId = "north-extra";
	const track = releases.find((release) => release.id === "north")!.tracks[0];
	track.audioMetadata = {
		...track.audioMetadata,
		[sourceId]: { replaygain_track_gain: ["-8.25"] },
	};
	plan.assignments = {
		[plan.target.tracks[0].id]: {
			...plan.assignments[plan.target.tracks[0].id],
			sourceIds: [sourceId],
			selectedSourceId: sourceId,
			included: true,
		},
	};
	expect(
		planRows(plan, files, releases)[0].nextTags.REPLAYGAIN_TRACK_GAIN,
	).toEqual(["-8.25"]);
});

it("filters an invalid included row from application while preserving other positions", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	const plan = makePlan(state, files, releases)!;
	const local = releases.find((release) => release.id === "north")!;
	const invalid = local.tracks[0];
	const valid = local.tracks[1];
	invalid.recordingMetadata = { explicitstatus: ["maybe"] };
	const invalidPublication = structuredClone(invalid.published);
	valid.published = null;
	plan.assignments = Object.fromEntries(
		plan.target.tracks.slice(0, 2).map((targetTrack, index) => [
			targetTrack.id,
			{
				...plan.assignments[targetTrack.id],
				included: true,
				sourceIds: [index === 0 ? "north-f1" : "north-f2"],
				selectedSourceId: index === 0 ? "north-f1" : "north-f2",
			},
		]),
	);
	const originalPositions = local.tracks.map((track) => track.position);

	expect(applyPlan(plan, files, releases)).toBe(1);
	expect(invalid.published).toEqual(invalidPublication);
	expect(valid.published).not.toBeNull();
	expect(local.tracks.map((track) => track.position)).toEqual(
		originalPositions,
	);
});

it("replaces only audio measurement tags on an automatic quality upgrade", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	state.groups.find((group) => group.id === "north")!.mode = "local";
	const plan = makePlan(state, files, releases)!;
	const targetTrack = plan.target.tracks[0];
	const track = releases.find((release) => release.id === "north")!.tracks[0];
	if (!track.published) throw new Error("Missing publication fixture");
	const sourceId = "north-upgrade";
	files[sourceId] = {
		...files["north-f1"],
		id: sourceId,
		codec: "FLAC",
	};
	track.audioMetadata = {
		...track.audioMetadata,
		[sourceId]: { replaygain_track_gain: ["-5.00"] },
	};
	track.published = {
		...track.published,
		codec: "MP3",
		tags: {
			...track.published.tags,
			REPLAYGAIN_TRACK_GAIN: ["-12.00 dB"],
			R128_TRACK_GAIN: ["-4"],
		},
	};
	plan.assignments = {
		[targetTrack.id]: {
			...plan.assignments[targetTrack.id],
			included: true,
			sourceIds: [sourceId],
			selectedSourceId: sourceId,
		},
	};

	expect(applyPlan(plan, files, releases, undefined, true)).toBe(1);
	expect(track.published?.tags.REPLAYGAIN_TRACK_GAIN).toEqual(["-5.00"]);
	expect(track.published?.tags.R128_TRACK_GAIN).toBeUndefined();
	expect(track.metadataBaseline?.REPLAYGAIN_TRACK_GAIN).toEqual(["-5.00"]);
	expect(track.published?.tags.TITLE).toEqual(track.metadataBaseline?.TITLE);
});

it("preserves an identical current publication and its complete metadata", () => {
	const { state, files, releases } = fresh();
	const plan = makePlan(state, files, releases)!;
	const target = plan.target.tracks[0];
	const track = releases.find((release) => release.id === "north")!.tracks[0];
	if (!track.published) throw new Error("Missing publication baseline");
	track.published = {
		...track.published,
		tags: providerTags(plan.target, target),
		sourceId: "north-f1",
		codec: "FLAC",
	};
	plan.assignments = { [target.id]: plan.assignments[target.id] };
	const before = structuredClone(track.published);
	expect(planRows(plan, files, releases)[0].action).toBe("Без изменений");
	expect(applyPlan(plan, files, releases)).toBe(0);
	expect(track.published).toEqual(before);
	expect(track.published.tags.MUSICBRAINZ_TRACKID).toEqual(
		before.tags.MUSICBRAINZ_TRACKID,
	);
});

it("forces an identical current publication through the replacement path", () => {
	const { state, files, releases } = fresh();
	const plan = makePlan(state, files, releases)!;
	const target = plan.target.tracks[0];
	const track = releases.find((release) => release.id === "north")!.tracks[0];
	if (!track.published) throw new Error("Missing publication baseline");
	track.published = {
		...track.published,
		tags: providerTags(plan.target, target),
		sourceId: "north-f1",
		codec: "FLAC",
	};
	plan.assignments = { [target.id]: plan.assignments[target.id] };
	plan.force = true;

	expect(planRows(plan, files, releases)[0].action).toBe("Заменить");
	expect(
		applyPlan(plan, files, releases, undefined, false, undefined, {
			outputDirectory: "/publish/current",
			format: "mka",
		}),
	).toBe(1);
	expect(track.published?.sourceId).toBe("north-f1");
	expect(track.published?.readAt).toBe("Только что · демо");
	expect(track.published?.path).toContain("/publish/current/");
	expect(track.published?.container).toBe("MKA");
});

it("does not create a providerless release from a stale local plan", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	state.groups.find((group) => group.id === "north")!.mode = "local";
	const plan = makePlan(state, files, releases)!;
	plan.localReleaseId = "missing-local-release";

	expect(applyPlan(plan, files, releases)).toBe(0);
	expect(releases.some((release) => release.id.startsWith("import-"))).toBe(
		false,
	);
});

it("retains a legacy selected source when saving another association only", () => {
	const { state, files, releases } = fresh();
	const group = state.groups.find((group) => group.id === "north")!;
	const track = releases.find((release) => release.id === "north")!.tracks[1];
	const oldSource = track.sourceId;
	track.sourceIds = [];
	const before = structuredClone(track.published);
	assignFile(group, "p-north-t2", "north-extra", files, releases);
	const plan = makePlan(state, files, releases)!;
	for (const assignment of Object.values(plan.assignments))
		assignment.included = false;
	expect(applyPlan(plan, files, releases)).toBe(0);
	expect(track.sourceIds).toEqual(
		expect.arrayContaining([oldSource, "north-extra"]),
	);
	expect(track.sourceId).toBe(oldSource);
	expect(track.published).toEqual(before);
});

it("retains an independent draft for every provider candidate", () => {
	const { state, files, releases } = fresh();
	const group = state.groups.find((item) => item.id === "north")!;
	const first = getDraft(group, files, providerReleases, releases);
	first["p-north-t2"].included = false;
	group.candidateId = "p-japan";
	const second = getDraft(group, files, providerReleases, releases);
	expect(second["p-japan-t2"]).toBeUndefined();
	assignFile(group, "p-japan-t2", "north-f2", files, releases);
	second["p-japan-t2"].included = true;
	expect(
		getDraft(group, files, providerReleases, releases)["p-japan-t2"].included,
	).toBe(true);
	group.candidateId = "p-north";
	expect(
		getDraft(group, files, providerReleases, releases)["p-north-t2"].included,
	).toBe(false);
});

it("keeps multiple associated sources and does not auto-select a newly added option", () => {
	const { state, files, releases } = fresh();
	const group = state.groups.find((item) => item.id === "north")!;
	const draft = getDraft(group, files, providerReleases, releases);
	const before = selectedSourceId(draft["p-north-t2"]);
	expect(assignFile(group, "p-north-t2", "north-extra", files, releases)).toBe(
		true,
	);
	expect(draft["p-north-t2"].sourceIds).toEqual(["north-f2", "north-extra"]);
	expect(selectedSourceId(draft["p-north-t2"])).toBe(before);
	unassignFile(draft, "p-north-t2", "north-f2");
	expect(draft["p-north-t2"].sourceIds).toEqual(["north-extra"]);
});

it("creates a partial plan, preserves unselected publications, and freezes inventory version", () => {
	const { state, files, releases } = fresh();
	state.groupId = "north";
	const plan = makePlan(state, files, releases)!;
	plan.assignments["p-north-t1"].included = false;
	const row = planRows(plan, files, releases).find(
		(item) => item.targetTrack.id === "p-north-t1",
	)!;
	expect(row.action).toBe("Пропустить");
	expect(row.previous?.codec).toBe("MP3");
	expect(plan.inventoryVersion).toBe(0);
	removeRootFromIncomingInventory("root-inbox", state, files, releases);
	expect(state.inventoryVersion).toBe(1);
	expect(plan.inventoryVersion).not.toBe(state.inventoryVersion);
	expect(row.previous?.codec).toBe("MP3");
});

it("removes only inventory locations and clears selected-source authority without replacement", () => {
	const { state, files, releases } = fresh();
	const group = state.groups.find((item) => item.id === "north")!;
	const draft = getDraft(group, files, providerReleases, releases);
	const beforePublication = structuredClone(
		releases
			.find((item) => item.id === "north")!
			.tracks.map((track) => track.published),
	);
	assignFile(group, "p-north-t3", "north-f3", files, releases);
	removeRootFromIncomingInventory("root-library", state, files, releases);
	expect(files["north-f3"].available).toBe(false);
	expect(selectedSourceId(draft["p-north-t3"])).toBeNull();
	expect(
		releases
			.find((item) => item.id === "north")!
			.tracks.find((track) => track.id === "north-t3")?.sourceId,
	).toBeNull();
	expect(
		releases
			.find((item) => item.id === "north")!
			.tracks.map((track) => track.published),
	).toEqual(beforePublication);
});
