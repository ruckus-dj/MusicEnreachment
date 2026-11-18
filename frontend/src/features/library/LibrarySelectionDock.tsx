import { useId, useRef, useState } from "react";
import {
  Button,
  Dialog,
  Heading,
  Modal,
  ModalOverlay,
} from "react-aria-components";
import { useI18n } from "../../i18n/I18nProvider";

export interface SelectedLibraryPosition {
  release_id: string;
  position_id: string;
  title: string;
  release_title: string;
}

export const maxLibraryPreviewPositions = 100;

export const libraryPositionKey = (
  position: Pick<SelectedLibraryPosition, "release_id" | "position_id">,
) => `${position.release_id}:${position.position_id}`;

const copyByLocale = {
  en: {
    label: "Selected positions",
    count: "Selected positions",
    hidden: "Not on this page",
    review: "Review selection",
    deselect: "Deselect",
    preview: "Preview publication",
    empty: "Nothing selected.",
    close: "Close",
    clear: "Clear selection",
    limit:
      "Up to 100 positions can be previewed. Review the selection to remove positions.",
  },
  ru: {
    label: "Выбранные позиции",
    count: "Выбрано позиций",
    hidden: "Вне этой страницы",
    review: "Проверить выбор",
    deselect: "Снять выбор",
    preview: "Предпросмотр публикации",
    empty: "Ничего не выбрано.",
    close: "Закрыть",
    clear: "Снять выбор",
    limit:
      "Для предпросмотра можно выбрать до 100 позиций. Откройте список и снимите лишние отметки.",
  },
};

const dockClass =
  "fixed bottom-[calc(16px+env(safe-area-inset-bottom))] right-6 z-[60] grid w-[min(680px,calc(100vw-48px))] gap-2 rounded-lg border border-[color-mix(in_srgb,var(--accent)_38%,var(--line))] bg-[var(--accent-soft)] px-[14px] py-3 text-[var(--ink)] shadow-[0_12px_36px_color-mix(in_srgb,var(--ink)_18%,transparent)] max-[600px]:inset-x-2 max-[600px]:bottom-[env(safe-area-inset-bottom)] max-[600px]:w-auto max-[600px]:gap-[7px] max-[600px]:p-[10px]";

export const librarySelectionButtonClass =
  "min-h-9 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-[10px] py-[6px] text-[var(--ink)] data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";

export function LibrarySelectionDialog({
  title,
  isOpen,
  onOpenChange,
  children,
}: {
  title: string;
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  children: React.ReactNode;
}) {
  const heading = useId();
  return (
    <ModalOverlay
      isOpen={isOpen}
      isDismissable
      onOpenChange={onOpenChange}
      className="fixed inset-0 z-[80] grid place-items-center overflow-y-auto bg-black/45 p-3"
    >
      <Modal className="w-full max-w-[680px] rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] shadow-xl">
        <Dialog aria-labelledby={heading} className="min-w-0 outline-none">
          <Heading slot="title" id={heading} className="text-lg font-semibold">
            {title}
          </Heading>
          {children}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

export function LibrarySelectionDock({
  positions,
  hiddenCount,
  onDeselect,
  onClear,
  onPreview,
}: {
  positions: readonly SelectedLibraryPosition[];
  hiddenCount: number | undefined;
  onDeselect: (position: SelectedLibraryPosition) => void;
  onClear: () => void;
  onPreview: (positions: SelectedLibraryPosition[]) => void;
}) {
  const { locale } = useI18n();
  const copy = copyByLocale[locale];
  const [reviewOpen, setReviewOpen] = useState(false);
  const closeButton = useRef<HTMLButtonElement>(null);
  function closeReview(open: boolean) {
    setReviewOpen(open);
    if (!open && positions.length === 0) {
      requestAnimationFrame(() =>
        document.querySelector<HTMLElement>("main h1")?.focus(),
      );
    }
  }
  return (
    <>
      {positions.length > 0 && (
        <section className={dockClass} aria-label={copy.label}>
          <span role="status">
            {copy.count}: {positions.length}
            {!!hiddenCount && ` · ${copy.hidden}: ${hiddenCount}`}
          </span>
          {positions.length > maxLibraryPreviewPositions && (
            <p className="text-xs">{copy.limit}</p>
          )}
          <div className="flex flex-wrap gap-2 max-[600px]:grid max-[600px]:grid-cols-2 max-[600px]:text-xs">
            <Button
              className={`${librarySelectionButtonClass} max-[600px]:col-span-full`}
              onPress={() => setReviewOpen(true)}
            >
              {copy.review}
            </Button>
            <Button
              className={`${librarySelectionButtonClass} border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90`}
              isDisabled={positions.length > maxLibraryPreviewPositions}
              onPress={() => onPreview([...positions])}
            >
              {copy.preview}
            </Button>
            <Button
              className={librarySelectionButtonClass}
              onPress={() => {
                onClear();
                document.querySelector<HTMLElement>("main h1")?.focus();
              }}
            >
              {copy.clear}
            </Button>
          </div>
        </section>
      )}
      <LibrarySelectionDialog
        title={copy.review}
        isOpen={reviewOpen}
        onOpenChange={closeReview}
      >
        {positions.length === 0 ? (
          <p className="my-3">{copy.empty}</p>
        ) : (
          <ul className="my-3 max-h-[52dvh] list-none overflow-auto p-0">
            {positions.map((position) => (
              <li
                key={libraryPositionKey(position)}
                className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] py-2"
              >
                <span className="min-w-0 wrap-anywhere">
                  <strong>{position.title}</strong> · {position.release_title}
                </span>
                <Button
                  className={librarySelectionButtonClass}
                  aria-label={`${copy.deselect}: ${position.title} · ${position.release_title}`}
                  onPress={(event) => {
                    const row = event.target.closest("li");
                    const adjacent =
                      row?.nextElementSibling ?? row?.previousElementSibling;
                    (
                      adjacent?.querySelector("button") ?? closeButton.current
                    )?.focus();
                    onDeselect(position);
                  }}
                >
                  {copy.deselect}
                </Button>
              </li>
            ))}
          </ul>
        )}
        <div className="mt-3 flex justify-end">
          <Button
            ref={closeButton}
            className={librarySelectionButtonClass}
            onPress={() => closeReview(false)}
          >
            {copy.close}
          </Button>
        </div>
      </LibrarySelectionDialog>
    </>
  );
}
