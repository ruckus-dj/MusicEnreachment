import type { LanguagePreference, Locale } from "./types";

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
