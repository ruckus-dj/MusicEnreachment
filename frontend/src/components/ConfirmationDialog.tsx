import * as AlertDialog from "@radix-ui/react-alert-dialog";
import { useRef } from "react";

export function ConfirmationDialog({
  open,
  title,
  description,
  confirmLabel,
  busyLabel,
  busy,
  onCancel,
  onConfirm,
  onAfterCloseFocus,
}: {
  readonly open: boolean;
  readonly title: string;
  readonly description: string;
  readonly confirmLabel: string;
  readonly busyLabel: string;
  readonly busy: boolean;
  readonly onCancel: () => void;
  readonly onConfirm: () => void;
  readonly onAfterCloseFocus: () => void;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);

  return (
    <AlertDialog.Root open={open}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="confirmation-backdrop" />
        <AlertDialog.Content
          className="confirmation-dialog"
          aria-busy={busy}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            cancelRef.current?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            onAfterCloseFocus();
          }}
          onEscapeKeyDown={(event) => {
            event.preventDefault();
            if (!busy) onCancel();
          }}
        >
          <div className="confirmation-dialog-marker" aria-hidden="true" />
          <p className="eyebrow">Подтвердите действие</p>
          <AlertDialog.Title className="confirmation-dialog-title">{title}</AlertDialog.Title>
          <AlertDialog.Description className="confirmation-dialog-description">
            {description}
          </AlertDialog.Description>
          <div className="confirmation-dialog-actions">
            <AlertDialog.Cancel asChild>
              <button
                ref={cancelRef}
                type="button"
                className="secondary"
                disabled={busy}
                onClick={onCancel}
              >
                Отмена
              </button>
            </AlertDialog.Cancel>
            <AlertDialog.Action asChild>
              <button type="button" className="primary danger" disabled={busy} onClick={onConfirm}>
                {busy ? busyLabel : confirmLabel}
              </button>
            </AlertDialog.Action>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  );
}
