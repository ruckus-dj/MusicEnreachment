import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { ArtistScreen } from "./ArtistScreen";
import {
	originalArtists,
	originalFiles,
	originalReleases,
} from "./catalogData";

let previousLanguagePreference: string | null = null;

beforeEach(() => {
	previousLanguagePreference = window.localStorage.getItem(
		"music-design-v2-language",
	);
	window.localStorage.setItem("music-design-v2-language", "ru");
});

afterEach(() => {
	if (previousLanguagePreference === null)
		window.localStorage.removeItem("music-design-v2-language");
	else
		window.localStorage.setItem(
			"music-design-v2-language",
			previousLanguagePreference,
		);
});

it.each([
	["unchanged", "Отклонений не отмечено"],
	["changed", "Изменён вне приложения"],
	["drift", "Изменён вне приложения"],
	["missing", "Отсутствует"],
	[
		"output-unavailable",
		"Выходной каталог недоступен; наличие файла не установлено",
	],
])("shows the correct publication observation for %s", (state, expected) => {
	const source = originalReleases.find((release) =>
		release.tracks.some((track) => track.published),
	);
	if (!source?.artistId) throw new Error("Missing published artist fixture");
	const release = structuredClone(source);
	const track = release.tracks.find((item) => item.published);
	if (!track) throw new Error("Missing published track fixture");
	release.tracks = [track];
	track.observedFile = {
		state,
		rootAvailable: state === "missing" ? true : undefined,
	};
	render(
		<ArtistScreen
			artistId={source.artistId}
			artists={originalArtists}
			releases={[release]}
			files={originalFiles}
		/>,
	);

	expect(screen.getByText(expected)).toBeInTheDocument();
});

it("distinguishes a missing file when the publication root is unavailable", () => {
	const source = originalReleases.find((release) =>
		release.tracks.some((track) => track.published),
	);
	if (!source?.artistId) throw new Error("Missing published artist fixture");
	const release = structuredClone(source);
	const track = release.tracks.find((item) => item.published);
	if (!track) throw new Error("Missing published track fixture");
	release.tracks = [track];
	track.observedFile = { state: "missing", rootAvailable: false };
	render(
		<ArtistScreen
			artistId={source.artistId}
			artists={originalArtists}
			releases={[release]}
			files={originalFiles}
		/>,
	);

	expect(
		screen.getByText(
			"Выходной каталог недоступен; наличие файла не установлено",
		),
	).toBeInTheDocument();
});
