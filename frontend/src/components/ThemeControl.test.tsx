import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider, useI18n } from "../i18n/I18nProvider";
import { resetThemeSessionPreference, ThemeControl } from "./ThemeControl";

function LocaleSwitch() {
  const { setPreference } = useI18n();
  return (
    <button type="button" onClick={() => setPreference("en")}>
      Switch to English
    </button>
  );
}

function renderControl() {
  return render(
    <I18nProvider>
      <ThemeControl />
      <LocaleSwitch />
    </I18nProvider>,
  );
}

function mediaFixture(initial = false) {
  let matches = initial;
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  const media = {
    get matches() {
      return matches;
    },
    media: "(prefers-color-scheme: dark)",
    onchange: null,
    addEventListener: (
      _: string,
      listener: (event: MediaQueryListEvent) => void,
    ) => listeners.add(listener),
    removeEventListener: (
      _: string,
      listener: (event: MediaQueryListEvent) => void,
    ) => listeners.delete(listener),
    dispatchEvent: () => true,
    change(value: boolean) {
      matches = value;
      listeners.forEach((listener) => {
        listener({ matches: value } as MediaQueryListEvent);
      });
    },
  };
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => media),
  );
  return media;
}

describe("ThemeControl", () => {
  let storage: Storage;

  beforeEach(() => {
    resetThemeSessionPreference();
    document.documentElement.removeAttribute("data-theme");
    const values = new Map<string, string>();
    storage = {
      get length() {
        return values.size;
      },
      clear: () => values.clear(),
      getItem: (key) => values.get(key) ?? null,
      key: (index) => [...values.keys()][index] ?? null,
      removeItem: (key) => values.delete(key),
      setItem: (key, value) => values.set(key, String(value)),
    };
    storage.setItem("melotrove-language", "ru");
    vi.stubGlobal("localStorage", storage);
  });

  afterEach(() => vi.unstubAllGlobals());

  it("cycles system, light, dark and persists using the production key", () => {
    mediaFixture();
    renderControl();
    const button = screen.getByRole("button", {
      name: "Тема: Как в системе. Следующая тема: Светлая",
    });
    fireEvent.click(button);
    expect(
      screen.getByRole("button", {
        name: "Тема: Светлая. Следующая тема: Тёмная",
      }),
    ).toBeInTheDocument();
    expect(storage.getItem("melotrove-theme")).toBe("light");
    fireEvent.click(
      screen.getByRole("button", {
        name: "Тема: Светлая. Следующая тема: Тёмная",
      }),
    );
    expect(storage.getItem("melotrove-theme")).toBe("dark");
    fireEvent.click(
      screen.getByRole("button", {
        name: "Тема: Тёмная. Следующая тема: Как в системе",
      }),
    );
    expect(storage.getItem("melotrove-theme")).toBe("system");
  });

  it("preserves a selection across remounts if storage writes fail", () => {
    mediaFixture();
    vi.spyOn(storage, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const first = renderControl();
    fireEvent.click(
      screen.getByRole("button", {
        name: "Тема: Как в системе. Следующая тема: Светлая",
      }),
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Не удалось сохранить тему",
    );
    first.unmount();
    renderControl();
    expect(
      screen.getByRole("button", {
        name: "Тема: Светлая. Следующая тема: Тёмная",
      }),
    ).toBeInTheDocument();
  });

  it("tracks system changes while system mode is selected", () => {
    const media = mediaFixture();
    renderControl();
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    act(() => media.change(true));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    fireEvent.click(
      screen.getByRole("button", {
        name: "Тема: Как в системе. Следующая тема: Светлая",
      }),
    );
    act(() => media.change(false));
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
  });

  it("supports keyboard activation and announces current and next modes", () => {
    mediaFixture();
    renderControl();
    const button = screen.getByRole("button", {
      name: "Тема: Как в системе. Следующая тема: Светлая",
    });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.keyDown(button, { key: "Enter" });
    fireEvent.keyUp(button, { key: "Enter" });
    expect(
      screen.getByRole("button", {
        name: "Тема: Светлая. Следующая тема: Тёмная",
      }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Switch to English" }));
    expect(
      screen.getByRole("button", {
        name: "Theme: Light. Next theme: Dark",
      }),
    ).toBeInTheDocument();
  });
});
