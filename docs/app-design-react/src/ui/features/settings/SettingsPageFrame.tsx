import type { ReactNode, Ref } from "react";

const messages = {
  en: {
    title: "Settings",
    subtitle: "Library configuration and connected services",
  },
  ru: {
    title: "Настройки",
    subtitle: "Параметры библиотеки и её подключений",
  },
};

export function SettingsPageFrame({
  locale,
  headingRef,
  children,
}: {
  locale: keyof typeof messages;
  headingRef?: Ref<HTMLHeadingElement>;
  children: ReactNode;
}) {
  const copy = messages[locale];
  return (
    <section
      className="grid min-w-0 gap-[14px]"
      aria-labelledby="settings-title"
    >
      <header className="mb-[18px] flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] pb-[14px]">
        <div>
          <h1
            ref={headingRef}
            tabIndex={-1}
            className="m-0 text-[clamp(21px,2.5vw,28px)] leading-[1.18] [text-wrap:balance]"
            id="settings-title"
          >
            {copy.title}
          </h1>
          <p className="m-0 max-w-[75ch] text-[var(--muted)]">
            {copy.subtitle}
          </p>
        </div>
      </header>
      {children}
    </section>
  );
}
