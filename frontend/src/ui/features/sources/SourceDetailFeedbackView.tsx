import { useEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

const messages = {
  en: {
    requestRunning: "Request in progress…",
    loading: "Loading source…",
    retry: "Retry loading",
  },
  ru: {
    requestRunning: "Выполняется запрос…",
    loading: "Загрузка каталога…",
    retry: "Повторить загрузку",
  },
} as const;

export type SourceDetailFeedbackViewProps = {
  locale: "en" | "ru";
  requestRunning?: boolean;
  loading?: boolean;
  error?: string;
  retryDisabled?: boolean;
  onRetry: () => void;
};

// Read/delete notices for the selected source: a running request, the initial
// load, and a read failure whose alert takes focus with a retry action.
export function SourceDetailFeedbackView({
  locale,
  requestRunning,
  loading,
  error,
  retryDisabled,
  onRetry,
}: SourceDetailFeedbackViewProps) {
  const copy = messages[locale];
  const alert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  return (
    <>
      {requestRunning && <p role="status">{copy.requestRunning}</p>}
      {loading && <p role="status">{copy.loading}</p>}
      {error && (
        <>
          <p
            ref={alert}
            tabIndex={-1}
            role="alert"
            className="rounded border border-[var(--line)] bg-[var(--error-soft)] p-2 text-[var(--error)]"
          >
            {error}
          </p>
          <AppButton isDisabled={retryDisabled} onPress={onRetry}>
            {copy.retry}
          </AppButton>
        </>
      )}
    </>
  );
}
