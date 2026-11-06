import { describe, expect, it } from "vitest";
import { resolveLocale } from "./locale";

describe("resolveLocale", () => {
  it("uses an explicit preference", () => {
    expect(resolveLocale("ru", ["en-US"])).toBe("ru");
    expect(resolveLocale("en", ["ru-RU"])).toBe("en");
  });

  it("prefers the first supported system language", () => {
    expect(resolveLocale("system", ["fr-FR", "ru-RU", "en-US"])).toBe("ru");
    expect(resolveLocale("system", ["en-GB", "ru-RU"])).toBe("en");
    expect(resolveLocale("system", ["de_DE", "ru_RU"])).toBe("ru");
  });

  it("falls back to English", () => {
    expect(resolveLocale("system")).toBe("en");
    expect(resolveLocale("system", [])).toBe("en");
    expect(resolveLocale("system", ["de-DE"])).toBe("en");
  });
});
