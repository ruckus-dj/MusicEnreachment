import { type ReactNode, useId } from "react";
import { AppButton } from "../../components/AppButton";

const copy = {
  en: {
    pageTitle: "Review incoming groups",
    introduction:
      "Open a group of source files to choose a release and assign files to its positions.",
    title: "Incoming groups",
    empty: "There are no incoming groups yet.",
  },
  ru: {
    pageTitle: "Разбор входящих групп",
    introduction:
      "Откройте группу исходных файлов, чтобы выбрать издание и назначить файлы его позициям.",
    title: "Входящие группы",
    empty: "Входящих групп пока нет.",
  },
} as const;

type Locale = keyof typeof copy;

export function IncomingPageHeading({
  locale,
  action,
}: {
  locale: Locale;
  action: { label: string; onPress: () => void };
}) {
  return (
    <header className="mb-[10px] flex min-w-0 flex-wrap items-center justify-between gap-x-[14px] gap-y-2">
      <div className="min-w-0">
        <h1
          id="incoming-title"
          className="m-0 text-xl leading-[1.25] text-[var(--ink)] [text-wrap:balance]"
        >
          {copy[locale].pageTitle}
        </h1>
        <p className="mt-[3px] mb-0 text-xs text-[var(--muted)]">
          {copy[locale].introduction}
        </p>
      </div>
      <AppButton className="max-[600px]:w-full" onPress={action.onPress}>
        {action.label}
      </AppButton>
    </header>
  );
}

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
