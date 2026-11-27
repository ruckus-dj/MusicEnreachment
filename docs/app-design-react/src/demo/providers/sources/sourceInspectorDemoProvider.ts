import { sourceInspectorNotFound } from "../../../ui/features/sources/sourceInspectorMessages";
import type {
  SourceInspectorStepName,
  SourceInspectorStepStatus,
  SourceInspectorViewProps,
} from "../../../ui/features/sources/sourceInspectorModel";
import { sourceMessage } from "../../../ui/features/sources/sourceInventoryMessages";
import {
  sourceDetailHref,
  sourcesHref,
} from "../../../ui/features/sources/sourcesRoute";
import type { SourceFiles } from "../library/catalogData";
import type { SourceRoot } from "./SourcesScreen";
import {
  inventoryPathStale,
  retainedInventoryLocations,
} from "./sourceInventory";
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
  const location = root
    ? retainedInventoryLocations(files, root).find(
        (item) => item.id === locationId,
      )
    : undefined;
  const stale = root ? inventoryPathStale(root) : false;
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
    !stale &&
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
  const inventoryPath = root?.inventoryPath ?? root?.path;
  const prefix = inventoryPath
    ? `${inventoryPath.replace(/\/+$/, "")}/`
    : undefined;
  const relativePath =
    prefix && location?.path.startsWith(prefix)
      ? location.path.slice(prefix.length)
      : undefined;
  return {
    locale,
    sourcesHref: sourcesHref,
    sourceHref: sourceDetailHref(rootId),
    file:
      root && location
        ? {
            relativePath,
            inventoryPath,
            sizeBytes: location.size,
            modifiedAt:
              location.mtime && Number.isFinite(Date.parse(location.mtime))
                ? location.mtime
                : undefined,
            availability:
              root.available === null
                ? "unknown"
                : root.available
                  ? "available"
                  : "unavailable",
            enabled: true,
            stale,
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
    fingerprintReady: steps.fingerprint?.state === "succeeded",
    loading: false,
    busy: false,
    pending,
    operationState: pending ? work.status : undefined,
    streamInterrupted: false,
    error: root && location ? undefined : sourceInspectorNotFound(locale),
  };
}
