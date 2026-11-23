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
import { catalog } from "./catalog";
import { defaultLocale, defaultPreference, languageStorageKey } from "./config";
import { resolveLocale } from "./locale";
import { createTranslator } from "./translator";
import type { LanguagePreference, Locale, Translator } from "./types";

type PreferenceState = {
  preference: LanguagePreference;
  persistenceIssue: "read" | "write" | null;
};

function isPreference(value: string | null): value is LanguagePreference {
  return value === "system" || value === "en" || value === "ru";
}

function initialPreference(): PreferenceState {
  try {
    const saved = window.localStorage.getItem(languageStorageKey);
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
  t: Translator<typeof catalog>;
  persistenceIssue: PreferenceState["persistenceIssue"];
};

const isolatedTranslator = createTranslator(defaultLocale, catalog);
const defaultContext: I18nValue = {
  locale: defaultLocale,
  preference: defaultPreference,
  setPreference: () => undefined,
  t: isolatedTranslator,
  persistenceIssue: null,
};

const I18nContext = createContext<I18nValue>(defaultContext);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState(initialPreference);
  const [systemLanguages, setSystemLanguages] = useState(readSystemLanguages);
  const locale = resolveLocale(state.preference, systemLanguages);
  const t = useMemo(() => createTranslator(locale, catalog), [locale]);

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
      window.localStorage.setItem(languageStorageKey, preference);
    } catch {
      persistenceIssue = "write";
    }
    setState({ preference, persistenceIssue });
  }, []);
  const value = useMemo<I18nValue>(
    () => ({
      locale,
      preference: state.preference,
      setPreference,
      t,
      persistenceIssue: state.persistenceIssue,
    }),
    [locale, state.preference, state.persistenceIssue, setPreference, t],
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
