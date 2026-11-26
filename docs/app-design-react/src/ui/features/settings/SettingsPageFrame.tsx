import type { ReactNode, Ref } from "react";
import { PageHeader } from "../../components/PageHeader";

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
      className="mx-auto grid w-full min-w-0 max-w-[1200px] gap-5"
      aria-labelledby="settings-title"
    >
      <PageHeader
        id="settings-title"
        headingRef={headingRef}
        title={copy.title}
        subtitle={copy.subtitle}
      />
      {children}
    </section>
  );
}
