import { expect, it } from "vitest";
import { metadataChanged, originalReleases } from "./catalogData";
import {
	explicitlyRepublish,
	refreshProviderHint,
	renameLocalTrack,
	upgradeAudioOnly,
} from "./trackActionModel";

function baseline() {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track?.published)
		throw new Error("Missing baseline fixture");
	return { release, track };
}

it("renames locally and refreshes the provider hint without rewriting current publication", () => {
	const { release, track } = baseline();
	const next = refreshProviderHint(renameLocalTrack(track, "New local title"));
	expect(next.title).toBe("New local title");
	expect(next.providerTitle).toBe(track.providerTitle);
	expect(next.providerCacheVersion).toBe((track.providerCacheVersion || 1) + 1);
	expect(next.published).toEqual(track.published);
	expect(next.metadataBaseline).toEqual(track.metadataBaseline);
	expect(metadataChanged(release, next)).toBe(true);
});

it("upgrades only audio, preserving current metadata and its baseline", () => {
	const { track } = baseline();
	expect(track.published?.codec).toBe("MP3");
	const upgraded = upgradeAudioOnly(track);
	expect(upgraded.published?.codec).toBe("FLAC");
	expect(upgraded.published?.tags).toEqual(track.published?.tags);
	expect(upgraded.published?.coverHash).toEqual(track.published?.coverHash);
	expect(upgraded.published?.lyrics).toEqual(track.published?.lyrics);
	expect(upgraded.metadataBaseline).toEqual(track.metadataBaseline);
	expect(upgraded.metadataBaselineExtras).toEqual(track.metadataBaselineExtras);
});

it("refuses an unconfirmed downgrade and replaces only the current state after confirmation", () => {
	const { release, track } = baseline();
	const upgraded = { ...upgradeAudioOnly(track), sourceId: "north-t1-mp3" };
	expect(explicitlyRepublish(release, upgraded, false)).toBe(upgraded);
	const result = explicitlyRepublish(release, upgraded, true);
	expect(result.published?.codec).toBe("MP3");
	expect(result.published?.sourceId).toBe("north-t1-mp3");
	expect(result.metadataBaseline).toEqual(result.published?.tags);
	expect(result.observedFile).toBeNull();
});
