import { createNamespace } from "../../../ui/i18n/createNamespace";
import type { Locale } from "../../../ui/i18n/types";

/**
 * Prototype-host copy for simulated publication notices. The portable UI must
 * not own demo-only labels, so this demo notice composes its own messages.
 */
export const publicationNoticeMessages = createNamespace({
  en: {
    published: "Demo: {count} positions published.",
    linksSaved: "Links for {count} positions saved.",
    unchanged: "Unselected positions and unmatched files were not changed.",
  },
  ru: {
    published: "Демо: опубликовано позиций: {count}.",
    linksSaved: "Сохранены связи позиций: {count}.",
    unchanged: "Невыбранные позиции и неразмеченные файлы не изменены.",
  },
});

export type PublicationNoticeMessage =
  keyof typeof publicationNoticeMessages.en;

export function publicationNoticeMessage(
  locale: Locale,
  key: PublicationNoticeMessage,
  values: Record<string, string | number> = {},
): string {
  return publicationNoticeMessages[locale][key].replace(
    /\{([^}]+)\}/g,
    (_, name: string) => String(values[name] ?? `{${name}}`),
  );
}
