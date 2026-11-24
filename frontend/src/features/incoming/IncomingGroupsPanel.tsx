import { type ReactNode, useId } from "react";

const copy = {
  en: {
    title: "Incoming groups",
    empty: "There are no incoming groups yet.",
  },
  ru: {
    title: "Входящие группы",
    empty: "Входящих групп пока нет.",
  },
} as const;

type Locale = keyof typeof copy;

export function incomingGroupsCount(locale: Locale, count: number) {
  const category = new Intl.PluralRules(locale).select(count);
  if (locale === "en") {
    return `${count} ${category === "one" ? "group" : "groups"} · source queue`;
  }
  const noun =
    category === "one" ? "Группа" : category === "few" ? "Группы" : "Групп";
  return `${noun}: ${count} · очередь источников`;
}

export function incomingGroupsEmpty(locale: Locale) {
  return copy[locale].empty;
}

export function IncomingGroupsPanel({
  locale,
  count,
  children,
  status,
}: {
  locale: Locale;
  count: number;
  children: ReactNode;
  status?: string;
}) {
  const headingId = useId();
  return (
    <section
      className="mb-[10px] min-w-0 overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)]"
      aria-labelledby={headingId}
    >
      <header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
        <div>
          <h2 className="m-0 text-[13px] font-semibold" id={headingId}>
            {copy[locale].title}
          </h2>
          <span className="mt-0.5 block text-xs text-[var(--muted)]">
            {incomingGroupsCount(locale, count)}
          </span>
        </div>
      </header>
      {status !== undefined && (
        <p
          role="status"
          className={status ? "px-[10px] py-[7px] text-sm" : "sr-only"}
        >
          {status}
        </p>
      )}
      {children}
    </section>
  );
}
