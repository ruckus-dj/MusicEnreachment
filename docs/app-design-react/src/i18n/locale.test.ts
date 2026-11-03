import { describe, expect, it } from "vitest";
import { resolveLocale } from "./locale";

describe("resolveLocale", () => {
	it("uses an explicit preference", () => {
		expect(resolveLocale("ru", ["en-US"])).toBe("ru");
		expect(resolveLocale("en", ["ru-RU"])).toBe("en");
	});

	it("selects the first supported language, reducing regional tags to base", () => {
		expect(resolveLocale("system", ["fr-CA", "ru-RU", "en-US"])).toBe("ru");
		expect(resolveLocale("system", ["en-GB", "ru-RU"])).toBe("en");
	});

	it("falls back to English when no supported system language is present", () => {
		expect(resolveLocale("system", ["fr-CA", "de-DE"])).toBe("en");
		expect(resolveLocale("system", [])).toBe("en");
	});
});
