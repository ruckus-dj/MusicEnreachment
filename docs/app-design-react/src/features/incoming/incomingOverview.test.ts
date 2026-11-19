import { expect, it } from "vitest";
import { createIncomingState, incomingFixtures } from "../../incomingModel";
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
		"Small Hours",
		"Road Notes",
		"Northern lines",
	]);
	expect(rows.map((row) => row.memberCount)).toEqual([8, 5, 9]);
	expect(rows.map((row) => row.pathCount)).toEqual([8, 5, 10]);
	expect(rows.map((row) => row.readyCount)).toEqual([8, 5, 9]);
	const north = rows.find((row) => row.group.id === "north");
	expect(north?.paths.map((path) => path.label)).toContain(
		"/mnt/inbox / north/01 - First Light.flac",
	);
	expect(north?.paths.map((path) => path.label)).toContain(
		"/mnt/backup / Northern Lines/01 - First Light.flac",
	);
	expect(new Set(north?.folderPaths)).toEqual(
		new Set(["/mnt/inbox/north", "/mnt/backup/Northern Lines"]),
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

it("keeps same-folder groups adjacent and separates equal folders on another root", () => {
	const { groups, files } = fresh();
	const template = files["north-extra"];
	const make = (id: string, path: string, album: string, rootId: string) => {
		files[id] = {
			...structuredClone(template),
			id,
			name: path.split("/").at(-1) ?? id,
			path,
			tags: { ...structuredClone(template.tags), ALBUM: [album] },
			locations: [{ id: `loc-${id}`, rootId, path }],
		};
		return { ...groups[0], id, title: album, fileIds: [id] };
	};
	const shuffled = [
		make("inbox-a", "/mnt/inbox/set 10/a.flac", "Alpha", "root-inbox"),
		{ ...groups[0], id: "missing", fileIds: ["missing-file"] },
		make("backup-b", "/mnt/backup/set 10/b.flac", "Beta", "root-backup"),
		make("inbox-c", "/mnt/inbox/set 10/c.flac", "Gamma", "root-inbox"),
		make("inbox-d", "/mnt/inbox/set 2/d.flac", "Delta", "root-inbox"),
		{ ...groups[0], id: "missing-2", fileIds: ["missing-file-2"] },
	];

	const rows = incomingOverviewRows(shuffled, files, "en", sourceRoots);
	expect(rows.map((row) => row.group.id)).toEqual([
		"backup-b",
		"inbox-d",
		"inbox-a",
		"inbox-c",
		"missing",
		"missing-2",
	]);
	expect(rows.slice(-2).map((row) => row.name)).toEqual([
		"Source path unavailable · group 1",
		"Source path unavailable · group 2",
	]);
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
