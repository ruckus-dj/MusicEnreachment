import type { RefObject } from "react";
import { Button } from "react-aria-components";
import { AppActionToolbar } from "../../components/AppToolbar";
import { RowActionMenu } from "../../components/RowActionMenu";
import {
  librarySelectionButtonClass,
  maxLibraryPreviewPositions,
} from "./LibrarySelectionPreviewView";
import { LibrarySelectionReviewButton } from "./LibrarySelectionReviewView";

const copy = {
  en: {
    label: "Selected positions",
    count: (count: number) => `Selected positions: ${count}`,
    hidden: (count: number) => `Not on this page: ${count}`,
    preview: "Preview publication",
    clear: "Clear selection",
    remove: "Delete publications",
    moreActions: "More selected position actions",
    limit:
      "Up to 100 positions can be previewed. Review the selection to remove positions.",
  },
  ru: {
    label: "Выбранные позиции",
    count: (count: number) => `Выбрано позиций: ${count}`,
    hidden: (count: number) => `Вне этой страницы: ${count}`,
    preview: "Предпросмотр публикации",
    clear: "Снять выбор",
    remove: "Удалить публикации",
    moreActions: "Другие действия с выбранными позициями",
    limit:
      "Для предпросмотра можно выбрать до 100 позиций. Откройте список и снимите лишние отметки.",
  },
} as const;

const dock =
  "fixed bottom-[calc(16px+env(safe-area-inset-bottom))] right-6 z-[60] grid w-[min(680px,calc(100vw-48px))] gap-2.5 rounded-xl border border-[var(--line)] bg-[var(--surface)] px-4 py-3 text-[var(--ink)] shadow-[0_16px_40px_rgb(0_0_0/0.16)] max-[600px]:inset-x-2 max-[600px]:bottom-[env(safe-area-inset-bottom)] max-[600px]:w-auto max-[600px]:gap-2 max-[600px]:p-3";

export function LibrarySelectionDockView({
  locale,
  count,
  hiddenCount,
  review,
  onPreview,
  onDelete,
  onClear,
}: {
  locale: "en" | "ru";
  count: number;
  hiddenCount?: number;
  review?: {
    triggerRef: RefObject<HTMLButtonElement | null>;
    onOpen: () => void;
  };
  onPreview: () => void;
  onDelete?: () => void;
  onClear: () => void;
}) {
  const text = copy[locale];
  const overPreviewLimit = count > maxLibraryPreviewPositions;
  return (
    <section className={dock} aria-label={text.label}>
      <span role="status" className="text-sm font-medium">
        {text.count(count)}
        {hiddenCount ? ` · ${text.hidden(hiddenCount)}` : ""}
      </span>
      {overPreviewLimit && <p className="text-xs">{text.limit}</p>}
      <AppActionToolbar
        label={text.label}
        className="max-[600px]:grid max-[600px]:grid-cols-2"
      >
        {review && <LibrarySelectionReviewButton {...review} />}
        <Button
          className={`${librarySelectionButtonClass} border-transparent! bg-[var(--button)]! text-[var(--button-text)]! hover:bg-[var(--button-hover)]! data-[disabled]:opacity-45`}
          isDisabled={overPreviewLimit}
          onPress={onPreview}
        >
          {text.preview}
        </Button>
        {onDelete && (
          <RowActionMenu
            label={text.moreActions}
            size="default"
            actions={[
              {
                id: "delete-publications",
                label: text.remove,
                isDestructive: true,
                onAction: () => onDelete(),
              },
            ]}
          />
        )}
        <Button
          className={librarySelectionButtonClass}
          onPress={() => {
            onClear();
            document.querySelector<HTMLElement>("main h1")?.focus();
          }}
        >
          {text.clear}
        </Button>
      </AppActionToolbar>
    </section>
  );
}
