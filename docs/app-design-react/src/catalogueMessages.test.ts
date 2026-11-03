import { describe, expect, it } from "vitest";
import { catalogueMessage } from "./catalogueMessages";

describe("catalogue messages", () => {
	it("formats matching English and Russian entries for the active locale", () => {
		expect(catalogueMessage("en", "selectTrack", { track: "Blue Train" })).toBe(
			"Select Blue Train",
		);
		expect(catalogueMessage("ru", "selectTrack", { track: "Blue Train" })).toBe(
			"Выбрать Blue Train",
		);
	});
});
