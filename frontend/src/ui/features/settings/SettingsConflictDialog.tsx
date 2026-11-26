import type { RefObject } from "react";
import { useCallback, useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

export type SettingsConflictAction = {
  label: string;
  onPress: () => void;
  isDisabled?: boolean;
};

export type SettingsConflictDialogProps = {
  title: string;
  message: string;
  details?: {
    saved: string;
    draft: string;
  };
  busy?: boolean;
  dismissOnBackdrop?: boolean;
  onDismiss: () => void;
  actions: {
    confirm: SettingsConflictAction;
    cancel: SettingsConflictAction;
  };
  dialogRef?: RefObject<HTMLDialogElement | null>;
};

const dialogClassName =
  "m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] gap-3 overflow-auto rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)] backdrop:bg-[rgb(0_0_0/0.45)] open:grid";

const focusableSelector =
  'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])';

export function SettingsConflictDialog({
  title,
  message,
  details,
  busy,
  dismissOnBackdrop,
  onDismiss,
  actions,
  dialogRef,
}: SettingsConflictDialogProps) {
  const titleID = useId();
  const messageID = `${titleID}-message`;
  const dialog = useRef<HTMLDialogElement>(null);

  const assignDialog = useCallback(
    (node: HTMLDialogElement | null) => {
      dialog.current = node;
      if (dialogRef) dialogRef.current = node;
    },
    [dialogRef],
  );

  useLayoutEffect(() => {
    const current = dialog.current;
    if (!current || current.open) return;
    current.showModal();
    current.querySelector<HTMLElement>(focusableSelector)?.focus();
    return () => {
      if (current.open) current.close();
    };
  }, []);

  const dismiss = () => {
    if (!busy) onDismiss();
  };

  return (
    <dialog
      ref={assignDialog}
      aria-modal="true"
      aria-labelledby={titleID}
      aria-describedby={messageID}
      className={dialogClassName}
      onClick={(event) => {
        if (!dismissOnBackdrop || event.target !== event.currentTarget) return;
        const { left, right, top, bottom } =
          event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < left ||
          event.clientX >= right ||
          event.clientY < top ||
          event.clientY >= bottom
        )
          dismiss();
      }}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        dismiss();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("dialog") !== event.currentTarget
        )
          return;
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          dismiss();
          return;
        }
        if (event.key !== "Tab") return;
        const controls =
          event.currentTarget.querySelectorAll<HTMLElement>(focusableSelector);
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
      <h2 id={titleID} className="m-0 text-base font-semibold">
        {title}
      </h2>
      <p id={messageID} className="m-0">
        {message}
      </p>
      {details && (
        <p className="m-0 wrap-anywhere">
          <code>{details.saved}</code>
          {" → "}
          <code>{details.draft}</code>
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <AppButton
          isDisabled={busy || actions.confirm.isDisabled}
          onPress={actions.confirm.onPress}
        >
          {actions.confirm.label}
        </AppButton>
        <AppButton
          isDisabled={busy || actions.cancel.isDisabled}
          onPress={actions.cancel.onPress}
        >
          {actions.cancel.label}
        </AppButton>
      </div>
    </dialog>
  );
}
