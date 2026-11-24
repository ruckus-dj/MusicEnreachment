import type { ReactNode } from "react";
import { useId } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";

export type LibraryDeletionDialogViewProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dismissable: boolean;
  title: ReactNode;
  children: ReactNode;
  actions: ReactNode;
};

export function LibraryDeletionDialogView({
  open,
  onOpenChange,
  dismissable,
  title,
  children,
  actions,
}: LibraryDeletionDialogViewProps) {
  const titleID = useId();
  return (
    <ModalOverlay
      isOpen={open}
      onOpenChange={onOpenChange}
      isDismissable={dismissable}
      className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/45 p-3 sm:p-6"
    >
      <Modal className="flex max-h-[min(70dvh,700px)] w-full max-w-xl flex-col overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_16px_48px_#0003]">
        <Dialog
          aria-labelledby={titleID}
          className="flex min-h-0 min-w-0 flex-1 flex-col wrap-anywhere outline-none"
        >
          <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-6">
            <Heading
              slot="title"
              id={titleID}
              className="text-xl font-semibold"
            >
              {title}
            </Heading>
            {children}
          </div>
          <div className="sticky bottom-0 z-10 flex shrink-0 flex-col-reverse flex-wrap gap-2 border-t border-[var(--line)] bg-[var(--surface)] p-4 sm:flex-row sm:justify-end sm:px-6">
            {actions}
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
