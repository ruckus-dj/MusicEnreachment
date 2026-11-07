import {
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { genreCatalog } from "../genres/catalogue";
import { I18nProvider } from "../i18n";
import { MetadataEditor } from "./MetadataEditor";

type TestGenreReference = { id?: string; name: string };

function renderEditor(
	providerGenreBaseline?: { genres: TestGenreReference[] } | null,
	onSave = vi.fn(),
) {
	render(
		<I18nProvider>
			<MetadataEditor
				label="Metadata"
				scope="release"
				values={{}}
				onSave={onSave}
				providerGenreBaseline={providerGenreBaseline}
			/>
		</I18nProvider>,
	);
	return onSave;
}

function catalogReference(index: number): TestGenreReference {
	const genre = genreCatalog[index];
	if (!genre) throw new Error(`Missing genre catalog fixture ${index}`);
	return { id: genre.id, name: genre.name };
}

const copy = {
	en: {
		label: "MusicBrainz genres",
		unavailable: "No MusicBrainz genre baseline is available.",
		empty: "MusicBrainz accepted an empty genre list.",
	},
	ru: {
		label: "Жанры MusicBrainz",
		unavailable: "Данные о жанрах MusicBrainz недоступны.",
		empty: "MusicBrainz вернул пустой список жанров.",
	},
} as const;

describe.each([
	"en",
	"ru",
] as const)("MetadataEditor provider genre baseline (%s)", (locale) => {
	beforeEach(() => {
		localStorage.setItem("music-design-v2-language", locale);
	});

	it("keeps the default presentation when the channel is not provided", () => {
		renderEditor(undefined);
		expect(
			screen.queryByRole("region", { name: copy[locale].label }),
		).toBeNull();
	});

	it("renders an unavailable row for a missing accepted baseline", () => {
		renderEditor(null);
		const row = screen.getByRole("region", { name: copy[locale].label });
		expect(within(row).getByText(copy[locale].unavailable)).toBeInTheDocument();
		expect(within(row).queryByRole("textbox")).toBeNull();
		expect(within(row).queryByRole("button")).toBeNull();
	});

	it("renders an accepted empty baseline without inventing genres", () => {
		renderEditor({ genres: [] });
		const row = screen.getByRole("region", { name: copy[locale].label });
		expect(within(row).getByText(copy[locale].empty)).toBeInTheDocument();
		expect(within(row).queryAllByRole("listitem")).toHaveLength(0);
	});
});

it("renders typed catalog references readonly and outside the draft", async () => {
	localStorage.setItem("music-design-v2-language", "en");
	const references = [catalogReference(0), catalogReference(1)];
	const onSave = renderEditor({ genres: references });

	const row = screen.getByRole("region", { name: copy.en.label });
	const items = within(row).getAllByRole("listitem");
	expect(items.map((item) => item.textContent)).toEqual(
		references.map((reference) => reference.name),
	);
	for (const control of ["textbox", "button", "combobox"] as const) {
		expect(within(row).queryAllByRole(control)).toHaveLength(0);
	}

	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	const editingRow = screen.getByRole("region", { name: copy.en.label });
	expect(within(editingRow).getAllByRole("listitem")).toHaveLength(2);
	for (const control of ["textbox", "button", "combobox"] as const) {
		expect(within(editingRow).queryAllByRole(control)).toHaveLength(0);
	}

	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(onSave).toHaveBeenCalled());
	expect(onSave.mock.calls[0]?.[1]?.genres).toBeUndefined();
});
