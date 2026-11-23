import type {
  OperationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceScanPanel, type SourceScanState } from "./SourceScanPanel";
import { sourceMessage } from "./sourceMessages";
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

export type SourceScanControlProps = {
  root: SourceRootResponse;
  admissionDisabled?: boolean;
};

export function SourceScanControl({
  root,
  admissionDisabled,
}: SourceScanControlProps) {
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

  return (
    <div className="my-2 grid gap-1">
      <SourceScanPanel
        locale={locale}
        state={state}
        onStart={() => void start(root.id)}
        onRetry={() => void retry(root.id)}
        onRecheck={discoveryFailure ? () => void discover() : undefined}
        startDisabled={!root.enabled || admissionDisabled}
      />
      {!root.enabled && (
        <p className="sources-note">{sourceMessage(locale, "disabledScan")}</p>
      )}
      {following && streamError(root.id) && (
        <p role="status">{sourceMessage(locale, "scanStreamError")}</p>
      )}
    </div>
  );
}
