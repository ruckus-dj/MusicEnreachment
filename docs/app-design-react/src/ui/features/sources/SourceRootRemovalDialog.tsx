import { useLayoutEffect, useRef } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import { AppButton } from "../../components/AppButton";

export type SourceRootRemovalDialogProps = {
  locale: "en" | "ru";
  path: string;
  count: number;
  busy?: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: () => void;
};

const copy = {
  en: {
    dialog: "Remove source confirmation",
    title: (path: string) => `Remove source ${path}?`,
    count: (count: string) => `File records in inventory: ${count}.`,
    scope:
      "This removes this source's inventory, failed work and retained application-owned copies. Queued or running work must finish first. Host originals, library objects and current publications remain untouched.",
    recheck:
      "Before confirmation, the source inventory and any active processing are checked again.",
    cancel: "Cancel",
    confirm: "Confirm removal",
  },
  ru: {
    dialog: "Подтверждение удаления источника",
    title: (path: string) => `Удалить источник ${path}?`,
    count: (count: string) => `Записей о файлах в инвентаре: ${count}.`,
    scope:
      "Удаляются инвентарь источника, неудачные задания и сохранённые копии приложения. Задания в очереди или выполняющиеся должны завершиться. Исходные файлы на сервере, объекты медиатеки и текущие публикации не затрагиваются.",
    recheck:
      "Перед подтверждением инвентарь и активная обработка будут проверены повторно.",
    cancel: "Отмена",
    confirm: "Подтвердить удаление",
  },
} as const;

const overlayClassName =
  "react-aria-ModalOverlay ui-modal-overlay fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[rgb(0_0_0/0.45)] p-4";
const modalClassName =
  "react-aria-Modal ui-modal w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)]";
const dialogClassName =
  "react-aria-Dialog ui-dialog wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-base [&_p]:my-3 [&_p]:mb-[14px] [&_button]:mr-2 [&_button]:rounded-[var(--control-radius)] [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-1.5";

export function SourceRootRemovalDialog({
  locale,
  path,
  count,
  busy,
  error,
  onCancel,
  onConfirm,
}: SourceRootRemovalDialogProps) {
  const label = copy[locale];
  const errorNode = useRef<HTMLParagraphElement>(null);

  useLayoutEffect(() => {
    if (error) errorNode.current?.focus();
  }, [error]);

  const countText = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);

  return (
    <ModalOverlay
      className={overlayClassName}
      isOpen
      isDismissable={!busy}
      isKeyboardDismissDisabled={Boolean(busy)}
      onOpenChange={(open) => {
        if (!open && !busy) onCancel();
      }}
    >
      <Modal className={modalClassName}>
        <Dialog className={dialogClassName} aria-label={label.dialog}>
          <Heading
            slot="title"
            className="react-aria-Heading ui-heading min-w-0"
          >
            {label.title(path)}
          </Heading>
          <p>
            <code>{path}</code>
            <br />
            {label.count(countText)}
          </p>
          <p>{label.scope}</p>
          <p>{label.recheck}</p>
          {error ? (
            <p
              ref={errorNode}
              tabIndex={-1}
              role="alert"
              className="text-[var(--error)]"
            >
              {error}
            </p>
          ) : null}
          <AppButton autoFocus isDisabled={busy} onPress={onCancel}>
            {label.cancel}
          </AppButton>
          <AppButton isDisabled={busy} onPress={onConfirm}>
            {label.confirm}
          </AppButton>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
