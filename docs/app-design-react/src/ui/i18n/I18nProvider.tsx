import {
  createContext,
  type ReactElement,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { I18nProvider as ReactAriaI18nProvider } from "react-aria-components";
import { resolveLocale } from "./locale";
import { createTranslator } from "./translator";
import {
  type I18nCatalog,
  type LanguagePreference,
  type Locale,
  supportedLocales,
  type Translator,
} from "./types";

type PreferenceState = {
  preference: LanguagePreference;
  persistenceIssue: "read" | "write" | null;
};

export type I18nHost<Catalog extends I18nCatalog> = {
  catalog: Catalog;
  defaultLocale: Locale;
  defaultPreference: LanguagePreference;
  languageStorageKey: string;
};

export type I18nValue<Catalog extends I18nCatalog> = {
  locale: Locale;
  preference: LanguagePreference;
  setPreference: (preference: LanguagePreference) => void;
  t: Translator<Catalog>;
  persistenceIssue: PreferenceState["persistenceIssue"];
};

type I18nUiValue = Omit<I18nValue<I18nCatalog>, "t">;

export type I18nRuntime<Catalog extends I18nCatalog> = {
  I18nProvider: (props: { children: ReactNode }) => ReactElement;
  useI18n: () => I18nValue<Catalog>;
};

function isPreference(value: string | null): value is LanguagePreference {
  return (
    value === "system" || supportedLocales.some((locale) => locale === value)
  );
}

function initialPreference(languageStorageKey: string): PreferenceState {
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

const I18nUiContext = createContext<I18nUiValue | null>(null);
let uiFallback: I18nUiValue = {
  locale: "en",
  preference: "system",
  setPreference: () => undefined,
  persistenceIssue: null,
};

export function useI18n(): I18nUiValue {
  return useContext(I18nUiContext) ?? uiFallback;
}

export function createI18n<Catalog extends I18nCatalog>(
  host: I18nHost<Catalog>,
): I18nRuntime<Catalog> {
  uiFallback = {
    locale: host.defaultLocale,
    preference: host.defaultPreference,
    setPreference: () => undefined,
    persistenceIssue: null,
  };
  const defaultContext: I18nValue<Catalog> = {
    ...uiFallback,
    t: createTranslator(host.defaultLocale, host.catalog),
  };
  const Context = createContext<I18nValue<Catalog>>(defaultContext);

  function useHostI18n(): I18nValue<Catalog> {
    return useContext(Context);
  }

  function I18nProvider({ children }: { children: ReactNode }) {
    const [state, setState] = useState(() =>
      initialPreference(host.languageStorageKey),
    );
    const [systemLanguages, setSystemLanguages] = useState(readSystemLanguages);
    const locale = resolveLocale(state.preference, systemLanguages);
    const t = useMemo(() => createTranslator(locale, host.catalog), [locale]);

    useEffect(() => {
      if (state.preference !== "system") return;
      const updateLanguages = () => setSystemLanguages(readSystemLanguages());
      updateLanguages();
      window.addEventListener("languagechange", updateLanguages);
      return () =>
        window.removeEventListener("languagechange", updateLanguages);
    }, [state.preference]);

    useEffect(() => {
      document.documentElement.lang = locale;
    }, [locale]);

    const setPreference = useCallback((preference: LanguagePreference) => {
      let persistenceIssue: PreferenceState["persistenceIssue"] = null;
      try {
        window.localStorage.setItem(host.languageStorageKey, preference);
      } catch {
        persistenceIssue = "write";
      }
      setState({ preference, persistenceIssue });
    }, []);
    const value = useMemo<I18nValue<Catalog>>(
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
      <Context.Provider value={value}>
        <I18nUiContext.Provider value={value}>
          <ReactAriaI18nProvider locale={locale}>
            {children}
          </ReactAriaI18nProvider>
        </I18nUiContext.Provider>
      </Context.Provider>
    );
  }

  return { I18nProvider, useI18n: useHostI18n };
}
