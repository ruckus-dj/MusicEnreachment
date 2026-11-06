import { expect, it } from "vitest";
import { originalReleases } from "./catalogData";
import { publicationMessage } from "./publicationMessages";
import { publicationArtistNotices } from "./publicationArtistNotices";

function baseline() {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north")!,
	);
	return { release, track: structuredClone(release.tracks[0]) };
}

it("reports missing MusicBrainz IDs per recording and release artist group", () => {
	const { release, track } = baseline();
	const artists = [
		{ id: "recording-artist", name: "Recording Artist" },
		{ id: "release-artist", name: "Release Artist" },
	];
	track.recordingCredits = [
		{
			artistId: "recording-artist",
			creditName: "Recording Artist",
			joinPhrase: "",
		},
	];
	release.credits = [
		{ artistId: "release-artist", credit: "Release Artist", joinPhrase: "" },
	];

	const notices = publicationArtistNotices(release, track, artists, "en");
	expect(notices).toContain(
		publicationMessage("en", "recordingArtistMissingMbid"),
	);
	expect(notices).toContain(
		publicationMessage("en", "releaseArtistMissingMbid"),
	);
});

it("reports same-name artist identities separately from missing IDs", () => {
	const { release, track } = baseline();
	const artists = [
		{
			id: "one",
			name: "Shared Name",
			mbid: "11111111-1111-4111-8111-111111111111",
		},
		{
			id: "two",
			name: "Shared Name",
			mbid: "22222222-2222-4222-8222-222222222222",
		},
	];
	track.recordingCredits = [
		{ artistId: "one", creditName: "Shared Name", joinPhrase: " & " },
		{ artistId: "two", creditName: "Shared Name", joinPhrase: "" },
	];
	const notices = publicationArtistNotices(release, track, artists, "en");
	expect(notices).toContain(
		publicationMessage("en", "recordingArtistNameCollision"),
	);
});
