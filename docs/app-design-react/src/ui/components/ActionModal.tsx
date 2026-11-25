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
      className="fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-4"
    >
      <Modal className="w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_16px_48px_#0003]">
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
      className={`wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-base [&_p]:my-3 [&_p]:mb-[14px] [&_button]:mr-2 [&_button]:rounded-[var(--control-radius)] [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-1.5 ${className ?? ""}`}
    />
  );
}
