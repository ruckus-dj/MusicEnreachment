import { describe, expect, it } from "vitest";
import { catalog } from "./i18n/catalog";
import { libraryRowsMessage, libraryRowsMessages } from "./libraryRowsMessages";

describe("library row localization", () => {
	it("registers the namespace in the primary i18n catalog", () => {
		expect(catalog.libraryRows).toBe(libraryRowsMessages);
	});

	it("provides complete English and Russian filter and selection labels", () => {
		for (const locale of ["en", "ru"] as const) {
			expect(libraryRowsMessage(locale, "filterState")).toBeTruthy();
			expect(libraryRowsMessage(locale, "filterAll")).toBeTruthy();
			expect(libraryRowsMessage(locale, "filterMissing")).toBeTruthy();
			expect(libraryRowsMessage(locale, "filterAvailable")).toBeTruthy();
			expect(libraryRowsMessage(locale, "filterPublished")).toBeTruthy();
			expect(libraryRowsMessage(locale, "confirmDeletion")).toBeTruthy();
		}
	});

	it("formats all catalog counts without language-specific inflection", () => {
		expect(libraryRowsMessage("en", "releasesCount", { count: 3 })).toBe(
			"Releases: 3",
		);
		expect(libraryRowsMessage("ru", "releasesCount", { count: 3 })).toBe(
			"Изданий: 3",
		);
		expect(
			libraryRowsMessage("ru", "audioCount", { available: 7, total: 29 }),
		).toBe("Аудио: 7 / 29");
		expect(
			libraryRowsMessage("ru", "frozenDeletionCounts", {
				selected: 4,
				published: 3,
				skipped: 1,
			}),
		).toBe(
			"Выбрано позиций: 4. Текущих публикаций в зафиксированном выборе: 3. Без публикации пропущено: 1.",
		);
	});
});
