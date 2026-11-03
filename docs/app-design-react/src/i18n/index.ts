export { createNamespace, catalog, common } from "./catalog";
export { I18nProvider, useI18n, type I18nValue } from "./I18nProvider";
export { resolveLocale } from "./locale";
export { createTranslator } from "./translator";
export type {
	I18nCatalog,
	LanguagePreference,
	Locale,
	NamespaceCatalog,
	PlaceholderNames,
	TranslationParams,
	TranslationKey,
	Translator,
} from "./types";
export { supportedLocales } from "./types";
