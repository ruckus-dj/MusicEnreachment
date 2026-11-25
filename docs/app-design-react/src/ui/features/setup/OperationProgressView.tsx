import type { RefObject } from "react";
import { AppButton } from "../../components/AppButton";
import { SetupFeedbackView } from "./SetupFeedbackView";

export type OperationProgressLocale = "en" | "ru";

const messages = {
  en: {
    download: "Tool download",
    loadingStatus: " — loading status…",
    retryStatus: "Retry status read",
    loadingOperationStatus: "Loading operation status…",
    retryDownload: "Retry download",
    retryToolList: "Retry tool list refresh",
    loadingTools: "Loading tools…",
    retryingDownload: "Retrying download…",
  },
  ru: {
    download: "Загрузка инструмента",
    loadingStatus: " — загрузка состояния…",
    retryStatus: "Повторить загрузку состояния",
    loadingOperationStatus: "Загрузка состояния операции…",
    retryDownload: "Повторить загрузку",
    retryToolList: "Повторить загрузку списка инструментов",
    loadingTools: "Загрузка инструментов…",
    retryingDownload: "Повторная загрузка…",
  },
} as const;

/** Controller-formatted operation line; the view owns the separator layout. */
export type OperationProgressStatus = {
  /** Kind, target and state; already localized. */
  text: string;
  /** Current stage label, omitted when the snapshot has no stage. */
  stage?: string;
  /** Byte progress, omitted when the snapshot does not report it. */
  progress?: string;
  /** Non-blocking snapshot diagnostic; never the terminal failure. */
  warning?: string;
};

export type OperationProgressViewProps = {
  locale: OperationProgressLocale;
  /** Omit until the first snapshot arrives; the view then shows loading copy. */
  status?: OperationProgressStatus;
  /** Terminal failure text that ends the download. */
  failure?: string;
  /** Snapshot read failure; offers reading the operation state again. */
  statusRead?: {
    message: string;
    alertRef?: RefObject<HTMLParagraphElement | null>;
    pending: boolean;
    onRetry: () => void;
  };
  /** Tool inventory refresh failure after a successful download. */
  toolsRefresh?: {
    message: string;
    alertRef?: RefObject<HTMLParagraphElement | null>;
    pending: boolean;
    onRetry: () => void;
  };
  /** Retry controls; the controller disables them while it is busy. */
  retryDownload?: { disabled: boolean; onPress: () => void };
  retryToolList?: { disabled: boolean; onPress: () => void };
  /** In-flight step; the controller owns which retry is running. */
  pending?: "status" | "download" | "tools";
};

const cardClass = "wrap-anywhere bg-[var(--accent-soft)] p-2 my-2";

function statusText(
  status: OperationProgressStatus | undefined,
  copy: (typeof messages)[OperationProgressLocale],
) {
  if (!status) return `${copy.download}${copy.loadingStatus}`;
  return [status.text, status.stage, status.progress, status.warning]
    .filter(Boolean)
    .join(" · ");
}

/**
 * One managed-tool operation card. The controller owns the follower, retries
 * and alert focus; this view renders the operation line, its terminal failure
 * and the supplied controls in the established order.
 */
export function OperationProgressView({
  locale,
  status,
  failure,
  statusRead,
  toolsRefresh,
  retryDownload,
  retryToolList,
  pending,
}: OperationProgressViewProps) {
  const copy = messages[locale];
  return (
    <div className={cardClass}>
      <p role="status">{statusText(status, copy)}</p>
      <SetupFeedbackView error={failure} />
      {statusRead && (
        <AppButton isDisabled={statusRead.pending} onPress={statusRead.onRetry}>
          {copy.retryStatus}
        </AppButton>
      )}
      <SetupFeedbackView
        pending={pending === "status" ? copy.loadingOperationStatus : null}
      />
      {retryDownload && (
        <AppButton
          isDisabled={retryDownload.disabled}
          onPress={retryDownload.onPress}
        >
          {copy.retryDownload}
        </AppButton>
      )}
      {retryToolList && (
        <AppButton
          isDisabled={retryToolList.disabled}
          onPress={retryToolList.onPress}
        >
          {copy.retryToolList}
        </AppButton>
      )}
      <SetupFeedbackView
        pending={
          pending === "tools"
            ? copy.loadingTools
            : pending === "download"
              ? copy.retryingDownload
              : null
        }
      />
      <SetupFeedbackView
        error={toolsRefresh?.message}
        errorRef={toolsRefresh?.alertRef}
      />
      <SetupFeedbackView
        error={statusRead?.message}
        errorRef={statusRead?.alertRef}
      />
    </div>
  );
}
