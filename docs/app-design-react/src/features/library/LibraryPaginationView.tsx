import { Button } from "react-aria-components";

const copies = {
  en: {
    label: "Library pages",
    status: (page: number, pages: number, total: number) =>
      `Page ${page} of ${pages} · Total: ${total}`,
    previous: "Previous page",
    next: "Next page",
  },
  ru: {
    label: "Страницы медиатеки",
    status: (page: number, pages: number, total: number) =>
      `Страница ${page} из ${pages} · Всего: ${total}`,
    previous: "Предыдущая страница",
    next: "Следующая страница",
  },
} as const;

const button =
  "min-h-9 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-[10px] py-[6px] text-[var(--ink)] data-[disabled]:opacity-45 data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";

export function LibraryPaginationView({
  locale,
  page,
  totalPages,
  totalItems,
  hasPrevious,
  hasNext,
  onPageChange,
}: {
  locale: "en" | "ru";
  page: number;
  totalPages: number;
  totalItems: number;
  hasPrevious: boolean;
  hasNext: boolean;
  onPageChange: (page: number) => void;
}) {
  const copy = copies[locale];
  return (
    <nav
      className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs"
      aria-label={copy.label}
    >
      <span className="text-[var(--muted)]" role="status">
        {copy.status(page, totalPages, totalItems)}
      </span>
      <div className="flex flex-wrap gap-2">
        <Button
          className={button}
          isDisabled={!hasPrevious}
          onPress={() => onPageChange(page - 1)}
        >
          {copy.previous}
        </Button>
        <Button
          className={button}
          isDisabled={!hasNext}
          onPress={() => onPageChange(page + 1)}
        >
          {copy.next}
        </Button>
      </div>
    </nav>
  );
}
