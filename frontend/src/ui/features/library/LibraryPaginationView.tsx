import { Button, Link } from "react-aria-components";

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

const controlClass =
  "inline-flex min-h-9 items-center justify-center rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-1.5 text-sm font-medium text-[var(--ink)] no-underline data-[disabled]:opacity-45 data-[hovered]:border-[var(--control)] data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

type PageAction = { label: string; disabled: boolean } & (
  | { href: string }
  | { onPress: () => void }
);

function PageControl({ action }: { action: PageAction }) {
  if ("href" in action) {
    return (
      <Link
        className={`${controlClass} inline-flex no-underline`}
        href={action.disabled ? undefined : action.href}
        isDisabled={action.disabled}
      >
        {action.label}
      </Link>
    );
  }
  return (
    <Button
      className={controlClass}
      isDisabled={action.disabled}
      onPress={action.onPress}
    >
      {action.label}
    </Button>
  );
}

export function LibraryPaginationControls({
  label,
  status,
  previous,
  next,
}: {
  label: string;
  status: string;
  previous: PageAction;
  next: PageAction;
}) {
  return (
    <nav
      className="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm"
      aria-label={label}
    >
      <span className="text-[var(--muted)]" role="status">
        {status}
      </span>
      <div className="flex flex-wrap gap-2">
        <PageControl action={previous} />
        <PageControl action={next} />
      </div>
    </nav>
  );
}

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
    <LibraryPaginationControls
      label={copy.label}
      status={copy.status(page, totalPages, totalItems)}
      previous={{
        label: copy.previous,
        disabled: !hasPrevious,
        onPress: () => onPageChange(page - 1),
      }}
      next={{
        label: copy.next,
        disabled: !hasNext,
        onPress: () => onPageChange(page + 1),
      }}
    />
  );
}
