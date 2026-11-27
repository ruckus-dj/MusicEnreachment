import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import { retryOperation } from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import type {
  OperationProgressStatus,
  OperationProgressViewProps,
} from "../../ui/features/setup/OperationProgressView";
import { SetupOperationDataBinder } from "../../ui/features/setup/SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
} from "../../ui/features/setup/setupMessages";
import { useSetupAlertFocus } from "../../ui/features/setup/useSetupAlertFocus";
import {
  operationStageLabel,
  operationStateLabel,
  toolInstallCompletedText,
  toolInstallProgressBar,
  toolInstallTarget,
} from "../operations/operationPresentation";

type OperationInputs = {
  snapshots: Readonly<Record<string, OperationResponse>>;
  onSuccess: () => Promise<void>;
  onFinished: (id: string, snapshot: OperationResponse) => void;
};

export const SetupOperationsContext = createContext<OperationInputs | null>(
  null,
);

export function SetupOperationProgressBinder({
  id,
  children,
}: {
  id: string;
  children: ReactNode;
}) {
  const inputs = useContext(SetupOperationsContext);
  if (!inputs) throw new Error("missing setup operation inputs");
  const model = useOperationProgressModel({
    id,
    initial: inputs.snapshots[id],
    onSuccess: inputs.onSuccess,
    onFinished: inputs.onFinished,
  });
  return (
    <SetupOperationDataBinder model={model}>
      {children}
    </SetupOperationDataBinder>
  );
}

function useOperationProgressModel({
  id,
  initial,
  onSuccess,
  onFinished,
}: {
  id: string;
  initial?: OperationResponse;
  onSuccess: () => Promise<void>;
  onFinished: (id: string, snapshot: OperationResponse) => void;
}): OperationProgressViewProps {
  const { locale } = useI18n();
  const [snapshot, setSnapshot] = useState(initial);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [refreshError, setRefreshError] = useState<SetupFeedback | null>(null);
  const [pending, setPending] = useState(false);
  const [snapshotPending, setSnapshotPending] = useState(false);
  const refreshing = useRef(false);
  const completion = useRef("");
  const alert = useRef<HTMLParagraphElement>(null);
  const refreshAlert = useRef<HTMLParagraphElement>(null);
  useSetupAlertFocus(alert, error);
  useSetupAlertFocus(refreshAlert, refreshError);
  const refreshInstallations = useCallback(
    async (next: OperationResponse) => {
      if (refreshing.current) return;
      refreshing.current = true;
      setPending(true);
      setRefreshError(null);
      try {
        await onSuccess();
        onFinished(id, next);
      } catch (reason) {
        setRefreshError(setupFeedback(reason, "operationFailed"));
      } finally {
        refreshing.current = false;
        setPending(false);
      }
    },
    [id, onSuccess, onFinished],
  );
  const { acceptSnapshot, refresh } = useOperationFollower(id, {
    initialSnapshot: initial,
    onSnapshot: (next) => {
      setSnapshot(next);
      setError(null);
      if (next.state === "queued" || next.state === "running") {
        completion.current = "";
      } else if (next.state === "succeeded") {
        const key = `${next.id}:${next.state}`;
        if (completion.current !== key) {
          completion.current = key;
          void refreshInstallations(next);
        }
      }
    },
    onHttpError: setupHttpError,
    onError: (reason) => setError(setupFeedback(reason, "unknownError")),
    onStreamError: (reason) =>
      setError({ kind: "diagnostic", text: reason.message }),
  });
  useEffect(() => {
    if (snapshot?.state !== "succeeded") return;
    const key = `${snapshot.id}:${snapshot.state}`;
    if (completion.current !== key) {
      completion.current = key;
      void refreshInstallations(snapshot);
    }
  }, [snapshot, refreshInstallations]);
  const refreshSnapshot = useCallback(async () => {
    setSnapshotPending(true);
    setError(null);
    await refresh();
    setSnapshotPending(false);
  }, [refresh]);

  async function retry() {
    setPending(true);
    setError(null);
    try {
      const response = await retryOperation(id);
      if (response.status !== 200) throw setupHttpError(response);
      acceptSnapshot(response.data, "action", true);
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  const failedError =
    snapshot?.state === "failed" ? snapshot.safe_error : undefined;
  const status: OperationProgressStatus | undefined = snapshot
    ? {
        text:
          snapshot.state === "succeeded"
            ? toolInstallCompletedText(snapshot, locale)
            : toolInstallTarget(snapshot, locale),
        stage:
          snapshot.state === "queued"
            ? operationStageLabel("waiting", locale)
            : snapshot.state === "failed"
              ? operationStateLabel("failed", locale)
              : snapshot.state === "running"
                ? operationStageLabel(snapshot.stage, locale) || undefined
                : undefined,
        progress: toolInstallProgressBar(snapshot, locale),
        warning:
          snapshot.state !== "failed"
            ? snapshot.safe_error || undefined
            : undefined,
      }
    : undefined;
  const completed = snapshot?.state === "succeeded" ? snapshot : undefined;
  const refreshToolInventory = () => {
    if (completed) void refreshInstallations(completed);
  };
  return {
    locale,
    status,
    failure: failedError,
    statusRead: error
      ? {
          message: setupFeedbackText(error, locale),
          alertRef: alert,
          pending: snapshotPending,
          onRetry: refreshSnapshot,
        }
      : undefined,
    toolsRefresh: refreshError
      ? {
          message: setupFeedbackText(refreshError, locale),
          alertRef: refreshAlert,
          pending,
          onRetry: refreshToolInventory,
        }
      : undefined,
    retryDownload:
      snapshot?.state === "failed"
        ? { disabled: pending, onPress: retry }
        : undefined,
    retryToolList:
      completed && refreshError
        ? { disabled: pending, onPress: refreshToolInventory }
        : undefined,
    pending: snapshotPending
      ? "status"
      : pending
        ? snapshot?.state === "succeeded"
          ? "tools"
          : "download"
        : undefined,
  };
}
