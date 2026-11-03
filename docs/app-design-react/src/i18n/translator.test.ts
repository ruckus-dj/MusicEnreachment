import { describe, expect, it } from "vitest";
import { common, createNamespace } from "./catalog";
import { createTranslator } from "./translator";

describe("createTranslator", () => {
	it("translates typed namespace keys for the resolved locale", () => {
		const t = createTranslator("en", { common });
		expect(t("common.language")).toBe("Language");
	});

	it("allows additional locale-complete namespaces", () => {
		const library = createNamespace({
			en: {
				release: "Release",
				greeting: "Hello {name}; again, {name}.",
			},
			ru: {
				release: "Релиз",
				greeting: "Здравствуйте, {name}; снова {name}.",
			},
		});
		const t = createTranslator("ru", { common, library });
		expect(t("library.release")).toBe("Релиз");
		expect(t("library.greeting", { name: "Аня" })).toBe(
			"Здравствуйте, Аня; снова Аня.",
		);

		if (false) {
			// @ts-expect-error A template message requires every named value.
			t("library.greeting");
			// @ts-expect-error Parameter names must match the message placeholders.
			t("library.greeting", { person: "Аня" });
			// @ts-expect-error Messages without placeholders do not accept parameters.
			t("library.release", { name: "Аня" });
		}
	});

	it("requires English and Russian messages to use the same placeholders", () => {
		if (false) {
			createNamespace({
				en: { greeting: "Hello {name}" },
				// @ts-expect-error Locale catalogs must have matching placeholder names.
				ru: { greeting: "Привет, {person}" },
			});
			createNamespace({
				en: { first: "First", second: "Second" },
				// @ts-expect-error Both locales must define the same message keys.
				ru: { first: "Первый" },
			});
		}
	});
});
