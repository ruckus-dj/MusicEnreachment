import { expect, it } from "vitest";
import type { OriginalRelease } from "./catalogData";
import type { AbsentCatalogGroup } from "./catalogViewModel";
import {
	combineLibraryAlbumEntries,
	libraryPageCount,
	libraryPageSlice,
	splitLibraryAlbumEntries,
} from "./libraryCatalogPage";

it("pages a filtered result at 50 items without shifting boundaries", () => {
	// Filtering happens before paging: 130 rows narrow to 65, so page 2 is a
	// short last page rather than a skipped or duplicated slice.
	const filtered = Array.from(
		{ length: 130 },
		(_, index) => `id-${index}`,
	).filter((_, index) => index % 2 === 0);
	expect(filtered).toHaveLength(65);
	expect(libraryPageCount(filtered.length)).toBe(2);
	expect(libraryPageSlice(filtered, 1)).toEqual(filtered.slice(0, 50));
	expect(libraryPageSlice(filtered, 2)).toEqual(filtered.slice(50));
	expect(libraryPageSlice(filtered, 2)).not.toContain(filtered[0]);
	expect(libraryPageSlice(filtered, 3)).toEqual([]);
	expect(libraryPageSlice(filtered, 0)).toEqual(filtered.slice(0, 50));
	expect(libraryPageCount(0)).toBe(1);
	expect(libraryPageCount(100)).toBe(2);
});

const release = (index: number): OriginalRelease => ({
	id: `release-${index}`,
	title: `Release ${index}`,
	artistId: null,
	year: "2026",
	country: "",
	catalog: "",
	label: "",
	providerId: null,
	tracks: [],
});

const group = (index: number): AbsentCatalogGroup => ({
	id: `group-${index}`,
	title: `Group ${index}`,
	artistId: "artist-1",
	groupId: `mbid-${index}`,
	editions: [],
});

it("applies one page boundary after combining releases and missing groups", () => {
	const releases = Array.from({ length: 60 }, (_, index) => release(index + 1));
	const groups = Array.from({ length: 5 }, (_, index) => group(index + 1));
	const entries = combineLibraryAlbumEntries(releases, groups);
	expect(entries).toHaveLength(65);

	const first = splitLibraryAlbumEntries(libraryPageSlice(entries, 1));
	expect(first.releases).toHaveLength(50);
	expect(first.missingGroups).toEqual([]);

	const second = splitLibraryAlbumEntries(libraryPageSlice(entries, 2));
	expect(second.releases.map(({ id }) => id)).toEqual(
		releases.slice(50).map(({ id }) => id),
	);
	expect(second.missingGroups.map(({ id }) => id)).toEqual(
		groups.map(({ id }) => id),
	);
});
