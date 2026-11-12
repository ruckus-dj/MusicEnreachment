import { useEffect, useState } from "react";
import { Tooltip, TooltipTrigger } from "react-aria-components";
import { useI18n } from "../i18n/I18nProvider";
import { shellMessages } from "../i18n/shellMessages";
import { AppButton } from "./AppButton";

export type ThemePreference = "system" | "light" | "dark";

const storageKey = "melotrove-theme";
const themes: ThemePreference[] = ["system", "light", "dark"];

// Keep a page-session choice even when storage is unavailable or the control remounts.
let sessionPreference: ThemePreference | undefined;

export function resetThemeSessionPreference() {
  sessionPreference = undefined;
}

function nextTheme(theme: ThemePreference): ThemePreference {
  return themes[(themes.indexOf(theme) + 1) % themes.length];
}

function readPreference(): ThemePreference {
  if (sessionPreference) return sessionPreference;
  let preference: ThemePreference = "system";
  try {
    const stored = window.localStorage.getItem(storageKey);
    if (stored === "light" || stored === "dark") preference = stored;
  } catch {
    // Storage is optional; keep the default usable for this page session.
  }
  sessionPreference = preference;
  return preference;
}

function systemPrefersDark(): boolean {
  try {
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
  } catch {
    return false;
  }
}

function ThemeIcon({ theme }: { theme: ThemePreference }) {
  if (theme === "light") {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.65 17.65l1.42 1.42M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.65 6.35l1.42-1.42" />
      </svg>
    );
  }
  if (theme === "dark") {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />
      </svg>
    );
  }
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function ThemeControl() {
  const { locale } = useI18n();
  const copy = shellMessages[locale];
  const labels: Record<ThemePreference, string> = {
    system: copy.systemTheme,
    light: copy.lightTheme,
    dark: copy.darkTheme,
  };
  const [preference, setPreference] = useState<ThemePreference>(readPreference);
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
    <div className="flex items-center gap-2 text-[11px] text-[var(--muted)]">
      <TooltipTrigger>
        <AppButton
          className="h-[26px] w-7 !min-h-0 pointer-coarse:min-h-[40px]! pointer-coarse:min-w-[40px] rounded-[4px] !p-0 text-[var(--ink)] motion-safe:transition-[background-color,border-color] [&_svg]:size-[15px]"
          aria-label={`${copy.theme}: ${currentLabel}. ${copy.nextTheme}: ${followingLabel}`}
          onPress={() => {
            const next = nextTheme(preference);
            setPreference(next);
            sessionPreference = next;
            try {
              window.localStorage.setItem(storageKey, next);
              setWarning(false);
            } catch {
              setWarning(true);
            }
          }}
        >
          <ThemeIcon theme={preference} />
        </AppButton>
        <Tooltip className="react-aria-Tooltip ui-tooltip z-50 max-w-[260px] rounded-[5px] border border-[var(--line)] bg-[var(--ink)] px-2 py-1.5 text-[10px] text-[var(--surface)] shadow-[0_8px_24px_#0003]">
          {`${copy.theme}: ${currentLabel}`}
        </Tooltip>
      </TooltipTrigger>
      {warning && (
        <span
          className="max-w-[220px] text-[10px] text-[var(--warning)]"
          role="status"
        >
          {copy.themeWarning}
        </span>
      )}
    </div>
  );
}
