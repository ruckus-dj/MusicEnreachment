import { useEffect, useState } from "react";
import { Tooltip, TooltipTrigger } from "react-aria-components";
import { useI18n } from "../i18n/I18nProvider";
import { AppButton } from "./AppButton";
import { AppIcon } from "./AppIcon";
import type { ThemePreference, ThemePreferenceStore } from "./themePreference";

export type { ThemePreference } from "./themePreference";

const themes: ThemePreference[] = ["system", "light", "dark"];

const messages = {
  en: {
    theme: "Theme",
    nextTheme: "Next theme",
    systemTheme: "System default",
    lightTheme: "Light",
    darkTheme: "Dark",
    themeWarning:
      "Could not save the theme preference. Your choice lasts until the page is closed.",
  },
  ru: {
    theme: "Тема",
    nextTheme: "Следующая тема",
    systemTheme: "Как в системе",
    lightTheme: "Светлая",
    darkTheme: "Тёмная",
    themeWarning:
      "Не удалось сохранить тему. Выбор действует до закрытия страницы.",
  },
} as const;

function nextTheme(theme: ThemePreference): ThemePreference {
  return themes[(themes.indexOf(theme) + 1) % themes.length];
}

function systemPrefersDark(): boolean {
  try {
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
  } catch {
    return false;
  }
}

function ThemeIcon({ theme }: { theme: ThemePreference }) {
  const name =
    theme === "light" ? "sun" : theme === "dark" ? "moon" : "monitor";
  return <AppIcon name={name} className="size-[18px]" />;
}

export function ThemeControl({ store }: { store: ThemePreferenceStore }) {
  const { locale } = useI18n();
  const copy = messages[locale];
  const labels: Record<ThemePreference, string> = {
    system: copy.systemTheme,
    light: copy.lightTheme,
    dark: copy.darkTheme,
  };
  const [preference, setPreference] = useState<ThemePreference>(() =>
    store.read(),
  );
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);
  const [warning, setWarning] = useState(false);

  useEffect(() => {
    let mediaQuery: MediaQueryList | undefined;
    const updateSystemPreference = (event: MediaQueryListEvent) =>
      setPrefersDark(event.matches);
    try {
      mediaQuery = window.matchMedia?.("(prefers-color-scheme: dark)");
      mediaQuery?.addEventListener("change", updateSystemPreference);
    } catch {
      // Explicit themes and the light fallback still work without matchMedia.
    }
    return () =>
      mediaQuery?.removeEventListener("change", updateSystemPreference);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme =
      preference === "system" ? (prefersDark ? "dark" : "light") : preference;
  }, [preference, prefersDark]);

  const currentLabel = labels[preference];
  const followingLabel = labels[nextTheme(preference)];

  return (
    <div className="flex items-center gap-2 text-xs text-[var(--muted)]">
      <TooltipTrigger>
        <AppButton
          variant="secondary"
          className="w-9 min-w-9 px-0 text-[var(--ink)]"
          aria-label={`${copy.theme}: ${currentLabel}. ${copy.nextTheme}: ${followingLabel}`}
          onPress={() => {
            const next = nextTheme(preference);
            setPreference(next);
            setWarning(!store.save(next));
          }}
        >
          <ThemeIcon theme={preference} />
        </AppButton>
        <Tooltip className="z-50 max-w-[260px] rounded-lg border border-[var(--line)] bg-[var(--ink)] px-2.5 py-1.5 text-xs text-[var(--surface)] shadow-[0_8px_24px_rgb(0_0_0/0.18)]">
          {`${copy.theme}: ${currentLabel}`}
        </Tooltip>
      </TooltipTrigger>
      {warning && (
        <span
          className="max-w-[220px] text-xs text-[var(--warning)]"
          role="status"
        >
          {copy.themeWarning}
        </span>
      )}
    </div>
  );
}
