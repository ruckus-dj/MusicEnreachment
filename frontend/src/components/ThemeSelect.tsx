import { useEffect, useState } from "react";

export type ThemePreference = "system" | "light" | "dark";

const storageKey = "melotrove-theme";

// A failed write must not lose the choice: this cache lives as long as the
// page, so unmounting and remounting the control keeps the selected mode.
let sessionPreference: ThemePreference | undefined;

export function resetThemeSessionPreference() {
  sessionPreference = undefined;
}

function readPreference(): ThemePreference {
  if (sessionPreference) return sessionPreference;
  let preference: ThemePreference = "system";
  try {
    const stored = window.localStorage.getItem(storageKey);
    if (stored === "light" || stored === "dark") preference = stored;
  } catch {
    // Storage is optional; the preference remains usable for this page session.
  }
  // Cache the resolved preference so a remount does not lose a successfully
  // loaded choice if storage becomes unavailable later in the page lifetime.
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

export function ThemeSelect() {
  const [preference, setPreference] = useState<ThemePreference>(readPreference);
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);

  useEffect(() => {
    let mediaQuery: MediaQueryList | undefined;
    const updateSystemPreference = (event: MediaQueryListEvent) =>
      setPrefersDark(event.matches);

    try {
      mediaQuery = window.matchMedia?.("(prefers-color-scheme: dark)");
      mediaQuery?.addEventListener("change", updateSystemPreference);
    } catch {
      // The explicit themes and the default light fallback still work without it.
    }

    return () =>
      mediaQuery?.removeEventListener("change", updateSystemPreference);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme =
      preference === "system" ? (prefersDark ? "dark" : "light") : preference;
  }, [preference, prefersDark]);

  function changePreference(value: string) {
    if (value !== "system" && value !== "light" && value !== "dark") return;
    setPreference(value);
    sessionPreference = value;
    try {
      window.localStorage.setItem(storageKey, value);
    } catch {
      // Keep the current choice in React state when persistent storage is unavailable.
    }
  }

  return (
    <label>
      Тема интерфейса
      <select
        aria-label="Тема интерфейса"
        value={preference}
        onChange={(event) => changePreference(event.currentTarget.value)}
      >
        <option value="system">Как в системе</option>
        <option value="light">Светлая</option>
        <option value="dark">Тёмная</option>
      </select>
    </label>
  );
}
