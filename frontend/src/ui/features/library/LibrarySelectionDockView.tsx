import type { RefObject } from "react";
import { Button } from "react-aria-components";
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
    limit:
      "Для предпросмотра можно выбрать до 100 позиций. Откройте список и снимите лишние отметки.",
  },
} as const;

const dock =
  "fixed bottom-[calc(16px+env(safe-area-inset-bottom))] right-6 z-[60] grid w-[min(680px,calc(100vw-48px))] gap-2 rounded-lg border border-[color-mix(in_srgb,var(--accent)_38%,var(--line))] bg-[var(--accent-soft)] px-[14px] py-3 text-[var(--ink)] shadow-[0_12px_36px_color-mix(in_srgb,var(--ink)_18%,transparent)] max-[600px]:inset-x-2 max-[600px]:bottom-[env(safe-area-inset-bottom)] max-[600px]:w-auto max-[600px]:gap-[7px] max-[600px]:p-[10px]";

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
      <span role="status">
        {text.count(count)}
        {hiddenCount ? ` · ${text.hidden(hiddenCount)}` : ""}
      </span>
      {overPreviewLimit && <p className="text-xs">{text.limit}</p>}
      <div className="flex flex-wrap gap-2 max-[600px]:grid max-[600px]:grid-cols-2 max-[600px]:text-xs">
        {review && <LibrarySelectionReviewButton {...review} />}
        <Button
          className={`${librarySelectionButtonClass} border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90 data-[disabled]:opacity-45`}
          isDisabled={overPreviewLimit}
          onPress={onPreview}
        >
          {text.preview}
        </Button>
        {onDelete && (
          <Button className={librarySelectionButtonClass} onPress={onDelete}>
            {text.remove}
          </Button>
        )}
        <Button className={librarySelectionButtonClass} onPress={onClear}>
          {text.clear}
        </Button>
      </div>
    </section>
  );
}
