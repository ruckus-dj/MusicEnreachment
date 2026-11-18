import { createNamespace } from "../../i18n/createNamespace";
import type { Locale } from "../../i18n/types";

export const libraryPaginationMessages = createNamespace({
	en: {
		pages: "Library pages",
		pageStatus: "Page {page} of {pages}",
		totalItems: "Total: {count}",
		previousPage: "Previous page",
		nextPage: "Next page",
	},
	ru: {
		pages: "Страницы медиатеки",
		pageStatus: "Страница {page} из {pages}",
		totalItems: "Всего: {count}",
		previousPage: "Предыдущая страница",
		nextPage: "Следующая страница",
	},
});

export type LibraryPaginationMessage =
	keyof typeof libraryPaginationMessages.en;

export function libraryPaginationMessage(
	locale: Locale,
	key: LibraryPaginationMessage,
	values: Record<string, string | number> = {},
): string {
	return libraryPaginationMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
