import { expect, it } from "vitest";
import { originalFiles } from "./catalogData";
import {
	completeSourceStep,
	createSourceScan,
	eligibleSourceCopies,
	failSourceCopyCleanup,
	initialSourceProcessingState,
	processSourceQueue,
	removeEligibleCopy,
	removeRootProcessingState,
	resetSourceProcessingQueue,
	retrySourceStep,
	sourceProcessingLocks,
} from "./sourceProcessingModel";

it("scan only queues per-location work and preserves retained copies", () => {
	const baseline = structuredClone(originalFiles);
	const state = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	expect(state.files.length).toBeGreaterThan(0);
	expect(
		state.files.every(
			(file) =>
				file.status === "queued" &&
				file.steps.every((step) => step.status === "queued"),
		),
	).toBe(true);
	expect(state.files.every((file) => file.copyId === undefined)).toBe(true);
	expect(state.copies).toEqual([]);
	expect(originalFiles).toEqual(baseline);

	const rescanned = createSourceScan(
		state,
		"root-inbox",
		"in_place",
		originalFiles,
	);
	expect(rescanned.files).toEqual(state.files);
	expect(rescanned.copies).toBe(state.copies);
	expect(rescanned.files[0]).toBe(state.files[0]);
	const otherRoot = createSourceScan(
		rescanned,
		"root-library",
		"in_place",
		originalFiles,
	);
	expect(otherRoot.files.length).toBeGreaterThan(state.files.length);
	expect(
		otherRoot.files
			.filter((file) => file.rootId === "root-library")
			.every((file) => file.mode === "in_place"),
	).toBe(true);
});

it("reuses an unchanged observed stat pair and queues fresh work when size or mtime changes", () => {
	const files = structuredClone(originalFiles);
	const source = Object.values(files).find((file) =>
		file.locations?.some((location) => location.rootId === "root-inbox"),
	);
	if (!source?.locations) throw new Error("Missing inbox location fixture");
	const location = source.locations.find(
		(item) => item.rootId === "root-inbox",
	);
	if (!location) throw new Error("Missing inbox location fixture");
	location.size = 1536;
	location.mtime = "2026-09-26T10:20:00Z";
	const firstScan = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		files,
	);
	const sameObservation = createSourceScan(
		firstScan,
		"root-inbox",
		"in_place",
		files,
	);
	const id = `root-inbox:${location.id}`;
	const originalWork = firstScan.files.find((file) => file.id === id);
	expect(sameObservation.files.find((file) => file.id === id)).toBe(
		originalWork,
	);

	location.size = 2048;
	const changedSize = createSourceScan(
		sameObservation,
		"root-inbox",
		"staged",
		files,
	);
	const fresh = changedSize.files.find((file) => file.id === id);
	expect(fresh).not.toBe(originalWork);
	expect(fresh).toMatchObject({
		status: "queued",
		observedSize: 2048,
		observedMtime: location.mtime,
	});
	expect(fresh?.copyId).toBeUndefined();
	location.size = 1536;
	location.mtime = "2026-09-27T10:20:00Z";
	const changedMtime = createSourceScan(
		sameObservation,
		"root-inbox",
		"staged",
		files,
	).files.find((file) => file.id === id);
	expect(changedMtime).toMatchObject({
		status: "queued",
		observedSize: 1536,
		observedMtime: location.mtime,
	});
});

it("does not reuse a retained copy when a changed observation starts new work", () => {
	const files = structuredClone(originalFiles);
	const location = files["north-f1"].locations?.find(
		(item) => item.rootId === "root-inbox",
	);
	if (!location) throw new Error("Missing inbox location fixture");
	location.mtime = "2026-09-26T10:20:00Z";
	const first = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			files,
		),
	);
	const previousCopy = first.files[0].copyId;
	location.mtime = "2026-09-27T10:20:00Z";
	const scanned = createSourceScan(first, "root-inbox", "staged", files);
	expect(scanned.files[0].copyId).toBeUndefined();
	expect(scanned.copies).toEqual(first.copies);
	const processed = processSourceQueue(scanned);
	expect(processed.files[0].copyId).not.toBe(previousCopy);
	expect(processed.copies.some((copy) => copy.id === previousCopy)).toBe(true);
	expect(processed.copies).toHaveLength(2);
});

it("explicit processing creates independent outcomes and only staged failed work gets a copy", () => {
	const files = structuredClone(originalFiles);
	const source = Object.values(files)[0];
	if (!source) throw new Error("Missing source fixture");
	const inboxLocation = source.locations?.find(
		(location) => location.rootId === "root-inbox",
	);
	if (!inboxLocation || !source.locations)
		throw new Error("Missing inbox location fixture");
	source.locations = [
		...source.locations,
		{
			...inboxLocation,
			id: `${inboxLocation.id}-independent`,
			path: `${inboxLocation.path}-independent`,
		},
	];
	const queued = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		files,
	);
	const processed = processSourceQueue(queued, "root-inbox");
	expect(queued.files.every((file) => file.status === "queued")).toBe(true);
	expect(processed.files.every((file) => file.status === "failed")).toBe(true);
	expect(
		processed.files.every(
			(file) =>
				file.steps[0].status === "succeeded" &&
				file.steps[1].status === "failed",
		),
	).toBe(true);
	expect(processed.files.map((file) => file.copyId)).toEqual(
		processed.copies.map((copy) => copy.id),
	);
	expect(new Set(processed.copies.map((copy) => copy.id)).size).toBe(
		processed.files.length,
	);
	expect(processed.copies).toHaveLength(processed.files.length);
	expect(
		processed.copies.every(
			(copy) => copy.status === "retained" && copy.ownerFileId,
		),
	).toBe(true);
	const unrelated = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-library",
			"in_place",
			originalFiles,
		),
		"root-library",
	);
	expect(unrelated.copies).toEqual([]);
	expect(unrelated.files.every((file) => file.copyId === undefined)).toBe(true);

	const first = processed.files[0];
	const retried = retrySourceStep(processed, first.id, "fingerprint");
	expect(retried.files.find((file) => file.id === first.id)?.copyId).toBe(
		first.copyId,
	);
	const completed = completeSourceStep(retried, first.id, "fingerprint");
	expect(completed.files.find((file) => file.id === first.id)?.status).toBe(
		"completed",
	);
	expect(completed.files.slice(1)).toEqual(processed.files.slice(1));
	expect(
		eligibleSourceCopies(completed).map((copy) => copy.ownerFileId),
	).toEqual([first.id]);
});

it("executes queued steps independently without overwriting an already successful step", () => {
	const queued = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	const first = queued.files[0];
	const mixed = {
		...queued,
		files: queued.files.map((file) =>
			file.id === first.id
				? {
						...file,
						steps: file.steps.map((step) =>
							step.id === "fingerprint"
								? { ...step, status: "succeeded" as const }
								: step,
						),
					}
				: file,
		),
	};
	const processed = processSourceQueue(mixed, "root-inbox");
	expect(processed.files.find((file) => file.id === first.id)?.steps).toEqual([
		{ id: "audio_inspection", status: "succeeded" },
		{ id: "fingerprint", status: "succeeded" },
	]);
	expect(processed.files.find((file) => file.id === first.id)?.status).toBe(
		"completed",
	);
});

it("reset retains copy and explicit intent, does not restart, and failed no-copy work does not lock", () => {
	const staged = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			originalFiles,
		),
		"root-inbox",
	);
	const reset = resetSourceProcessingQueue(staged);
	expect(reset.files.every((file) => file.status === "failed")).toBe(true);
	expect(reset.copies).toEqual(staged.copies);
	expect(processSourceQueue(reset)).toEqual(reset);
	expect(sourceProcessingLocks(reset).blocksWorkDirectoryChange).toBe(true);

	const inPlace = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-library",
			"in_place",
			originalFiles,
		),
		"root-library",
	);
	const inPlaceReset = resetSourceProcessingQueue(inPlace);
	expect(inPlaceReset.copies).toEqual([]);
	expect(sourceProcessingLocks(inPlaceReset).hasActiveOperations).toBe(false);
	expect(sourceProcessingLocks(inPlaceReset).blocksWorkDirectoryChange).toBe(
		false,
	);
});

it("allows cleanup only after owner success, preserves errors and protects failed staged copies", () => {
	const state = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			originalFiles,
		),
		"root-inbox",
	);
	const first = state.files[0];
	const copyId = first.copyId!;
	expect(removeEligibleCopy(state, copyId)).toBe(state);
	const protectedCleanup = failSourceCopyCleanup(state, copyId);
	expect(protectedCleanup).toBe(state);

	const retried = retrySourceStep(state, first.id, "fingerprint");
	const succeeded = completeSourceStep(retried, first.id, "fingerprint");
	expect(succeeded.files.find((file) => file.id === first.id)?.status).toBe(
		"completed",
	);
	expect(eligibleSourceCopies(succeeded).map((copy) => copy.id)).toContain(
		copyId,
	);
	const failedCleanup = failSourceCopyCleanup(succeeded, copyId);
	expect(failedCleanup.copies[0]).toMatchObject({
		status: "cleanup_failed",
		cleanupError: expect.any(String),
	});
	expect(eligibleSourceCopies(failedCleanup)).toHaveLength(1);
	const removed = removeEligibleCopy(succeeded, copyId);
	expect(removed.copies).toHaveLength(succeeded.copies.length - 1);
	expect(removed.files).toEqual(succeeded.files);
});

it("removes only the selected root processing records", () => {
	const baseline = structuredClone(originalFiles);
	const staged = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			originalFiles,
		),
		"root-inbox",
	);
	const both = createSourceScan(
		staged,
		"root-library",
		"in_place",
		originalFiles,
	);
	const removed = removeRootProcessingState(both, "root-inbox");
	expect(removed.files.length).toBeGreaterThan(0);
	expect(removed.files.every((file) => file.rootId === "root-library")).toBe(
		true,
	);
	expect(removed.copies).toEqual(
		staged.copies.filter((copy) => copy.rootId !== "root-inbox"),
	);
	expect(originalFiles).toEqual(baseline);
});
