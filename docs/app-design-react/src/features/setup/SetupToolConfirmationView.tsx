import { useEffect, useRef } from "react";
import {
  Dialog as AriaDialog,
  Heading as AriaHeading,
  Modal as AriaModal,
  ModalOverlay as AriaModalOverlay,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";

export function SetupToolConfirmationView({
  title,
  packageName,
  release,
  executables,
  conflicts,
  confirmLabel,
  cancelLabel,
  busy = false,
  busyLabel,
  error,
  onConfirm,
  onCancel,
}: {
  title: string;
  packageName: string;
  release: string;
  executables: readonly string[];
  conflicts?: readonly string[];
  confirmLabel: string;
  cancelLabel: string;
  busy?: boolean;
  busyLabel?: string;
  error?: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const alert = useRef<HTMLParagraphElement>(null);
  const progress = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (error) alert.current?.focus();
    else if (busy) progress.current?.focus();
  }, [busy, error]);
  return (
    <AriaModalOverlay
      isOpen
      isDismissable={!busy}
      isKeyboardDismissDisabled={busy}
      onOpenChange={(open) => {
        if (!open && !busy) onCancel();
      }}
      className="react-aria-ModalOverlay ui-modal-overlay fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-4"
    >
      <AriaModal className="react-aria-Modal ui-modal w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_16px_48px_#0003]">
        <AriaDialog className="react-aria-Dialog ui-dialog wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-base [&_p]:my-3 [&_p]:mb-[14px] [&_button]:mr-2 [&_button]:rounded-[var(--control-radius)] [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-1.5">
          <AriaHeading
            slot="title"
            className="react-aria-Heading ui-heading min-w-0"
          >
            {title}
          </AriaHeading>
          <p>
            {packageName} · {release}
          </p>
          <p>{executables.join(", ")}</p>
          {conflicts && conflicts.length > 0 && (
            <ul className="my-1 list-disc pl-5">
              {conflicts.map((path) => (
                <li key={path} className="wrap-anywhere">
                  {path}
                </li>
              ))}
            </ul>
          )}
          {busy && busyLabel && (
            <p
              ref={progress}
              role="status"
              tabIndex={-1}
              className="m-0 text-[var(--muted)]"
            >
              {busyLabel}
            </p>
          )}
          {error && (
            <p
              ref={alert}
              role="alert"
              tabIndex={-1}
              className="m-0 wrap-anywhere text-[var(--error)]"
            >
              {error}
            </p>
          )}
          <AppButton isDisabled={busy} onPress={onConfirm}>
            {confirmLabel}
          </AppButton>
          <AppButton autoFocus isDisabled={busy} onPress={onCancel}>
            {cancelLabel}
          </AppButton>
        </AriaDialog>
      </AriaModal>
    </AriaModalOverlay>
  );
}
