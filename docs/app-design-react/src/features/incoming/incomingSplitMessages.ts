import { createNamespace } from "../../i18n/createNamespace";
import type { Locale } from "../../i18n/types";

export const incomingSplitMessages = createNamespace({
	en: {
		split: "Split into a new group",
		splitFile: "Split “{file}” into a new group",
	},
	ru: {
		split: "Отделить в новую группу",
		splitFile: "Отделить «{file}» в новую группу",
	},
});

export type IncomingSplitMessage = keyof typeof incomingSplitMessages.en;

export function incomingSplitMessage(
	locale: Locale,
	key: IncomingSplitMessage,
	values: Record<string, string | number> = {},
): string {
	return incomingSplitMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
