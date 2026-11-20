import { expect, it } from "vitest";
import {
	metadataChanged,
	originalFiles,
	originalReleases,
} from "./catalogData";
import {
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
	const sourceFiles = structuredClone(originalFiles);
	const source = sourceFiles[track.sourceId!];
	if (!source) throw new Error("Missing source fixture");
	source.tags.ENCODEDBY = ["Selected source encoder"];
	source.tags.ENCODING = ["Selected source encoding"];
	source.tags.ENCODERSETTINGS = ["Selected source settings"];
	const upgraded = upgradeAudioOnly(track, sourceFiles);
	const expectedBaseline = structuredClone(
		track.metadataBaseline ?? track.published!.tags,
	);
	expect(upgraded.published?.codec).toBe("FLAC");
	for (const [key, value] of Object.entries({
		ENCODEDBY: "Selected source encoder",
		ENCODING: "Selected source encoding",
		ENCODERSETTINGS: "Selected source settings",
	})) {
		expectedBaseline[key] = [value];
		expect(upgraded.published?.tags[key]).toEqual([value]);
		expect(upgraded.metadataBaseline?.[key]).toEqual([value]);
	}
	expect(upgraded.published?.coverHash).toEqual(track.published?.coverHash);
	expect(upgraded.published?.lyrics).toEqual(track.published?.lyrics);
	expect(upgraded.metadataBaseline).toEqual(expectedBaseline);
	expect(upgraded.metadataBaselineExtras).toEqual(track.metadataBaselineExtras);
});
