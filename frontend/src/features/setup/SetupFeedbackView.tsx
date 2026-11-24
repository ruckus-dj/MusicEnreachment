import type { RefObject } from "react";

const errorClassName =
  "my-2 border-l-[3px] border-[var(--error)] bg-[var(--error-soft)] px-3 py-[10px] text-[var(--error)] wrap-anywhere";
const noticeClassName =
  "my-2 rounded bg-[var(--warning-soft)] px-3 py-[10px] wrap-anywhere";
const pendingClassName = "my-2 text-[var(--muted)] wrap-anywhere";

export interface SetupFeedbackViewProps {
  error?: string;
  errorRef?: RefObject<HTMLParagraphElement | null>;
  notice?: string;
  /** Pass null to keep a live region mounted before work starts. */
  pending?: string | null;
}

export function SetupFeedbackView({
  error,
  errorRef,
  notice,
  pending,
}: SetupFeedbackViewProps) {
  return (
    <>
      {error && (
        <p
          ref={errorRef}
          tabIndex={-1}
          role="alert"
          data-setup-feedback
          className={errorClassName}
        >
          {error}
        </p>
      )}
      {notice && (
        <p
          role="status"
          aria-live="polite"
          data-setup-feedback
          className={noticeClassName}
        >
          {notice}
        </p>
      )}
      {pending !== undefined && (
        <p
          role="status"
          data-setup-feedback
          className={pending ? pendingClassName : "sr-only"}
        >
          {pending}
        </p>
      )}
    </>
  );
}
