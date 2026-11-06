import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { useEffect, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryStorage } from "../test/memoryStorage";
import { I18nProvider, useI18n } from "./I18nProvider";

let draftMounts = 0;

function DraftChild() {
  const [draft] = useState("stable draft");
  useEffect(() => {
    draftMounts += 1;
  }, []);
  return <span data-testid="draft">{draft}</span>;
}

function Harness({ draft = false }: { draft?: boolean }) {
  const { locale, preference, setPreference, persistenceError } = useI18n();
  return (
    <>
      <span data-testid="locale">{locale}</span>
      <span data-testid="preference">{preference}</span>
      {persistenceError && <span role="status">{persistenceError}</span>}
      <button type="button" onClick={() => setPreference("ru")}>
        use-ru
      </button>
      <button type="button" onClick={() => setPreference("en")}>
        use-en
      </button>
      {draft && <DraftChild />}
    </>
  );
}

function setSystemLanguages(languages: string[]) {
  Object.defineProperty(window.navigator, "languages", {
    configurable: true,
    value: languages,
  });
  Object.defineProperty(window.navigator, "language", {
    configurable: true,
    value: languages[0] ?? "en-US",
  });
}

describe("I18nProvider", () => {
  beforeEach(() => {
    draftMounts = 0;
    document.documentElement.removeAttribute("lang");
    setSystemLanguages(["en-US"]);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    document.documentElement.removeAttribute("lang");
  });

  it("resolves system language, persists a choice and updates document lang", () => {
    const storage = createMemoryStorage();
    vi.stubGlobal("localStorage", storage);
    setSystemLanguages(["fr-FR", "ru-RU", "en-US"]);

    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );

    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
    expect(screen.getByTestId("preference")).toHaveTextContent("system");
    expect(document.documentElement).toHaveAttribute("lang", "ru");

    fireEvent.click(screen.getByRole("button", { name: "use-en" }));

    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(screen.getByTestId("preference")).toHaveTextContent("en");
    expect(storage.getItem("melotrove-language")).toBe("en");
    expect(document.documentElement).toHaveAttribute("lang", "en");
  });

  it("restores a stored preference and follows languagechange in system mode", () => {
    const storage = createMemoryStorage();
    storage.setItem("melotrove-language", "ru");
    vi.stubGlobal("localStorage", storage);
    setSystemLanguages(["en-US"]);

    const first = render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );
    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
    expect(screen.getByTestId("preference")).toHaveTextContent("ru");
    first.unmount();

    storage.removeItem("melotrove-language");
    setSystemLanguages(["en-US"]);
    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );
    expect(screen.getByTestId("locale")).toHaveTextContent("en");

    act(() => {
      setSystemLanguages(["ru-RU"]);
      window.dispatchEvent(new Event("languagechange"));
    });
    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
  });

  it("falls back to English for an unsupported system language", () => {
    vi.stubGlobal("localStorage", createMemoryStorage());
    setSystemLanguages(["de-DE"]);

    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );

    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(screen.getByTestId("preference")).toHaveTextContent("system");
  });

  it("remains usable when storage reads are denied", () => {
    const storage = createMemoryStorage();
    vi.spyOn(storage, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.stubGlobal("localStorage", storage);

    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );

    expect(screen.getByTestId("preference")).toHaveTextContent("system");
    expect(screen.getByRole("status")).toHaveTextContent(
      /could not be read from browser storage/,
    );

    fireEvent.click(screen.getByRole("button", { name: "use-ru" }));

    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
    expect(screen.getByTestId("preference")).toHaveTextContent("ru");
  });

  it("keeps the chosen language when storage writes are denied", () => {
    const storage = createMemoryStorage();
    vi.spyOn(storage, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.stubGlobal("localStorage", storage);

    render(
      <I18nProvider>
        <Harness />
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "use-ru" }));

    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
    expect(screen.getByTestId("preference")).toHaveTextContent("ru");
    expect(screen.getByRole("status")).toHaveTextContent(
      /не удалось сохранить её в хранилище браузера/,
    );
  });

  it("does not remount descendants when the locale changes", () => {
    vi.stubGlobal("localStorage", createMemoryStorage());
    setSystemLanguages(["en-US"]);

    render(
      <I18nProvider>
        <Harness draft />
      </I18nProvider>,
    );

    const draft = screen.getByTestId("draft");
    expect(draftMounts).toBe(1);
    expect(screen.getByTestId("locale")).toHaveTextContent("en");

    fireEvent.click(screen.getByRole("button", { name: "use-ru" }));

    expect(screen.getByTestId("locale")).toHaveTextContent("ru");
    expect(screen.getByTestId("draft")).toBe(draft);
    expect(screen.getByTestId("draft")).toHaveTextContent("stable draft");
    expect(draftMounts).toBe(1);
  });
});
