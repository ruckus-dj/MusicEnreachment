import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { JSDOM } from "jsdom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetThemeSessionPreference, ThemeSelect } from "./ThemeSelect";

describe("ThemeSelect", () => {
  let mediaChange: ((event: MediaQueryListEvent) => void) | undefined;
  let addEventListener: ReturnType<typeof vi.fn>;
  let removeEventListener: ReturnType<typeof vi.fn>;
  let matches = false;

  beforeEach(() => {
    resetThemeSessionPreference();
    vi.stubGlobal(
      "localStorage",
      new JSDOM("", { url: "http://localhost" }).window.localStorage,
    );
    window.localStorage.clear();
    matches = false;
    mediaChange = undefined;
    addEventListener = vi.fn((event: string, listener: EventListener) => {
      if (event === "change")
        mediaChange = listener as (event: MediaQueryListEvent) => void;
    });
    removeEventListener = vi.fn();
    vi.stubGlobal(
      "matchMedia",
      vi.fn(
        () =>
          ({
            get matches() {
              return matches;
            },
            media: "(prefers-color-scheme: dark)",
            addEventListener,
            removeEventListener,
          }) as unknown as MediaQueryList,
      ),
    );
    delete document.documentElement.dataset.theme;
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    delete document.documentElement.dataset.theme;
  });

  it("defaults to system, applies system changes, and cleans up the listener", () => {
    const { unmount } = render(<ThemeSelect />);

    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toHaveValue("system");
    expect(document.documentElement.dataset.theme).toBe("light");

    matches = true;
    act(() => mediaChange?.({ matches: true } as MediaQueryListEvent));
    expect(document.documentElement.dataset.theme).toBe("dark");

    unmount();
    expect(removeEventListener).toHaveBeenCalledWith(
      "change",
      expect.any(Function),
    );
  });

  it("loads and persists an explicit preference", () => {
    window.localStorage.setItem("melotrove-theme", "dark");
    render(<ThemeSelect />);

    const select = screen.getByRole("combobox", { name: "Тема интерфейса" });
    expect(select).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");

    fireEvent.change(select, { target: { value: "light" } });
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(window.localStorage.getItem("melotrove-theme")).toBe("light");
  });

  it("continues to work in memory when localStorage is unavailable", () => {
    vi.spyOn(
      Object.getPrototypeOf(window.localStorage),
      "getItem",
    ).mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    vi.spyOn(
      Object.getPrototypeOf(window.localStorage),
      "setItem",
    ).mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    const { unmount } = render(<ThemeSelect />);

    const select = screen.getByRole("combobox", { name: "Тема интерфейса" });
    expect(select).toHaveValue("system");
    fireEvent.change(select, { target: { value: "dark" } });
    expect(select).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");

    unmount();
    render(<ThemeSelect />);
    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("keeps a loaded stored preference across remount when reading fails", () => {
    window.localStorage.setItem("melotrove-theme", "dark");
    const { unmount } = render(<ThemeSelect />);

    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");

    vi.spyOn(
      Object.getPrototypeOf(window.localStorage),
      "getItem",
    ).mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    unmount();

    render(<ThemeSelect />);
    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("keeps a chosen preference across remount when the write fails", () => {
    window.localStorage.setItem("melotrove-theme", "light");
    vi.spyOn(
      Object.getPrototypeOf(window.localStorage),
      "setItem",
    ).mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    const { unmount } = render(<ThemeSelect />);

    const select = screen.getByRole("combobox", { name: "Тема интерфейса" });
    expect(select).toHaveValue("light");
    fireEvent.change(select, { target: { value: "dark" } });
    expect(document.documentElement.dataset.theme).toBe("dark");
    unmount();

    render(<ThemeSelect />);
    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toHaveValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(window.localStorage.getItem("melotrove-theme")).toBe("light");
  });
});
