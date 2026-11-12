import { useCallback, useEffect, useRef, useState } from "react";
import {
  isOperationSnapshotNewer,
  useOperationFollower,
} from "../../api/client/useOperationFollower";
import {
  listOperations,
  retryOperation,
  startSourceScan,
} from "../../api/generated/client";
import type {
  OperationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { sourceMessage } from "./sourceMessages";
import {
  message,
  renderSourceError,
  sourceError,
  sourceMessageFailure,
  sourceResponseError,
} from "./sourcesApi";

// The stage text is the whole progress report: bytes_completed is a byte count,
// not a file count, and a scan shows no percentage before the tree is read.
function stageText(operation: OperationResponse, locale: "en" | "ru"): string {
  if (operation.state === "queued") return sourceMessage(locale, "queuedScan");
  const stage = operation.stage.replace(/^retry:/, "");
  if (stage === "traversing") return sourceMessage(locale, "traversing");
  if (stage === "applying") return sourceMessage(locale, "applying");
  return sourceMessage(locale, "scanningStage", { stage });
}

function scanFailure(response: {
  status: number;
  data?: { detail?: string };
}): Error {
  if (response.status === 409)
    return sourceMessageFailure("scanAlreadyRunning");
  if (response.status === 404) return sourceMessageFailure("sourceMissing");
  if (response.status === 400) return sourceMessageFailure("scanRejected");
  return sourceResponseError(
    response,
    sourceError("serverError", { status: response.status }),
  );
}

// Opening the root page must adopt the scan the server already runs for it:
// listOperations is the only source that names that operation, and its id is
// the handle the existing SSE stream and REST snapshot attach to. Historical
// scans, other roots and install/move operations are never a match.
const activeScanStates = ["queued", "running"];

function activeScanForRoot(
  operations: OperationResponse[] | null,
  rootId: string,
): OperationResponse | undefined {
  return (operations || []).find(
    (operation) =>
      operation.kind === "scan_source" &&
      operation.target_source_root_id === rootId &&
      activeScanStates.includes(operation.state),
  );
}

export type SourceScanControlProps = {
  root: SourceRootResponse;
  onScanCompleted: () => void | Promise<void>;
};

export function SourceScanControl(props: SourceScanControlProps) {
  // A new root is a new scan: the key resets this control's state and unmounts
  // the previous stream and snapshot request instead of carrying them over.
  return <SourceScanView key={props.root.id} {...props} />;
}

function SourceScanView({ root, onScanCompleted }: SourceScanControlProps) {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const [operation, setOperation] = useState<OperationResponse>();
  const [starting, setStarting] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [actionError, setActionError] = useState<ReturnType<
    typeof message
  > | null>(null);
  const [streamError, setStreamError] = useState<ReturnType<
    typeof message
  > | null>(null);
  const [discovering, setDiscovering] = useState(true);
  const [discoveryError, setDiscoveryError] = useState<ReturnType<
    typeof message
  > | null>(null);
  const startInFlight = useRef(false);
  const retryInFlight = useRef(false);
  const reportedCompletion = useRef("");
  const completed = useRef(onScanCompleted);
  const operationSnapshot = useRef<OperationResponse | undefined>(undefined);
  useEffect(() => {
    completed.current = onScanCompleted;
  });

  const applySnapshot = useCallback(
    (snapshot: OperationResponse, force = false) => {
      if (
        !force &&
        !isOperationSnapshotNewer(operationSnapshot.current, snapshot)
      )
        return;
      operationSnapshot.current = snapshot;
      setOperation(snapshot);
      const completion =
        snapshot.state === "succeeded" || snapshot.state === "failed"
          ? `${snapshot.id}:${snapshot.state}`
          : "";
      const changed = completion !== reportedCompletion.current;
      // Pending snapshots re-arm the same operation after a retry; repeated
      // terminal snapshots must not repeatedly reload the parent inventory.
      reportedCompletion.current = completion;
      if (completion && changed) void completed.current();
    },
    [],
  );

  // Discovery owns the window between opening the page and knowing whether a
  // scan is already active: the Scan button stays disabled until it answers,
  // so no redundant POST can race an operation the server is already running.
  useEffect(() => {
    const request = new AbortController();
    const discover = async () => {
      setDiscovering(true);
      setDiscoveryError(null);
      try {
        const response = await listOperations(undefined, {
          signal: request.signal,
          cache: "no-store",
        });
        if (request.signal.aborted) return;
        if (response.status !== 200) {
          setDiscoveryError(
            sourceError("discoverScanError", { status: response.status }),
          );
          return;
        }
        const adopted = activeScanForRoot(response.data.operations, root.id);
        if (adopted) applySnapshot(adopted);
      } catch (reason) {
        if (!request.signal.aborted)
          setDiscoveryError({
            type: "message",
            key: "requestFailed",
            suffix: message(reason),
          });
      } finally {
        if (!request.signal.aborted) setDiscovering(false);
      }
    };
    void discover();
    return () => request.abort();
  }, [root.id, applySnapshot]);

  const operationId = operation?.id;
  const follower = useOperationFollower(operationId, {
    initialSnapshot: operation,
    onSnapshot: (snapshot, source) => {
      applySnapshot(snapshot, source === "action");
    },
    onSnapshotRead: (_snapshot, source) => {
      if (source === "stream") setStreamError(null);
    },
    onError: (reason) => {
      const status = reason.message.match(/\((\d+)\)/)?.[1];
      setStreamError(
        status
          ? {
              ...sourceError("requestFailed"),
              suffix: { type: "diagnostic", message: `(${status})` },
            }
          : message(reason),
      );
    },
    onStreamError: (reason) => setStreamError(message(reason)),
    refreshOnStreamError: true,
  });

  async function startScan() {
    if (startInFlight.current) return;
    startInFlight.current = true;
    setStarting(true);
    setActionError(null);
    setDiscoveryError(null);
    try {
      const response = await startSourceScan(root.id);
      if (response.status !== 200) {
        // Path validation can mark the root unavailable without an operation,
        // so no stream will arrive to refresh the authoritative root state.
        void completed.current();
        throw scanFailure(response);
      }
      applySnapshot(response.data);
    } catch (reason) {
      setActionError(message(reason));
    } finally {
      startInFlight.current = false;
      setStarting(false);
    }
  }

  async function retryScan() {
    if (!operation || retryInFlight.current) return;
    retryInFlight.current = true;
    setRetrying(true);
    setActionError(null);
    try {
      const response = await retryOperation(operation.id);
      if (response.status !== 200) throw scanFailure(response);
      follower.acceptSnapshot(response.data, "action", true);
    } catch (reason) {
      setActionError(message(reason));
    } finally {
      retryInFlight.current = false;
      setRetrying(false);
    }
  }

  const pending =
    operation?.state === "queued" || operation?.state === "running";
  const busy = starting || retrying;
  const failure =
    (actionError && renderSourceError(actionError, locale)) ||
    (operation?.state === "failed"
      ? operation.safe_error || t("scanFailed")
      : "");

  return (
    <section aria-labelledby="source-scan-title" className="sources-panel">
      <h2 id="source-scan-title">{t("scanningTitle")}</h2>
      <p className="sources-help">{t("manualScanOnly")}</p>
      {!root.enabled && <p className="sources-note">{t("disabledScan")}</p>}
      <AppButton
        isDisabled={!root.enabled || busy || pending || discovering}
        onPress={() => void startScan()}
      >
        {t("scan")}
      </AppButton>
      {discovering && <p role="status">{t("checkingScan")}</p>}
      {pending && operation && (
        <p role="status">{stageText(operation, locale)}</p>
      )}
      {operation?.state === "succeeded" && <p role="status">{t("scanDone")}</p>}
      {operation?.state === "failed" && (
        <AppButton isDisabled={busy} onPress={() => void retryScan()}>
          {t("retryScan")}
        </AppButton>
      )}
      {operation && streamError && <p role="status">{t("scanStreamError")}</p>}
      {discoveryError && (
        <p role="status">{renderSourceError(discoveryError, locale)}</p>
      )}
      {failure && <p role="alert">{failure}</p>}
    </section>
  );
}
