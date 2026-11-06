import { expect, it } from "vitest";
import { originalFiles } from "./catalogData";
import {
	blockStagedSourceQueue,
	canRemoveOriginalFile,
	canRemoveSourceRoot,
	createSourceScan,
	eligibleSourceCopies,
	initialSourceProcessingState,
	processSourceQueue,
	removeEligibleCopy,
	removeRootProcessingState,
	retrySourceStep,
	sourceProcessingLocks,
} from "./sourceProcessingModel";

it("queues inventory records without mutating fixed data and preserves size/mtime freshness", () => {
	const baseline = structuredClone(originalFiles);
	const scanned = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	expect(scanned.files.length).toBeGreaterThan(0);
	expect(
		scanned.files.every(
			(file) =>
				file.status === "queued" &&
				file.steps.every((step) => step.status === "queued"),
		),
	).toBe(true);
	expect(scanned.copies).toEqual([]);
	expect(originalFiles).toEqual(baseline);

	const unchanged = createSourceScan(
		scanned,
		"root-inbox",
		"in_place",
		originalFiles,
	);
	expect(unchanged.files[0]).toBe(scanned.files[0]);

	const files = structuredClone(originalFiles);
	const location = files["north-f1"].locations?.find(
		(item) => item.rootId === "root-inbox",
	);
	if (!location) throw new Error("Missing inbox location fixture");
	const withObservation = structuredClone(files);
	const observedLocation = withObservation["north-f1"].locations?.find(
		(item) => item.rootId === "root-inbox",
	);
	if (!observedLocation) throw new Error("Missing inbox location fixture");
	observedLocation.size = 1536;
	observedLocation.mtime = "2026-09-26T10:20:00Z";
	const first = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		withObservation,
	);
	const same = createSourceScan(first, "root-inbox", "staged", withObservation);
	const originalWork = first.files.find((file) => file.fileId === "north-f1");
	expect(same.files.find((file) => file.fileId === "north-f1")).toBe(
		originalWork,
	);
	observedLocation.size = 2048;
	const changed = createSourceScan(
		same,
		"root-inbox",
		"staged",
		withObservation,
	).files.find((file) => file.fileId === "north-f1");
	expect(changed).not.toBe(originalWork);
	expect(changed).toMatchObject({ status: "queued", observedSize: 2048 });
	expect(changed?.copyId).toBeUndefined();
	expect(location.size).toBeUndefined();
});

it("starts processing with the scan, keeps step outcomes independent, and retries only the failed step", () => {
	const queued = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	const first = queued.files[0];
	if (!first) throw new Error("Missing inbox processing fixture");
	const processed = processSourceQueue(queued, "root-inbox");
	expect(queued.files.every((file) => file.status === "queued")).toBe(true);
	expect(
		processed.files.every(
			(file) =>
				file.status === "failed" &&
				file.steps[0].status === "succeeded" &&
				file.steps[1].status === "failed",
		),
	).toBe(true);
	expect(
		processed.files.every(
			(file) => file.steps[1].error === "fingerprintFailed",
		),
	).toBe(true);
	expect(processed.copies).toHaveLength(processed.files.length);
	expect(eligibleSourceCopies(processed)).toEqual([]);

	const retried = retrySourceStep(processed, first.id, "fingerprint");
	expect(
		retried.files.find((file) => file.id === first.id)?.steps,
	).toMatchObject([
		{ id: "audio_inspection", status: "succeeded" },
		{ id: "fingerprint", status: "queued", attempts: 1 },
	]);
	expect(retrySourceStep(retried, first.id, "audio_inspection")).toBe(retried);
	const completed = processSourceQueue(retried, first.rootId);
	expect(
		completed.files.find((file) => file.id === first.id)?.steps,
	).toMatchObject([
		{ id: "audio_inspection", status: "succeeded" },
		{ id: "fingerprint", status: "succeeded" },
	]);
	expect(completed.files.find((file) => file.id === first.id)?.status).toBe(
		"completed",
	);
	expect(completed.files.slice(1)).toEqual(processed.files.slice(1));
	expect(
		eligibleSourceCopies(completed).map((copy) => copy.ownerFileId),
	).toEqual([first.id]);
});

it("preserves staged processing intent without locking Settings when a work directory is missing", () => {
	const scanned = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	const blocked = blockStagedSourceQueue(scanned, "root-inbox");
	expect(
		blocked.files.every(
			(file) => file.blockedReason === "work_directory_required",
		),
	).toBe(true);
	expect(blocked.files.every((file) => file.status === "queued")).toBe(true);
	expect(sourceProcessingLocks(blocked)).toMatchObject({
		hasActiveOperations: false,
		blocksWorkDirectoryChange: false,
	});
	const resumed = processSourceQueue(blocked, "root-inbox");
	expect(resumed.files.every((file) => file.blockedReason === undefined)).toBe(
		true,
	);
	expect(resumed.files.every((file) => file.status === "failed")).toBe(true);
});

it("removes only eligible app-owned copies and clears only a removed root's processing state", () => {
	const staged = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			originalFiles,
		),
		"root-inbox",
	);
	const first = staged.files[0];
	const copyId = first?.copyId;
	if (!first || !copyId) throw new Error("Missing staged work-copy fixture");
	expect(removeEligibleCopy(staged, copyId)).toBe(staged);
	const completed = processSourceQueue(
		retrySourceStep(staged, first.id, "fingerprint"),
		"root-inbox",
	);
	const removedCopy = removeEligibleCopy(completed, copyId);
	expect(removedCopy.copies).toHaveLength(completed.copies.length - 1);
	expect(removedCopy.files).toEqual(completed.files);

	const otherRoot = createSourceScan(
		completed,
		"root-library",
		"in_place",
		originalFiles,
	);
	const remaining = removeRootProcessingState(otherRoot, "root-inbox");
	expect(remaining.files.length).toBeGreaterThan(0);
	expect(remaining.files.every((file) => file.rootId === "root-library")).toBe(
		true,
	);
	expect(remaining.copies.every((copy) => copy.rootId === "root-library")).toBe(
		true,
	);
});

it("blocks root removal for queued work, but drops failed tasks and staged copies", () => {
	const queued = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	expect(canRemoveSourceRoot(queued, "root-inbox")).toBe(false);
	expect(removeRootProcessingState(queued, "root-inbox")).toBe(queued);

	const failed = processSourceQueue(queued, "root-inbox");
	expect(failed.files.every((file) => file.status === "failed")).toBe(true);
	expect(canRemoveSourceRoot(failed, "root-inbox")).toBe(true);
	const removed = removeRootProcessingState(failed, "root-inbox");
	expect(removed).toEqual({ files: [], copies: [] });
	// Removing app state cannot mutate the host-original inventory fixture.
	expect(Object.keys(originalFiles)).toContain("north-f1");
});

it("allows original cleanup only after opt-in, all assigned publications, and consumers/holds", () => {
	const evidence = {
		optedIn: true,
		assignedSegments: [
			{ id: "segment-a", published: true },
			{ id: "segment-not-queued-yet", published: false },
		],
		activeConsumers: 0,
		activeHolds: 0,
	};
	expect(canRemoveOriginalFile(evidence)).toBe(false);
	expect(
		canRemoveOriginalFile({
			...evidence,
			assignedSegments: evidence.assignedSegments.map((segment) => ({
				...segment,
				published: true,
			})),
		}),
	).toBe(true);
	expect(canRemoveOriginalFile({ ...evidence, activeConsumers: 1 })).toBe(
		false,
	);
	expect(canRemoveOriginalFile({ ...evidence, activeHolds: 1 })).toBe(false);
	expect(canRemoveOriginalFile({ ...evidence, optedIn: false })).toBe(false);
});
