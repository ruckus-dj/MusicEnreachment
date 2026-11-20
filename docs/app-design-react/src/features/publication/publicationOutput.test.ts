import { expect, it } from "vitest";
import { choosePublicationOutput } from "./publicationOutput";

const outputOptions = {
	outputDirectory: "/srv/music",
	artist: "Mira Vale",
	year: "2021",
	release: "Northern Lines",
	position: 1,
	title: "First Light",
};

it("preserves the selected FLAC source extension for original-format output", () => {
	expect(
		choosePublicationOutput(
			{ path: "/sources/first-light.flac", name: "first-light.flac" },
			{ ...outputOptions, format: "original" },
		),
	).toEqual({
		path: "/srv/music/Mira Vale/2021 - Northern Lines/01 - First Light.flac",
		container: "FLAC",
	});
});

it("preserves the selected MP3 source extension for original-format output", () => {
	expect(
		choosePublicationOutput(
			{ path: "/sources/first-light.mp3", name: "first-light.mp3" },
			{ ...outputOptions, format: "original" },
		),
	).toEqual({
		path: "/srv/music/Mira Vale/2021 - Northern Lines/01 - First Light.mp3",
		container: "MP3",
	});
});

it("uses MKA output independently of the selected source extension", () => {
	expect(
		choosePublicationOutput(
			{ path: "/sources/first-light.flac", name: "first-light.flac" },
			{ ...outputOptions, format: "mka" },
		),
	).toEqual({
		path: "/srv/music/Mira Vale/2021 - Northern Lines/01 - First Light.mka",
		container: "MKA",
	});
});
