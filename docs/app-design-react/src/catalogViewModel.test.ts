import { expect, it } from "vitest";
import type { CatalogArtist } from "./catalogData";
import {
	albumSearchText,
	artistReleases,
	catalogArtistMatches,
	missingCatalogGroups,
	normalizeCatalogSearch,
	trackSearchText,
	type AbsentCatalogGroup,
	type CatalogRelease,
	type CatalogTrack,
} from "./catalogViewModel";

const artists: CatalogArtist[] = [
	{ id: "lead", name: "Lead Artist", genre: "Ambient" },
	{ id: "guest", name: "Guest Artist", genre: "Jazz" },
	{ id: "missing", name: "Missing Artist", genre: "Folk" },
];

const track = (id: string, artistId: string | null = null) =>
	({ id, title: `Track ${id}`, artistId, published: false }) as CatalogTrack;

const release = (partial: Partial<CatalogRelease> = {}) =>
	({
		id: "album",
		title: "Shared Album",
		artistId: "lead",
		year: "2024",
		country: "GB",
		catalog: "CAT-1",
		tracks: [track("one"), track("two")],
		...partial,
	}) as CatalogRelease;

const missingGroup: AbsentCatalogGroup = {
	id: "missing-release",
	title: "Unreleased Album",
	artistId: "missing",
	groupId: "absent-group",
	editions: [
		{
			id: "edition",
			year: "1999",
			country: "JP",
			format: "Vinyl",
			disambiguation: "Deluxe pressing",
			titles: ["Hidden Song"],
		},
	],
};

it("uses de-duplicated, credit-aware artist release membership", () => {
	const credited = release({
		credits: [
			{ artistId: "guest", credit: "Guest billing" },
			{ artistId: "guest", name: "Guest Artist" },
		],
	});
	expect(artistReleases([credited], "guest")).toEqual([credited]);
	expect(artistReleases([credited], "lead")).toEqual([credited]);
	expect(artistReleases([credited], "missing")).toEqual([]);
});

it("includes missing-only artists for the original filters and excludes represented groups", () => {
	const visible = missingCatalogGroups(
		[missingGroup],
		[],
		artists,
		"Hidden Song",
		"all",
	);
	expect(visible).toEqual([missingGroup]);
	expect(
		missingCatalogGroups([missingGroup], [], artists, "", "missing"),
	).toEqual([missingGroup]);
	expect(
		catalogArtistMatches(
			artists[2],
			[],
			visible,
			"Hidden Song",
			"all",
			() => false,
		),
	).toBe(true);
	expect(
		missingCatalogGroups(
			[missingGroup],
			[release({ releaseGroupId: "absent-group" })],
			artists,
			"",
			"all",
		),
	).toEqual([]);
	expect(
		missingCatalogGroups([missingGroup], [], artists, "", "available"),
	).toEqual([]);
	expect(
		missingCatalogGroups([missingGroup], [], artists, "", "published"),
	).toEqual([]);
});

it("matches library artists by unified availability and publication state", () => {
	const mixed = release({
		tracks: [
			{ ...track("available-published"), published: true },
			{ ...track("unavailable-unpublished"), published: false },
		],
	});
	const hasAudio = (item: CatalogTrack) => item.id === "available-published";
	for (const filter of ["all", "available", "published"] as const) {
		expect(
			catalogArtistMatches(artists[0], [mixed], [], "Lead", filter, hasAudio),
		).toBe(true);
	}
	expect(
		catalogArtistMatches(artists[0], [mixed], [], "Lead", "missing", hasAudio),
	).toBe(true);
	expect(
		catalogArtistMatches(
			artists[0],
			[mixed],
			[],
			"Lead",
			"available",
			() => false,
		),
	).toBe(false);
});

it("searches normalized artist, credit, track, and complete absent-edition metadata", () => {
	const creditedRelease = release({
		credits: [{ artistId: "guest", credit: "Guest Artist" }],
	});
	const guestRelease = release({
		credits: [{ artistId: "guest", credit: "Guest Artist" }],
	});
	expect(
		catalogArtistMatches(
			artists[1],
			[guestRelease],
			[],
			"jazz",
			"all",
			() => false,
		),
	).toBe(true);
	expect(
		normalizeCatalogSearch(albumSearchText(creditedRelease, artists)).includes(
			normalizeCatalogSearch(" Guest   Artist "),
		),
	).toBe(true);
	expect(
		albumSearchText(
			release({ credits: [{ artistId: "guest", name: "Guest Name" }] }),
			artists,
		),
	).toContain("Guest Name");
	expect(
		normalizeCatalogSearch(
			trackSearchText(track("one", "guest"), creditedRelease, artists),
		).includes(normalizeCatalogSearch("guest artist")),
	).toBe(true);
	for (const query of [
		"1999",
		"jp",
		"vinyl",
		"deluxe pressing",
		"hidden song",
	]) {
		expect(
			missingCatalogGroups([missingGroup], [], artists, query, "all"),
		).toEqual([missingGroup]);
	}
	expect(normalizeCatalogSearch("  Ｇｕｅｓｔ\t Artist ")).toBe("guest artist");
});
