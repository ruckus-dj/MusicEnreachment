import type { I18nCatalog, Locale, Translator, TranslationKey } from "./types";

export function createTranslator<const Catalog extends I18nCatalog>(
	locale: Locale,
	catalog: Catalog,
): Translator<Catalog> {
	return ((
		key: TranslationKey<Catalog>,
		values?: Record<string, string | number>,
	) => {
		const [namespace, message] = key.split(".", 2);
		const translation = catalog[namespace]?.[locale][message] ?? key;
		return translation.replace(/\{([^{}]+)\}/g, (placeholder, name: string) =>
			values && Object.hasOwn(values, name)
				? String(values[name])
				: placeholder,
		);
	}) as Translator<Catalog>;
}
