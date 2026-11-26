import { useId, useLayoutEffect, useRef } from "react";
import { settingsToolsMoveTitle } from "./SettingsToolsMoveEditor";
import {
  SettingsToolsMoveReview,
  type SettingsToolsMoveReviewProps,
} from "./SettingsToolsMoveReview";

// Native modal chrome for the frozen move review. Uses the application's
// dialog semantics: showModal, first actionable control focus, and Escape/Tab
// handling scoped to this dialog.
const controlsSelector = "button:not(:disabled), input:not(:disabled)";

export type SettingsToolsMoveReviewDialogProps =
  SettingsToolsMoveReviewProps & {
    /** Element that opened the dialog; focused again when the dialog unmounts. */
    returnFocusTo?: HTMLElement | null;
  };

export function SettingsToolsMoveReviewDialog({
  returnFocusTo,
  ...review
}: SettingsToolsMoveReviewDialogProps) {
  const title = useId();
  const dialog = useRef<HTMLDialogElement>(null);

  useLayoutEffect(() => {
    const node = dialog.current;
    if (!node || node.open) return;
    node.showModal();
    const confirm = node.querySelector<HTMLElement>(
      "[data-settings-move-confirm]:not(:disabled)",
    );
    const first = node.querySelector<HTMLElement>(controlsSelector);
    (confirm ?? first)?.focus();
    return () => {
      if (node.open) node.close();
      if (returnFocusTo?.isConnected) returnFocusTo.focus();
    };
  }, [returnFocusTo]);

  return (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={title}
      className="m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] gap-3 overflow-auto overscroll-contain rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)] backdrop:bg-[rgb(0_0_0/0.45)] open:grid"
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        review.onDismiss();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("dialog") !== event.currentTarget
        ) {
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          review.onDismiss();
          return;
        }
        if (event.key !== "Tab") return;
        const controls =
          event.currentTarget.querySelectorAll<HTMLElement>(controlsSelector);
        if (controls.length === 0) return;
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        } else if (!event.currentTarget.contains(document.activeElement)) {
          event.preventDefault();
          (event.shiftKey ? last : first).focus();
        }
      }}
    >
      <h2 id={title} className="m-0 wrap-anywhere text-base font-semibold">
        {settingsToolsMoveTitle(review.locale)}
      </h2>
      <SettingsToolsMoveReview {...review} />
    </dialog>
  );
}
