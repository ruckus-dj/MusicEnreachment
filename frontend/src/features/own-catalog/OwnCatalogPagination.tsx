import { Button } from "react-aria-components";
import type { Locale } from "../../i18n/locale";
import { secondary } from "../local-release/localReleaseUi";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";

export function OwnCatalogPagination({
  locale,
  page,
  totalPages,
  totalItems,
  busy,
  onPageChange,
}: {
  locale: Locale;
  page: number;
  totalPages: number;
  totalItems: number;
  busy?: boolean;
  onPageChange: (page: number) => void;
}) {
  const m = (
    key: OwnCatalogMessage,
    values?: Record<string, string | number>,
  ) => ownCatalogMessage(locale, key, values);
  return (
    <nav
      className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] pt-3 text-xs"
      aria-label={m("pages")}
    >
      <span role="status" className="text-[var(--muted)]">
        {m("pageStatus", { page, pages: totalPages })} ·{" "}
        {m("totalItems", { count: totalItems })}
      </span>
      <div className="flex gap-2">
        <Button
          className={secondary}
          isDisabled={Boolean(busy) || page <= 1}
          onPress={() => onPageChange(page - 1)}
        >
          {m("previousPage")}
        </Button>
        <Button
          className={secondary}
          isDisabled={Boolean(busy) || page >= totalPages}
          onPress={() => onPageChange(page + 1)}
        >
          {m("nextPage")}
        </Button>
      </div>
    </nav>
  );
}
