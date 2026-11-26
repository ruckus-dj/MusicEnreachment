import type { ComponentProps } from "react";
import { Dialog, Modal, ModalOverlay } from "react-aria-components";

export function ActionModal({
  children,
  ...props
}: Pick<
  ComponentProps<typeof ModalOverlay>,
  "isOpen" | "onOpenChange" | "isDismissable"
> & {
  children: ComponentProps<typeof Modal>["children"];
}) {
  return (
    <ModalOverlay
      {...props}
      className="fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[rgb(0_0_0/0.45)] p-4"
    >
      <Modal className="w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-6 text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)]">
        {children}
      </Modal>
    </ModalOverlay>
  );
}

export function ActionDialog({
  className,
  ...props
}: Omit<ComponentProps<typeof Dialog>, "className"> & {
  className?: string;
}) {
  return (
    <Dialog
      {...props}
      className={`wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-lg [&_[slot=title]]:font-semibold [&_p]:my-3 [&_p]:text-sm [&_p]:text-[var(--muted)] ${className ?? ""}`}
    />
  );
}
