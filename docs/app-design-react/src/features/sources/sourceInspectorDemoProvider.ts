import type { SourceFiles } from "../library/catalogData";
import type { SourceRoot } from "./SourcesScreen";
import { sourceInspectorNotFound } from "./sourceInspectorMessages";
import type {
	SourceInspectorStepName,
	SourceInspectorStepStatus,
	SourceInspectorViewProps,
} from "./sourceInspectorModel";
import { rootLocations } from "./sourceInventory";
import { sourceMessage } from "./sourceMessages";
import {
	processSourceQueue,
	retrySourceStep,
	type SourceProcessingState,
} from "./sourceProcessingModel";

const stepNames = {
	audio_inspection: "probe",
	fingerprint: "fingerprint",
} as const;

export function sourceInspectorDemoProvider({
	locale,
	rootId,
	locationId,
	root,
	files,
	processing,
	updateProcessing,
	workDirectory,
	scanning,
}: {
	locale: SourceInspectorViewProps["locale"];
	rootId: string;
	locationId: string;
	root: SourceRoot | undefined;
	files: SourceFiles;
	processing: SourceProcessingState;
	updateProcessing: (state: SourceProcessingState) => void;
	workDirectory: string;
	scanning: boolean;
}): SourceInspectorViewProps {
	const location = root?.lastSuccessfulScanAt
		? rootLocations(files, rootId).find((item) => item.id === locationId)
		: undefined;
	const fileId = `${rootId}:${locationId}`;
	const work = location
		? processing.files.find((item) => item.id === fileId)
		: undefined;
	const pending =
		!!work &&
		!work.blockedReason &&
		(work.status === "queued" || work.status === "running");
	const canRetry =
		root?.available === true &&
		!scanning &&
		!pending &&
		(root.mode !== "staged" || !!workDirectory);
	const steps: Partial<
		Record<SourceInspectorStepName, SourceInspectorStepStatus>
	> = {};
	const retryAvailable: Partial<Record<SourceInspectorStepName, boolean>> = {};
	for (const step of work?.steps ?? []) {
		const name = stepNames[step.id];
		steps[name] = {
			state: step.status,
			safeError: step.error ? sourceMessage(locale, step.error) : undefined,
		};
		retryAvailable[name] = canRetry && step.status === "failed";
	}
	const onRetryStep = (name: SourceInspectorStepName) => {
		if (!retryAvailable[name]) return;
		const step = work?.steps.find((item) => stepNames[item.id] === name);
		if (step)
			updateProcessing(
				processSourceQueue(
					retrySourceStep(processing, fileId, step.id),
					rootId,
					fileId,
				),
			);
	};
	const copy = work?.copyId
		? processing.copies.find((item) => item.id === work.copyId)
		: undefined;
	const prefix = root ? `${root.path.replace(/\/+$/, "")}/` : undefined;
	return {
		locale,
		sourcesHref: "#sources",
		sourceHref: `#sources/${encodeURIComponent(rootId)}`,
		file:
			root && location
				? {
						relativePath:
							prefix && location.path.startsWith(prefix)
								? location.path.slice(prefix.length)
								: undefined,
						inventoryPath: root.path,
						sizeBytes: location.size,
						modifiedAt: location.mtime,
						availability:
							root.available === null
								? "unknown"
								: root.available
									? "available"
									: "unavailable",
						enabled: true,
						stale: false,
					}
				: undefined,
		steps,
		retryAvailable,
		onRetryStep,
		artifact: copy
			? {
					state: copy.status,
					requestedSteps: [],
					requestedStepsKnown: false,
					reusedFromOtherOperation: false,
				}
			: undefined,
		fingerprintRerunAvailable: false,
		onRerunFingerprint: () => {},
		loading: false,
		busy: false,
		pending,
		operationState: pending ? work.status : undefined,
		streamInterrupted: false,
		error: root && location ? undefined : sourceInspectorNotFound(locale),
	};
}
