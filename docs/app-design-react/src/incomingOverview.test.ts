import { expect, it } from "vitest";
import { createIncomingState, incomingFixtures } from "./incomingModel";
import { incomingOverviewRows } from "./incomingOverview";

const sourceRoots = ["/mnt/inbox", "/mnt/backup"];

const fresh = () => ({
	groups: createIncomingState().groups,
	files: structuredClone(incomingFixtures.files),
});

it("names groups from album evidence and reports unique paths separately from source readiness", () => {
	const { groups, files } = fresh();
	const rows = incomingOverviewRows(groups, files, "en", sourceRoots);

	expect(rows.map((row) => row.name)).toEqual([
		"Northern lines",
		"Small Hours",
		"Road Notes",
	]);
	expect(rows.map((row) => row.memberCount)).toEqual([9, 8, 5]);
	expect(rows.map((row) => row.pathCount)).toEqual([10, 8, 5]);
	expect(rows.map((row) => row.readyCount)).toEqual([9, 8, 5]);
	expect(rows[0].paths.map((path) => path.label)).toContain(
		"/mnt/inbox / north/01 - First Light.flac",
	);
	expect(rows[0].paths.map((path) => path.label)).toContain(
		"/mnt/backup / Northern Lines/01 - First Light.flac",
	);
});

it("keeps source readiness distinct from the release evaluation", () => {
	const { groups, files } = fresh();
	files["hours-f1"].available = false;
	const rows = incomingOverviewRows(groups, files, "en", sourceRoots);
	const hours = rows.find((row) => row.group.id === "hours");

	expect(hours?.readyCount).toBe(7);
	expect(hours?.memberCount).toBe(8);
	expect(hours?.group.reason).toEqual({
		code: "confident",
		params: { count: 8, total: 8 },
	});
});

it("falls back to a common folder or the first source-relative path plus remaining files", () => {
	const { groups, files } = fresh();
	const hours = groups.find((group) => group.id === "hours");
	if (!hours) throw new Error("Missing hours fixture");
	for (const id of hours.fileIds) delete files[id].tags.ALBUM;

	const mixed = {
		...groups[0],
		id: "mixed",
		fileIds: ["hours-f1", "north-f2"],
	};
	for (const id of mixed.fileIds) delete files[id].tags.ALBUM;

	const rows = incomingOverviewRows(
		[...groups, mixed],
		files,
		"ru",
		sourceRoots,
	);
	expect(rows.find((row) => row.group.id === "hours")?.name).toBe("hours");
	expect(rows.find((row) => row.group.id === "mixed")?.name).toBe(
		"hours/01 - Green Room.flac и ещё 1 файл",
	);
});
