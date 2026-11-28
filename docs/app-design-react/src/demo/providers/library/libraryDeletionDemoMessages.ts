import { createNamespace } from "../../../ui/i18n/createNamespace";
import type { Locale } from "../../../ui/i18n/types";

/**
 * Prototype-host copy for the simulated selection-deletion notice. The
 * portable UI receives this text as a caller-supplied notice.
 */
export const libraryDeletionDemoMessages = createNamespace({
  en: {
    notice:
      "This is an asynchronous operation. The prototype only adds a demo request to the operations list; no real files or demo records are deleted.",
  },
  ru: {
    notice:
      "Асинхронная операция. Прототип только ставит демонстрационный запрос в список операций: реальные файлы и демонстрационные записи не удаляются.",
  },
});

export function libraryDeletionDemoNotice(locale: Locale): string {
  return libraryDeletionDemoMessages[locale].notice;
}
