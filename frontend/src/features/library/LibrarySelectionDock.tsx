import { useId, useRef, useState } from "react";
import {
  Button,
  Dialog,
  Heading,
  Modal,
  ModalOverlay,
} from "react-aria-components";
import { useI18n } from "../../i18n/I18nProvider";
import {
  LibrarySelectionReviewButton,
  LibrarySelectionReviewView,
} from "./LibrarySelectionReviewView";

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
    preview: "Preview publication",
    clear: "Clear selection",
    limit:
      "Up to 100 positions can be previewed. Review the selection to remove positions.",
  },
  ru: {
    label: "Выбранные позиции",
    count: "Выбрано позиций",
    hidden: "Вне этой страницы",
    preview: "Предпросмотр публикации",
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
  const reviewTrigger = useRef<HTMLButtonElement>(null);
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
            <LibrarySelectionReviewButton
              triggerRef={reviewTrigger}
              onOpen={() => setReviewOpen(true)}
            />
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
      <LibrarySelectionReviewView
        isOpen={reviewOpen}
        triggerRef={reviewTrigger}
        rows={positions.map((position) => ({
          id: libraryPositionKey(position),
          title: position.title,
          releaseTitle: position.release_title,
        }))}
        onClose={() => setReviewOpen(false)}
        onDeselect={(row) => {
          const position = positions.find(
            (item) => libraryPositionKey(item) === row.id,
          );
          if (position) onDeselect(position);
        }}
      />
    </>
  );
}
