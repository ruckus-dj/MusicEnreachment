import { createThemePreferenceStore } from "../../ui/components/themePreference";
import { catalog } from "../../ui/i18n/catalog";
import { createI18n } from "../../ui/i18n/I18nProvider";
import type { LanguagePreference, Locale } from "../../ui/i18n/types";

export const languageStorageKey = "music-design-v2-language";
export const defaultLocale: Locale = "ru";
export const defaultPreference: LanguagePreference = "ru";
export const themeStorageKey = "music-design-v2-theme";

export const { I18nProvider, useI18n } = createI18n({
  catalog,
  defaultLocale,
  defaultPreference,
  languageStorageKey,
});

export const themePreferenceStore = createThemePreferenceStore(themeStorageKey);
