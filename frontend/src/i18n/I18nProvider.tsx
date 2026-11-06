import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { I18nProvider as ReactAriaI18nProvider } from "react-aria-components";
import { type LanguagePreference, type Locale, resolveLocale } from "./locale";

const storageKey = "melotrove-language";

type PreferenceState = {
  preference: LanguagePreference;
  persistenceIssue: "read" | "write" | null;
};

const persistenceMessages: Record<Locale, Record<"read" | "write", string>> = {
  en: {
    read: "Language preference is active but could not be read from browser storage.",
    write:
      "Language preference is active but could not be saved in browser storage.",
  },
  ru: {
    read: "Настройка языка действует, но не удалось прочитать её из хранилища браузера.",
    write:
      "Настройка языка действует, но не удалось сохранить её в хранилище браузера.",
  },
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
    return { preference: "system", persistenceIssue: "read" };
  }
  return { preference: "system", persistenceIssue: null };
}

function readSystemLanguages(): readonly string[] {
  try {
    if (navigator.languages?.length) return [...navigator.languages];
    return navigator.language ? [navigator.language] : [];
  } catch {
    return [];
  }
}

export type I18nValue = {
  locale: Locale;
  preference: LanguagePreference;
  setPreference: (preference: LanguagePreference) => void;
  persistenceError: string | null;
};

const defaultContext: I18nValue = {
  locale: "en",
  preference: "system",
  setPreference: () => undefined,
  persistenceError: null,
};

const I18nContext = createContext<I18nValue>(defaultContext);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState(initialPreference);
  const [systemLanguages, setSystemLanguages] = useState(readSystemLanguages);
  const locale = resolveLocale(state.preference, systemLanguages);

  useEffect(() => {
    if (state.preference !== "system") return;
    const updateLanguages = () => setSystemLanguages(readSystemLanguages());
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
    ? persistenceMessages[locale][state.persistenceIssue]
    : null;

  const value = useMemo<I18nValue>(
    () => ({
      locale,
      preference: state.preference,
      setPreference,
      persistenceError,
    }),
    [locale, state.preference, persistenceError, setPreference],
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
