export const supportedLocales = ["en", "ru"] as const;

export type Locale = (typeof supportedLocales)[number];
export type LanguagePreference = "system" | Locale;

function localeFromLanguageTag(language: string): Locale | undefined {
  const normalized = language.trim().replaceAll("_", "-").toLowerCase();
  if (normalized === "en" || normalized.startsWith("en-")) return "en";
  if (normalized === "ru" || normalized.startsWith("ru-")) return "ru";
  return undefined;
}

export function resolveLocale(
  preference: LanguagePreference,
  languages: readonly string[] = [],
): Locale {
  if (preference !== "system") return preference;
  for (const language of languages) {
    const locale = localeFromLanguageTag(language);
    if (locale) return locale;
  }
  return "en";
}
