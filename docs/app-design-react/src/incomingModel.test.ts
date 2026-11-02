import { expect, it } from "vitest";
import {
	applyPlan,
	assignFile,
	createIncomingState,
	getDraft,
	incomingFixtures,
	makePlan,
	planRows,
	providerReleases,
	providerTags,
	removeRootFromIncomingInventory,
	selectedSourceId,
	unassignFile,
} from "./incomingModel";

const fresh = () => ({
	state: createIncomingState(),
	files: structuredClone(incomingFixtures.files),
	releases: structuredClone(incomingFixtures.releases),
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
