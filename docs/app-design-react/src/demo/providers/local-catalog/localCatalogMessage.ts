import type { Locale } from "../../../ui/i18n/types";
import { localCatalogMessages } from "./localCatalogMessages";

export type LocalCatalogMessage = keyof typeof localCatalogMessages.en;

export function localCatalogMessage(
	locale: Locale,
	key: LocalCatalogMessage,
	values: Record<string, string | number> = {},
): string {
	return localCatalogMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
