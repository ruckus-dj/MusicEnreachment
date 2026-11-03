import type { SourceFiles } from "./catalogData";

export type DemoStepStatus = "queued" | "running" | "succeeded" | "failed";

export interface SourceProcessingStep {
	id: "audio_inspection" | "fingerprint";
	status: DemoStepStatus;
	error?: string;
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
}

export interface RetainedSourceCopy {
	id: string;
	rootId: string;
	fileId: string;
	ownerFileId: string;
	status: "retained" | "cleanup_failed";
	cleanupError?: string;
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

export function processSourceQueue(
	state: SourceProcessingState,
	rootId?: string,
): SourceProcessingState {
	const files = state.files.map((file) => {
		if (
			(rootId !== undefined && file.rootId !== rootId) ||
			(file.status !== "queued" && file.status !== "failed") ||
			!file.steps.some((step) => step.status === "queued")
		)
			return file;

		const copyId =
			file.mode === "staged"
				? (file.copyId ?? `copy:${file.id}:${file.workRevision ?? 0}`)
				: undefined;
		const steps = file.steps.map((step) =>
			step.status !== "queued"
				? step
				: step.id === "audio_inspection"
					? { id: step.id, status: "succeeded" as const }
					: {
							id: step.id,
							status: "failed" as const,
							error:
								"Демонстрационная ошибка fingerprint: повторите шаг после устранения причины. Файловая система в макете не проверяется.",
						},
		);
		return {
			...file,
			status: steps.every((step) => step.status === "succeeded")
				? ("completed" as const)
				: ("failed" as const),
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

export function resetSourceProcessingQueue(
	state: SourceProcessingState,
): SourceProcessingState {
	return {
		files: state.files.map((file) =>
			["queued", "running"].includes(file.status)
				? {
						...file,
						status: "failed" as const,
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
	return {
		...state,
		files: state.files.map((file) => {
			if (file.id !== fileId) return file;
			const steps = file.steps.map((step) =>
				step.id === stepId && step.status === "failed"
					? { id: step.id, status: "queued" as const }
					: step,
			);
			return {
				...file,
				steps,
				status: "queued" as const,
			};
		}),
	};
}

export function completeSourceStep(
	state: SourceProcessingState,
	fileId: string,
	stepId: SourceProcessingStep["id"],
): SourceProcessingState {
	const files = state.files.map((file) => {
		if (file.id !== fileId) return file;
		const steps = file.steps.map((step) =>
			step.id === stepId && step.status === "queued"
				? { id: step.id, status: "succeeded" as const }
				: step,
		);
		const completed = steps.every((step) => step.status === "succeeded");
		return {
			...file,
			steps,
			status: completed ? ("completed" as const) : ("failed" as const),
		};
	});
	return { ...state, files };
}

export function failSourceCopyCleanup(
	state: SourceProcessingState,
	copyId: string,
): SourceProcessingState {
	if (!eligibleSourceCopies(state).some((copy) => copy.id === copyId))
		return state;
	return {
		...state,
		copies: state.copies.map((copy) =>
			copy.id === copyId
				? {
						...copy,
						status: "cleanup_failed" as const,
						cleanupError: "Демонстрационная ошибка очистки копии.",
					}
				: copy,
		),
	};
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
	const activeStagedWork = state.files.some(
		(file) =>
			file.mode === "staged" && ["queued", "running"].includes(file.status),
	);
	return {
		hasActiveOperations: state.files.some((file) =>
			["queued", "running"].includes(file.status),
		),
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
