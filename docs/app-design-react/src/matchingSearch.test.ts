import { expect, it } from "vitest";
import {
	findExactMatchingIdentity,
	matchingPage,
	searchMatchingArtists,
	searchMatchingReleases,
} from "./matchingSearch";
import { incomingFixtures, providerReleases } from "./incomingModel";

it("requires explicit text search and only returns fixture-backed releases", () => {
	expect(
		searchMatchingReleases(
			"Northern Lines",
			providerReleases,
			incomingFixtures.artists,
		),
	).toEqual(
		expect.arrayContaining([
			expect.objectContaining({ id: "p-north" }),
			expect.objectContaining({ id: "p-japan" }),
		]),
	);
	expect(
		searchMatchingReleases(
			"release not in fixtures",
			providerReleases,
			incomingFixtures.artists,
		),
	).toEqual([]);
});

it("searches fixture artists and pages results with a default size of ten", () => {
	expect(
		searchMatchingArtists("Mira Vale", incomingFixtures.artists).map(
			(artist) => artist.id,
		),
	).toContain("mira");
	expect(
		matchingPage(
			Array.from({ length: 21 }, (_, index) => index),
			1,
		),
	).toEqual({
		items: [10, 11, 12, 13, 14, 15, 16, 17, 18, 19],
		page: 1,
		pageCount: 3,
	});
});

it("matches only a fixture-native identity and explains unavailable MusicBrainz identities", () => {
	const native = findExactMatchingIdentity(
		"release",
		"p-north",
		incomingFixtures.artists,
		providerReleases,
	);
	expect(native.items.map((release) => release.id)).toEqual(["p-north"]);
	const unavailableMBID = findExactMatchingIdentity(
		"release",
		"00000000-0000-4000-8000-000000000001",
		incomingFixtures.artists,
		providerReleases,
	);
	expect(unavailableMBID.items).toEqual([]);
	expect(unavailableMBID.message).toMatch(/нет MusicBrainz MBID\/URL/);
	const unavailableURL = findExactMatchingIdentity(
		"release",
		"https://musicbrainz.org/release/00000000-0000-4000-8000-000000000001",
		incomingFixtures.artists,
		providerReleases,
	);
	expect(unavailableURL.items).toEqual([]);
	expect(unavailableURL.message).toMatch(/нет MusicBrainz MBID\/URL/);
});
