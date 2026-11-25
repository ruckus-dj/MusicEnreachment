import { createI18n } from "../../ui/i18n/I18nProvider";
import { catalog } from "./catalog";
import { defaultLocale, defaultPreference, languageStorageKey } from "./config";

export const { I18nProvider, useI18n } = createI18n({
	catalog,
	defaultLocale,
	defaultPreference,
	languageStorageKey,
});

export type I18nValue = ReturnType<typeof useI18n>;
