import type { Ref } from "react";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import { PageHeader } from "../../components/PageHeader";
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
    <PageHeader
      id="sources-title"
      headingRef={headingRef}
      title={text.title}
      subtitle={text.description}
      actions={
        registration ? (
          <AppButton
            id={registration.id}
            variant="primary"
            onPress={registration.onPress}
          >
            <AppIcon name="plus" />
            {sourceRegistrationMessages[locale].addSource}
          </AppButton>
        ) : undefined
      }
    />
  );
}
