import type { Ref } from "react";
import { AppButton } from "../../components/AppButton";
import { sourceRegistrationMessages } from "./SourceRegistrationView";

const copy = {
  en: {
    title: "Sources",
    description:
      "Choose server directories and their processing mode. Analysis does not modify originals; source deletion is a separate setting and requires filesystem permission. Files are not uploaded by drag and drop.",
  },
  ru: {
    title: "Источники",
    description:
      "Выберите серверные каталоги и режим их обработки. Анализ не изменяет исходники; удаление — отдельная настройка, требующая разрешения файловой системы. Файлы не загружаются перетаскиванием.",
  },
} as const;

export function SourceListHeadingView({
  locale,
  headingRef,
  registration,
}: {
  locale: keyof typeof copy;
  headingRef?: Ref<HTMLHeadingElement>;
  registration?: { id: string; onPress: () => void };
}) {
  const text = copy[locale];
  return (
    <header className="mb-[18px] flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] pb-[14px]">
      <div className="min-w-0">
        <h1
          ref={headingRef}
          tabIndex={-1}
          id="sources-title"
          className="m-0 text-[clamp(21px,2.5vw,28px)] leading-[1.18] [text-wrap:balance]"
        >
          {text.title}
        </h1>
        <p className="m-0 max-w-[75ch] text-[var(--muted)]">
          {text.description}
        </p>
      </div>
      {registration && (
        <div className="my-2 flex flex-wrap items-center gap-2">
          <AppButton id={registration.id} onPress={registration.onPress}>
            {sourceRegistrationMessages[locale].addSource}
          </AppButton>
        </div>
      )}
    </header>
  );
}
