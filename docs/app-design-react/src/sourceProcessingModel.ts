import type { SourceFiles } from "./catalogData";
import type { SourceMessageKey } from "./sourceMessages";

export type DemoStepStatus = "queued" | "running" | "succeeded" | "failed";

export interface SourceProcessingStep {
	id: "audio_inspection" | "fingerprint";
	status: DemoStepStatus;
	error?: SourceMessageKey;
	attempts?: number;
}

export interface SourceProcessingFile {
	id: string;
	rootId: string;
	fileId: string;
	path: string;
	mode: "in_place" | "staged";
	status: "queued" | "running" | "failed" | "completed";
	steps: SourceProcessingStep[];
	copyId?: string;
	observedSize?: number | string;
	observedMtime?: string;
	workRevision?: number;
	blockedReason?: "work_directory_required" | "output_reset";
}

export interface RetainedSourceCopy {
	id: string;
	rootId: string;
	fileId: string;
	ownerFileId: string;
	status: "retained";
}

export interface SourceProcessingState {
	files: SourceProcessingFile[];
	copies: RetainedSourceCopy[];
}

export const initialSourceProcessingState: SourceProcessingState = {
	files: [],
	copies: [],
};

export function createSourceScan(
	state: SourceProcessingState,
	rootId: string,
	mode: "in_place" | "staged",
	files: SourceFiles,
): SourceProcessingState {
	const candidates = Object.values(files).flatMap((file) =>
		(file.locations ?? [])
			.filter((location) => location.rootId === rootId)
			.map((location) => ({ file, location })),
	);
	const nextFiles = candidates.map(({ file, location }) => {
		const id = `${rootId}:${location.id}`;
		const prior = state.files.find((item) => item.id === id);
		const observedSize = location.size ?? file.size;
		const observedMtime = location.mtime;
		if (
			prior &&
			prior.observedSize === observedSize &&
			prior.observedMtime === observedMtime
		)
			return prior;
		return {
			id,
			rootId,
			fileId: file.id,
			path: location.path,
			mode,
			observedSize,
			observedMtime,
			workRevision: (prior?.workRevision ?? -1) + 1,
			status: "queued" as const,
			steps: [
				{ id: "audio_inspection" as const, status: "queued" as const },
				{ id: "fingerprint" as const, status: "queued" as const },
			],
		};
	});
	const filesById = new Map(state.files.map((item) => [item.id, item]));
	for (const item of nextFiles) filesById.set(item.id, item);
	return {
		files: [...filesById.values()],
		copies: state.copies,
	};
}

/** A missing work directory blocks the staged intent without making it active. */
export function blockStagedSourceQueue(
	state: SourceProcessingState,
	rootId: string,
): SourceProcessingState {
	return {
		...state,
		files: state.files.map((file) =>
			file.rootId === rootId &&
			file.mode === "staged" &&
			file.status === "queued"
				? { ...file, blockedReason: "work_directory_required" as const }
				: file,
		),
	};
}

/** Fixed fixture behavior: first fingerprint attempt fails; retry succeeds. */
export function processSourceQueue(
	state: SourceProcessingState,
	rootId?: string,
): SourceProcessingState {
	const files = state.files.map((file) => {
		if (
			(rootId !== undefined && file.rootId !== rootId) ||
			(file.status !== "queued" && file.status !== "failed") ||
			file.blockedReason === "output_reset" ||
			!file.steps.some((step) => step.status === "queued")
		)
			return file;

		const copyId =
			file.mode === "staged"
				? (file.copyId ?? `copy:${file.id}:${file.workRevision ?? 0}`)
				: undefined;
		const steps = file.steps.map((step) => {
			if (step.status !== "queued") return step;
			const attempts = step.attempts ?? 0;
			if (step.id === "fingerprint" && attempts === 0)
				return {
					id: step.id,
					status: "failed" as const,
					error: "fingerprintFailed" as const,
					attempts: attempts + 1,
				};
			return {
				id: step.id,
				status: "succeeded" as const,
				attempts: attempts + 1,
			};
		});
		return {
			...file,
			status: steps.every((step) => step.status === "succeeded")
				? ("completed" as const)
				: ("failed" as const),
			blockedReason: undefined,
			copyId,
			steps,
		};
	});
	const copies = files.flatMap((file) =>
		file.copyId && !state.copies.some((copy) => copy.id === file.copyId)
			? [
					{
						id: file.copyId,
						rootId: file.rootId,
						fileId: file.fileId,
						ownerFileId: file.id,
						status: "retained" as const,
					},
				]
			: [],
	);
	return { files, copies: [...state.copies, ...copies] };
}

export function retryCancelledSourceFile(
	state: SourceProcessingState,
	fileId: string,
): SourceProcessingState {
	let changed = false;
	const files = state.files.map((file) => {
		if (file.id !== fileId || file.blockedReason !== "output_reset")
			return file;
		changed = true;
		return { ...file, status: "queued" as const, blockedReason: undefined };
	});
	return changed ? { ...state, files } : state;
}

export function resetSourceProcessingQueue(
	state: SourceProcessingState,
): SourceProcessingState {
	return {
		files: state.files.map((file) =>
			["queued", "running"].includes(file.status)
				? {
						...file,
						status: "failed" as const,
						blockedReason: "output_reset" as const,
						steps: file.steps.map((step) =>
							step.status === "running"
								? { id: step.id, status: "queued" as const }
								: step,
						),
					}
				: file,
		),
		copies: state.copies,
	};
}

export function retrySourceStep(
	state: SourceProcessingState,
	fileId: string,
	stepId: SourceProcessingStep["id"],
): SourceProcessingState {
	let changed = false;
	const files = state.files.map((file) => {
		if (file.id !== fileId) return file;
		const steps = file.steps.map((step) => {
			if (step.id !== stepId || step.status !== "failed") return step;
			changed = true;
			return {
				id: step.id,
				status: "queued" as const,
				attempts: step.attempts,
			};
		});
		if (!steps.some((step) => step.status === "queued")) return file;
		return {
			...file,
			steps,
			status: "queued" as const,
			blockedReason: undefined,
		};
	});
	return changed ? { ...state, files } : state;
}

export function removeEligibleCopy(
	state: SourceProcessingState,
	copyId: string,
): SourceProcessingState {
	const copy = state.copies.find((item) => item.id === copyId);
	if (!copy) return state;
	const owner = state.files.find((file) => file.id === copy.ownerFileId);
	if (!owner || owner.status !== "completed") return state;
	return {
		...state,
		copies: state.copies.filter((item) => item.id !== copyId),
	};
}

export function removeRootProcessingState(
	state: SourceProcessingState,
	rootId: string,
): SourceProcessingState {
	return {
		files: state.files.filter((file) => file.rootId !== rootId),
		copies: state.copies.filter((copy) => copy.rootId !== rootId),
	};
}

export function sourceProcessingLocks(state: SourceProcessingState) {
	const active = (file: SourceProcessingFile) =>
		["queued", "running"].includes(file.status) && !file.blockedReason;
	const activeStagedWork = state.files.some(
		(file) => file.mode === "staged" && active(file),
	);
	return {
		hasActiveOperations: state.files.some(active),
		hasRetainedCopies: state.copies.length > 0,
		blocksWorkDirectoryChange: activeStagedWork || state.copies.length > 0,
	};
}

export function eligibleSourceCopies(state: SourceProcessingState) {
	return state.copies.filter((copy) =>
		state.files.some(
			(file) => file.id === copy.ownerFileId && file.status === "completed",
		),
	);
}
