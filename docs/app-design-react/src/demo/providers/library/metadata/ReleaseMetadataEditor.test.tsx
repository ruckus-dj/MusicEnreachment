import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { I18nProvider } from "../../../../app/i18n";
import { ReleaseMetadataEditorOutlet } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorOutlet";
import {
	desiredTags,
	effectiveReleaseTitle,
	originalArtists,
	originalReleases,
} from "../catalogData";
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

function renderEditor(release: (typeof originalReleases)[number]) {
	let saved: typeof release | undefined;
	const view = render(
		<I18nProvider>
			<ReleaseMetadataEditorOutlet
				source={{
					bind: (children: ReactNode, visible: boolean) => (
						<ReleaseMetadataEditor
							release={release}
							artists={originalArtists}
							genresLoaded
							visible={visible}
							onSave={(updated) => {
								saved = updated;
							}}
						>
							{children}
						</ReleaseMetadataEditor>
					),
				}}
			/>
		</I18nProvider>,
	);
	return { saved: () => saved, unmount: view.unmount };
}

function titleState() {
	return screen.getByRole("combobox", {
		name: "Title — Override state",
	});
}

function titleInput() {
	return document.querySelector<HTMLInputElement>(
		'input[data-metadata-keys~="album"]',
	);
}

it("persists an explicitly cleared release title and resets it on cancel", () => {
	const release = structuredClone(originalReleases[0]);
	if (!release) throw new Error("Missing release fixture");
	const editor = renderEditor(release);

	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	expect(titleState()).toHaveValue("absent");
	fireEvent.change(titleState(), { target: { value: "explicit_empty" } });
	fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	expect(titleState()).toHaveValue("absent");

	fireEvent.change(titleState(), { target: { value: "explicit_empty" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const updated = editor.saved();
	expect(updated?.comparisonOverrides).toHaveProperty("album", []);
	expect(updated?.metadataValues).toHaveProperty("album", []);
	expect(updated?.title).toBe("Northern Lines");
	expect(effectiveReleaseTitle(updated!)).toBe("");
	const track = updated?.tracks.find((item) => item.published);
	expect(track).toBeDefined();
	expect(desiredTags(updated!, track!).ALBUM).toEqual([]);
});

it("preserves the title draft through state changes and reopens overrides", () => {
	const release = structuredClone(originalReleases[0]);
	if (!release) throw new Error("Missing release fixture");
	const first = renderEditor(release);

	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	fireEvent.change(titleState(), { target: { value: "value" } });
	const input = titleInput();
	expect(input).not.toBeNull();
	if (!input) throw new Error("Missing album metadata field");
	fireEvent.change(input, { target: { value: "Northern Lines Deluxe" } });
	fireEvent.change(titleState(), { target: { value: "absent" } });
	fireEvent.change(titleState(), { target: { value: "value" } });
	expect(titleInput()).toHaveValue("Northern Lines Deluxe");
	fireEvent.change(titleState(), { target: { value: "explicit_empty" } });
	fireEvent.change(titleState(), { target: { value: "value" } });
	expect(titleInput()).toHaveValue("Northern Lines Deluxe");
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const valued = first.saved();
	expect(valued?.comparisonOverrides).toHaveProperty("album", [
		"Northern Lines Deluxe",
	]);
	expect(valued?.metadataValues).toHaveProperty("album", [
		"Northern Lines Deluxe",
	]);
	expect(valued?.title).toBe("Northern Lines");
	const valuedTrack = valued?.tracks.find((item) => item.published);
	expect(valuedTrack).toBeDefined();
	expect(desiredTags(valued!, valuedTrack!).ALBUM).toEqual([
		"Northern Lines Deluxe",
	]);
	first.unmount();

	const reopened = renderEditor(valued!);
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	expect(titleState()).toHaveValue("value");
	expect(titleInput()).toHaveValue("Northern Lines Deluxe");
	const emptyInput = titleInput();
	if (!emptyInput) throw new Error("Missing album metadata field");
	fireEvent.change(emptyInput, { target: { value: "" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const emptyValue = reopened.saved();
	expect(emptyValue?.comparisonOverrides).toHaveProperty("album", [""]);
	expect(emptyValue?.metadataValues).toHaveProperty("album", [""]);
	const emptyTrack = emptyValue?.tracks.find((item) => item.published);
	expect(emptyTrack).toBeDefined();
	expect(desiredTags(emptyValue!, emptyTrack!).ALBUM).toEqual([""]);
	reopened.unmount();

	const clearedEditor = renderEditor(emptyValue!);
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	fireEvent.change(titleState(), { target: { value: "explicit_empty" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const cleared = clearedEditor.saved();
	expect(cleared?.comparisonOverrides).toHaveProperty("album", []);
	expect(cleared?.metadataValues).toHaveProperty("album", []);
	expect(desiredTags(cleared!, clearedTrack(cleared)).ALBUM).toEqual([]);
	clearedEditor.unmount();

	const inheritedEditor = renderEditor(cleared!);
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	expect(titleState()).toHaveValue("explicit_empty");
	fireEvent.change(titleState(), { target: { value: "absent" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const inherited = inheritedEditor.saved();
	expect(inherited?.comparisonOverrides).not.toHaveProperty("album");
	expect(inherited?.title).toBe("Northern Lines");
	expect(effectiveReleaseTitle(inherited!)).toBe("Northern Lines");
	expect(desiredTags(inherited!, clearedTrack(inherited)).ALBUM).toEqual([
		"Northern Lines",
	]);
});

it("keeps typed release basics explicit, inherited and cleared without rewriting base facts", async () => {
	const fixture = originalReleases.find(
		(item) => item.id === "north-japan-local",
	);
	if (!fixture) throw new Error("Missing release fixture");
	const release = structuredClone(fixture);
	const first = renderEditor(release);

	fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
	fireEvent.change(
		screen.getByRole("combobox", { name: "Release date — Override state" }),
		{ target: { value: "value" } },
	);
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	expect(first.saved()).toBeUndefined();

	fireEvent.change(
		screen.getByRole("combobox", { name: "Release date — Override state" }),
		{ target: { value: "absent" } },
	);
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	expect(first.saved()?.releaseBasicsIntent).toBeUndefined();

	fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
	fireEvent.change(
		screen.getByRole("combobox", { name: "Release date — Override state" }),
		{ target: { value: "value" } },
	);
	const dateInput = document.querySelector<HTMLInputElement>(
		'input[data-metadata-keys~="releasedate"]',
	);
	if (!dateInput) throw new Error("Missing release date field");
	fireEvent.change(dateInput, { target: { value: "2022-03" } });
	fireEvent.change(
		screen.getByRole("combobox", { name: "Country — Override state" }),
		{ target: { value: "value" } },
	);
	const countryInput = document.querySelector<HTMLInputElement>(
		'input[data-metadata-keys~="releasecountry"]',
	);
	if (!countryInput) throw new Error("Missing release country field");
	fireEvent.change(countryInput, { target: { value: release.country } });
	fireEvent.change(
		screen.getByRole("combobox", { name: "Label — Override state" }),
		{ target: { value: "value" } },
	);
	fireEvent.click(screen.getByRole("button", { name: "Add value: Label" }));
	const labels = [
		...document.querySelectorAll<HTMLInputElement>(
			'input[data-metadata-keys~="recordlabel"]',
		),
	];
	expect(labels).toHaveLength(2);
	fireEvent.change(labels[0]!, { target: { value: "First label" } });
	fireEvent.change(labels[1]!, { target: { value: "Second label" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));

	const valued = first.saved();
	expect(valued?.releaseBasicsIntent).toEqual({
		releasedate: { state: "value", value: ["2022-03"] },
		releasecountry: { state: "value", value: [release.country] },
		recordlabel: {
			state: "value",
			value: ["First label", "Second label"],
		},
	});
	expect(valued?.year).toBe(release.year);
	expect(valued?.country).toBe(release.country);
	expect(valued?.label).toBe(release.label);
	expect(valued?.catalog).toBe(release.catalog);
	expect(valued?.metadata).toEqual(release.metadata);
	const valuedTags = desiredTags(valued!, clearedTrack(valued));
	expect(valuedTags.RELEASEDATE).toEqual(["2022-03"]);
	expect(valuedTags.RELEASECOUNTRY).toEqual([release.country]);
	expect(valuedTags.LABEL).toEqual(["First label", "Second label"]);
	first.unmount();

	const second = renderEditor(valued!);
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	fireEvent.change(
		screen.getByRole("combobox", { name: "Country — Override state" }),
		{ target: { value: "explicit_empty" } },
	);
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	const cleared = second.saved();
	expect(cleared?.releaseBasicsIntent?.releasecountry).toEqual({
		state: "explicit_empty",
	});
	expect(desiredTags(cleared!, clearedTrack(cleared)).RELEASECOUNTRY).toEqual(
		[],
	);
	expect(cleared?.country).toBe(release.country);
	second.unmount();

	const third = renderEditor(cleared!);
	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	fireEvent.change(
		screen.getByRole("combobox", { name: "Country — Override state" }),
		{ target: { value: "absent" } },
	);
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	const inherited = third.saved();
	expect(inherited?.releaseBasicsIntent?.releasecountry).toBeUndefined();
	expect(
		desiredTags(inherited!, clearedTrack(inherited)).RELEASECOUNTRY,
	).toEqual([release.country]);
	expect(inherited?.country).toBe(release.country);
});

function clearedTrack(release: (typeof originalReleases)[number] | undefined) {
	const track = release?.tracks.find((item) => item.published);
	if (!track) throw new Error("Missing published track fixture");
	return track;
}
