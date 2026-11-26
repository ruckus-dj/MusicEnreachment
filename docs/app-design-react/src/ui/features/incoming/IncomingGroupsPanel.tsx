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
    <header className="mb-5 flex min-w-0 flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <div className="min-w-0">
        <h1
          id="incoming-title"
          className="m-0 text-2xl font-semibold leading-8 tracking-[-0.01em] text-[var(--ink)] [text-wrap:balance]"
        >
          {copy[locale].pageTitle}
        </h1>
        <p className="mt-1.5 mb-0 max-w-[68ch] text-sm leading-5 text-[var(--muted)]">
          {copy[locale].introduction}
        </p>
      </div>
      <AppButton
        className="min-h-9 px-4 text-sm max-[600px]:w-full"
        onPress={action.onPress}
      >
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
      className="mb-4 min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[0_1px_2px_#0000000a]"
      aria-labelledby={headingId}
    >
      <header className="flex min-h-12 flex-wrap items-center justify-between gap-x-3 gap-y-1 border-b border-[var(--line)] px-4 py-2.5">
        <h2
          className="m-0 text-sm font-semibold text-[var(--ink)]"
          id={headingId}
        >
          {copy[locale].title}
        </h2>
        <span className="text-sm text-[var(--muted)]">
          {incomingGroupsCount(locale, count)}
        </span>
      </header>
      {status !== undefined && (
        <p
          role="status"
          className={
            status
              ? "m-0 border-b border-[var(--line)] bg-[var(--raised)] px-4 py-2 text-sm text-[var(--muted)]"
              : "sr-only"
          }
        >
          {status}
        </p>
      )}
      {children}
    </section>
  );
}
