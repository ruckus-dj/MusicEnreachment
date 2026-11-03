import { describe, expect, it } from "vitest";
import { normalizeGenre, normalizeGenreName } from "./genreNames";

describe("normalizeGenreName", () => {
	it.each([
		["ambient", "Ambient"],
		["hip hop", "Hip Hop"],
		["post-rock", "Post-Rock"],
		["drum'n'bass", "Drum'n'bass"],
		["r&b", "R&B"],
		["electronic/dance", "Electronic/Dance"],
		["uk garage", "UK Garage"],
		["us country", "US Country"],
		["edm idm ebm", "EDM IDM EBM"],
		["k/j pop", "K/J Pop"],
		["1990s indie-rock", "1990s Indie-Rock"],
		["café del mar", "Café Del Mar"],
		["Beyoncé’s favorites", "Beyoncé’s Favorites"],
		["  ", "  "],
		["", ""],
	])("normalizes %j without changing its separators", (raw, expected) => {
		expect(normalizeGenreName(raw)).toBe(expected);
	});

	it("is idempotent", () => {
		const normalized = normalizeGenreName("r&b / post-rock — 1990s café");
		expect(normalizeGenreName(normalized)).toBe(normalized);
	});
});

describe("normalizeGenre", () => {
	it("preserves the stable ID and unmodified source name", () => {
		expect(normalizeGenre({ id: "mbid-1", name: "hip hop" })).toEqual({
			id: "mbid-1",
			sourceName: "hip hop",
			name: "Hip Hop",
		});
	});

	it("keeps distinct IDs distinct even when display names normalize alike", () => {
		const first = normalizeGenre({ id: "mbid-1", name: "ambient" });
		const second = normalizeGenre({ id: "mbid-2", name: "Ambient" });

		expect(first.name).toBe(second.name);
		expect(first.id).not.toBe(second.id);
		expect(first.sourceName).not.toBe(second.sourceName);
	});
});
