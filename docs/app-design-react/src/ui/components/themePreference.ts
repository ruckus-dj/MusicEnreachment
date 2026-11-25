export type ThemePreference = "system" | "light" | "dark";

export type ThemePreferenceStore = {
  reset: () => void;
  read: () => ThemePreference;
  save: (preference: ThemePreference) => boolean;
};

// Keep a page-session choice even when storage is unavailable or the control remounts.
export function createThemePreferenceStore(
  storageKey: string,
): ThemePreferenceStore {
  let sessionPreference: ThemePreference | undefined;

  return {
    reset() {
      sessionPreference = undefined;
    },
    read() {
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
    },
    save(preference) {
      sessionPreference = preference;
      try {
        window.localStorage.setItem(storageKey, preference);
        return true;
      } catch {
        return false;
      }
    },
  };
}
