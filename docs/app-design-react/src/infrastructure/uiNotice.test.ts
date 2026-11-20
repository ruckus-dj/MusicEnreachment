import { describe, expect, it } from "vitest";
import { formatUiNotice, type UiNotice } from "./uiNotice";

describe("formatUiNotice", () => {
	it("formats matching notices from the current locale and semantic parameters", () => {
		const notice: UiNotice = {
			namespace: "matching",
			id: "alreadyAssigned",
			params: { file: "track.flac" },
		};

		expect(formatUiNotice("en", notice)).toBe(
			"track.flac is already linked to this position.",
		);
		expect(formatUiNotice("ru", notice)).toBe(
			"track.flac уже связан с этой позицией.",
		);
		expect(notice).toEqual({
			namespace: "matching",
			id: "alreadyAssigned",
			params: { file: "track.flac" },
		});
	});

	it("formats publication success in the active locale", () => {
		const notice: UiNotice = {
			namespace: "publication",
			id: "applySuccess",
			params: { count: 2, linkCount: 1 },
		};

		expect(formatUiNotice("en", notice)).toBe(
			"Demo: 2 positions published. Links for 1 positions saved. Unselected positions and unmatched files were not changed.",
		);
		expect(formatUiNotice("ru", notice)).toBe(
			"Демо: опубликовано позиций: 2. Сохранены связи позиций: 1. Невыбранные позиции и неразмеченные файлы не изменены.",
		);
	});

	it("formats incoming and library notices using their namespaces", () => {
		expect(
			formatUiNotice("ru", {
				namespace: "incoming",
				id: "automaticPublished",
				params: { count: 3 },
			}),
		).toContain("Автоматически опубликовано позиций: 3.");
		expect(
			formatUiNotice("ru", {
				namespace: "library",
				id: "localReleaseSelected",
				params: { title: "Demo release" },
			}),
		).toBe("Для сопоставления входящих выбрано издание Local «Demo release».");
	});
});
