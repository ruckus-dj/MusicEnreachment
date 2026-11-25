import { describe, expect, it } from "vitest";
import {
	genreCatalog,
	resolveCatalogGenreName,
	resolveCatalogGenreValue,
} from "./catalogue";

describe("bundled MusicBrainz genre catalogue", () => {
	it("loads all 2,220 unique MusicBrainz IDs and normalizes names in English", () => {
		expect(genreCatalog).toHaveLength(2220);
		expect(new Set(genreCatalog.map((genre) => genre.id)).size).toBe(2220);
		expect(genreCatalog[0]).toEqual({
			id: "18797864-41b0-4602-8241-adbee761774c",
			sourceName: "2 tone",
			name: "2 Tone",
		});
	});

	it("uses exact known IDs and names while preserving unknown values", () => {
		const firstGenre = genreCatalog[0];
		expect(resolveCatalogGenreName({ id: firstGenre.id })).toBe(
			firstGenre.name,
		);
		expect(resolveCatalogGenreName({ name: firstGenre.sourceName })).toBe(
			firstGenre.name,
		);
		expect(
			resolveCatalogGenreName({ id: "unknown-id", name: "user-defined style" }),
		).toBe("user-defined style");
		expect(resolveCatalogGenreValue("user-defined style")).toBe(
			"user-defined style",
		);
	});
});
