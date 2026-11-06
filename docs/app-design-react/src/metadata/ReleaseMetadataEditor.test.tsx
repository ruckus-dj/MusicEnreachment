import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { originalArtists, originalReleases } from "../catalogData";
import { I18nProvider } from "../i18n";
import { ReleaseMetadataEditor } from "./ReleaseMetadataEditor";

let previousLanguagePreference: string | null = null;

beforeEach(() => {
	previousLanguagePreference = window.localStorage.getItem(
		"music-design-v2-language",
	);
	window.localStorage.setItem("music-design-v2-language", "en");
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

it("persists an explicit empty album metadata override", () => {
	const release = structuredClone(originalReleases[0]);
	if (!release) throw new Error("Missing release fixture");
	let saved: typeof release | undefined;
	render(
		<I18nProvider>
			<ReleaseMetadataEditor
				release={release}
				artists={originalArtists}
				genresLoaded
				onSave={(updated) => {
					saved = updated;
				}}
			/>
		</I18nProvider>,
	);

	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	const albumField = document.querySelector<HTMLInputElement>(
		'input[data-metadata-keys~="album"]',
	);
	expect(albumField).not.toBeNull();
	if (!albumField) throw new Error("Missing album metadata field");
	fireEvent.change(albumField, { target: { value: "" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	expect(saved?.metadataValues).toHaveProperty("album", []);
	expect(saved?.title).toBe("");
});
