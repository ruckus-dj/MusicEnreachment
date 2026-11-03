import { I18nProvider as ReactAriaI18nProvider } from "react-aria-components";
import {
	createContext,
	type ReactNode,
	useCallback,
	useContext,
	useEffect,
	useMemo,
	useState,
} from "react";
import { catalog } from "./catalog";
import { resolveLocale } from "./locale";
import { createTranslator } from "./translator";
import type { LanguagePreference, Locale, Translator } from "./types";

const storageKey = "music-design-v2-language";

type PreferenceState = {
	preference: LanguagePreference;
	persistenceIssue: "read" | "write" | null;
};

function isPreference(value: string | null): value is LanguagePreference {
	return value === "system" || value === "en" || value === "ru";
}

function initialPreference(): PreferenceState {
	try {
		const saved = window.localStorage.getItem(storageKey);
		if (isPreference(saved))
			return { preference: saved, persistenceIssue: null };
	} catch {
		return {
			preference: "system",
			persistenceIssue: "read",
		};
	}
	return { preference: "system", persistenceIssue: null };
}

export type I18nValue = {
	locale: Locale;
	preference: LanguagePreference;
	setPreference: (preference: LanguagePreference) => void;
	t: Translator<typeof catalog>;
	persistenceError: string | null;
};

const isolatedTranslator = createTranslator("ru", catalog);
const defaultContext: I18nValue = {
	locale: "ru",
	preference: "ru",
	setPreference: () => undefined,
	t: isolatedTranslator,
	persistenceError: null,
};

const I18nContext = createContext<I18nValue>(defaultContext);

export function I18nProvider({ children }: { children: ReactNode }) {
	const [state, setState] = useState(initialPreference);
	const [systemLanguages, setSystemLanguages] = useState<readonly string[]>(
		() =>
			navigator.languages ? [...navigator.languages] : [navigator.language],
	);
	const locale = resolveLocale(state.preference, systemLanguages);
	const t = useMemo(() => createTranslator(locale, catalog), [locale]);

	useEffect(() => {
		if (state.preference !== "system") return;
		const updateLanguages = () =>
			setSystemLanguages(
				navigator.languages ? [...navigator.languages] : [navigator.language],
			);
		updateLanguages();
		window.addEventListener("languagechange", updateLanguages);
		return () => window.removeEventListener("languagechange", updateLanguages);
	}, [state.preference]);

	useEffect(() => {
		document.documentElement.lang = locale;
	}, [locale]);

	const setPreference = useCallback((preference: LanguagePreference) => {
		let persistenceIssue: PreferenceState["persistenceIssue"] = null;
		try {
			window.localStorage.setItem(storageKey, preference);
		} catch {
			persistenceIssue = "write";
		}
		setState({ preference, persistenceIssue });
	}, []);
	const persistenceError = state.persistenceIssue
		? t(
				state.persistenceIssue === "read"
					? "common.preferenceReadWarning"
					: "common.preferenceWriteWarning",
			)
		: null;

	const value = useMemo<I18nValue>(
		() => ({
			locale,
			preference: state.preference,
			setPreference,
			t,
			persistenceError,
		}),
		[locale, state.preference, persistenceError, setPreference, t],
	);

	return (
		<I18nContext.Provider value={value}>
			<ReactAriaI18nProvider locale={locale}>{children}</ReactAriaI18nProvider>
		</I18nContext.Provider>
	);
}

export function useI18n(): I18nValue {
	return useContext(I18nContext);
}
