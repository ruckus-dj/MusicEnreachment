import { themeStorageKey } from "./themePreferenceConfig";

export type ThemePreference = "system" | "light" | "dark";

// Keep a page-session choice even when storage is unavailable or the control remounts.
let sessionPreference: ThemePreference | undefined;

export function resetThemeSessionPreference() {
  sessionPreference = undefined;
}

export function readThemePreference(): ThemePreference {
  if (sessionPreference) return sessionPreference;
  let preference: ThemePreference = "system";
  try {
    const stored = window.localStorage.getItem(themeStorageKey);
    if (stored === "light" || stored === "dark") preference = stored;
  } catch {
    // Storage is optional; keep the default usable for this page session.
  }
  sessionPreference = preference;
  return preference;
}

export function saveThemePreference(preference: ThemePreference): boolean {
  sessionPreference = preference;
  try {
    window.localStorage.setItem(themeStorageKey, preference);
    return true;
  } catch {
    return false;
  }
}
