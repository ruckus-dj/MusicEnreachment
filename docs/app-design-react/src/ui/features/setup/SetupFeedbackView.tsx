import type { RefObject } from "react";

const errorClassName =
  "my-2 rounded-lg border border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] px-3.5 py-2.5 text-sm text-[var(--error)] wrap-anywhere";
const noticeClassName =
  "my-2 rounded-lg border border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] px-3.5 py-2.5 text-sm wrap-anywhere";
const pendingClassName = "my-2 text-sm text-[var(--muted)] wrap-anywhere";

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
