import type {
  OperationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import type { SourceScanModel } from "../../ui/features/sources/SourceRootDetailView";
import type { SourceScanState } from "../../ui/features/sources/SourceScanPanel";
import { sourceMessage } from "../../ui/features/sources/sourceMessages";
import { useSourceScans } from "./sourceScans";
import { renderSourceError } from "./sourcesApi";

// A scan shows its server stage instead of a file count: bytes_completed is a
// byte count, and the total is unknown until the tree has been read. A retry
// reuses the same operation, so its stage keeps the server's prefix.
function runningState(stage: string): SourceScanState {
  const current = stage.replace(/^retry:/, "");
  if (current === "traversing") return { kind: "traversing" };
  if (current === "applying") return { kind: "applying" };
  return { kind: "running" };
}

function operationState(
  operation: OperationResponse,
  failedMessage: string,
): SourceScanState {
  if (operation.state === "queued") return { kind: "queued" };
  if (operation.state === "running") return runningState(operation.stage);
  if (operation.state === "succeeded") return { kind: "succeeded" };
  if (operation.state === "failed")
    return { kind: "failed", message: failedMessage };
  return { kind: "idle" };
}

/**
 * Projects the shared source-scan context into the portable scan panel model.
 * It owns no state or effects; admission and operation following stay in
 * SourceScansProvider.
 */
export function useSourceScanPanel(
  root: SourceRootResponse | undefined,
  admissionDisabled?: boolean,
): SourceScanModel | undefined {
  const { locale } = useI18n();
  const {
    operation,
    checking,
    discoveryError,
    streamError,
    busy,
    error,
    start,
    retry,
    discover,
  } = useSourceScans();
  if (!root) return undefined;
  const scan = operation(root.id);
  const rejected = error(root.id);
  const following = scan?.state === "queued" || scan?.state === "running";
  const discoveryFailure = following ? null : discoveryError;
  let state: SourceScanState = { kind: "idle" };
  if (!following && (busy(root.id) || checking)) {
    state = { kind: "checking" };
  } else if (discoveryFailure) {
    state = {
      kind: "error",
      message: renderSourceError(discoveryFailure, locale),
    };
  } else if (rejected && scan?.state !== "failed") {
    state = { kind: "error", message: renderSourceError(rejected, locale) };
  } else if (scan) {
    state = operationState(
      scan,
      rejected
        ? renderSourceError(rejected, locale)
        : scan.safe_error || sourceMessage(locale, "scanFailed"),
    );
  }

  return {
    state,
    onStart: () => void start(root.id),
    onRetry: () => void retry(root.id),
    onRecheck: discoveryFailure ? () => void discover() : undefined,
    startDisabled: admissionDisabled,
    streamNotice:
      following && streamError(root.id)
        ? sourceMessage(locale, "scanStreamError")
        : undefined,
  };
}
