export { resolveLocale } from "../../ui/i18n/locale";
export { createTranslator } from "../../ui/i18n/translator";
export type {
	I18nCatalog,
	LanguagePreference,
	Locale,
	NamespaceCatalog,
	PlaceholderNames,
	TranslationKey,
	TranslationParams,
	Translator,
} from "../../ui/i18n/types";
export { supportedLocales } from "../../ui/i18n/types";
export { catalog, common, createNamespace } from "./catalog";
export { I18nProvider, type I18nValue, useI18n } from "./host";
