import type { ReactNode } from "react";
import { Disclosure, DisclosurePanel, Heading } from "react-aria-components";
import { AppButton } from "../../components/AppButton";

const copy = {
  en: {
    technical: "Technical details",
    current: "Current publication",
  },
  ru: {
    technical: "Технические сведения",
    current: "Текущая публикация",
  },
} as const;

export function LibraryPositionTechnicalDetailsView({
  locale,
  children,
  current,
  afterCurrent,
}: {
  locale: "en" | "ru";
  children: ReactNode;
  current?: ReactNode;
  afterCurrent?: ReactNode;
}) {
  const text = copy[locale];
  return (
    <details className="min-w-0">
      <summary className="cursor-pointer text-[var(--accent)] underline-offset-2 hover:underline">
        {text.technical}
      </summary>
      {children}
      {current && (
        <Disclosure className="react-aria-Disclosure ui-disclosure border-b border-[var(--line)] py-[7px] [&_[slot=trigger]]:border-0 [&_[slot=trigger]]:bg-transparent [&_[slot=trigger]]:px-0 [&_[slot=trigger]]:py-1 [&_[slot=trigger]]:text-[var(--accent)] [&_p]:my-1.5 [&_p]:text-[var(--muted)]">
          <Heading level={3}>
            <AppButton slot="trigger">{text.current}</AppButton>
          </Heading>
          <DisclosurePanel className="react-aria-DisclosurePanel ui-disclosure-panel min-w-0">
            {current}
          </DisclosurePanel>
        </Disclosure>
      )}
      {afterCurrent}
    </details>
  );
}
