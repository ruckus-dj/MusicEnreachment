import { type RefObject, useEffect, useId, useRef } from "react";
import {
  Button,
  Dialog,
  Heading,
  Modal,
  ModalOverlay,
  type PressEvent,
} from "react-aria-components";
import { useI18n } from "../../i18n/I18nProvider";

export interface LibrarySelectionReviewRow {
  id: string;
  title: string;
  releaseTitle: string;
}

const librarySelectionReviewMessages = {
  en: {
    title: "Review selection",
    deselect: "Deselect",
    empty: "Nothing selected.",
    close: "Close",
  },
  ru: {
    title: "Проверить выбор",
    deselect: "Снять выбор",
    empty: "Ничего не выбрано.",
    close: "Закрыть",
  },
} as const;

const styles = {
  overlay:
    "fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-4",
  modal:
    "w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_16px_48px_#0003]",
  dialog: "wrap-anywhere outline-none",
  heading: "mb-2 mt-0 text-base",
  list: "my-3 grid max-h-[52dvh] list-none gap-2 overflow-auto p-0",
  row: "flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] py-2",
  label: "min-w-0 wrap-anywhere",
  empty: "my-2 mb-[14px]",
  footer: "mt-3 flex flex-wrap justify-end gap-2",
  button:
    "min-h-9 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-[10px] py-[6px] text-[var(--ink)] data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]",
} as const;

function focusFallback() {
  const heading = document.querySelector<HTMLElement>("main h1");
  if (!heading) {
    document.querySelector<HTMLElement>("main")?.focus();
    return;
  }
  if (!heading.hasAttribute("tabindex")) heading.setAttribute("tabindex", "-1");
  heading.focus();
}

export function LibrarySelectionReviewButton({
  triggerRef,
  onOpen,
}: {
  triggerRef: RefObject<HTMLButtonElement | null>;
  onOpen: () => void;
}) {
  const { locale } = useI18n();
  return (
    <Button
      ref={triggerRef}
      className={`${styles.button} max-[600px]:col-span-full`}
      onPress={onOpen}
    >
      {librarySelectionReviewMessages[locale].title}
    </Button>
  );
}

export function LibrarySelectionReviewView({
  isOpen,
  rows,
  triggerRef,
  onClose,
  onDeselect,
}: {
  isOpen: boolean;
  rows: readonly LibrarySelectionReviewRow[];
  triggerRef: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
  onDeselect: (row: LibrarySelectionReviewRow) => void;
}) {
  const { locale } = useI18n();
  const copy = librarySelectionReviewMessages[locale];
  const headingId = useId();
  const closeButton = useRef<HTMLButtonElement>(null);
  const wasOpen = useRef(false);

  useEffect(() => {
    if (isOpen) {
      wasOpen.current = true;
      return;
    }
    if (!wasOpen.current) return;
    wasOpen.current = false;
    const frame = requestAnimationFrame(() => {
      if (triggerRef.current) triggerRef.current.focus();
      else focusFallback();
    });
    return () => cancelAnimationFrame(frame);
  }, [isOpen, triggerRef]);

  function deselect(event: PressEvent, row: LibrarySelectionReviewRow) {
    const item = (event.target as Element).closest("li");
    const adjacent = item?.nextElementSibling ?? item?.previousElementSibling;
    (
      adjacent?.querySelector<HTMLButtonElement>("button") ??
      closeButton.current
    )?.focus();
    onDeselect(row);
  }

  return (
    <ModalOverlay
      isOpen={isOpen}
      isDismissable
      onOpenChange={(open) => !open && onClose()}
      className={styles.overlay}
    >
      <Modal className={styles.modal}>
        <Dialog aria-labelledby={headingId} className={styles.dialog}>
          <Heading slot="title" id={headingId} className={styles.heading}>
            {copy.title}
          </Heading>
          {rows.length === 0 ? (
            <p className={styles.empty}>{copy.empty}</p>
          ) : (
            <ul className={styles.list}>
              {rows.map((row) => (
                <li key={row.id} className={styles.row}>
                  <span className={styles.label}>
                    <strong>{row.title}</strong> · {row.releaseTitle}
                  </span>
                  <Button
                    className={styles.button}
                    aria-label={`${copy.deselect}: ${row.title} · ${row.releaseTitle}`}
                    onPress={(event) => deselect(event, row)}
                  >
                    {copy.deselect}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <div className={styles.footer}>
            <Button
              ref={closeButton}
              className={styles.button}
              onPress={onClose}
            >
              {copy.close}
            </Button>
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
