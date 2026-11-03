import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { OriginalRelease } from "../catalogData";
import { ReleaseMetadataEditor } from "./ReleaseMetadataEditor";

const release = (
	overrides: Partial<OriginalRelease> = {},
): OriginalRelease => ({
	id: "release-1",
	title: "Original title",
	artistId: "artist-1",
	year: "2020",
	country: "GB",
	catalog: "CAT-1",
	label: "Label",
	providerId: "provider-1",
	provider: "MusicBrainz",
	metadata: {
		date: "2020-04-09",
		country: "GB",
		label: "Label",
		catalogNumber: "CAT-1",
		type: "album",
	},
	credits: [
		{ artistId: "artist-1", credit: "First", joinPhrase: " & " },
		{ artistId: "artist-2", credit: "Second", joinPhrase: "" },
	],
	metadataGenres: [{ id: "genre-1", name: "Rock" }],
	tracks: [],
	...overrides,
});

it("shows legacy fields and preserves precise release dates", () => {
	render(
		<ReleaseMetadataEditor
			release={release()}
			artists={[]}
			genresLoaded
			onSave={() => undefined}
		/>,
	);
	expect(screen.getByLabelText("Дата выпуска")).toHaveValue("2020-04-09");
	expect(screen.getByLabelText("Каталожный номер")).toHaveValue("CAT-1");
	expect(screen.getByLabelText("Лейбл")).toHaveValue("Label");
	expect(screen.getByText(/Связи с провайдером/)).toBeInTheDocument();
	expect(screen.getByText("provider-1")).not.toBeVisible();
	fireEvent.click(screen.getByText(/Связи с провайдером/));
	expect(screen.getByText("provider-1")).toBeInTheDocument();
});

it("saves edited metadata without truncating a partial date or reordering credits", async () => {
	const onSave = vi.fn();
	render(
		<ReleaseMetadataEditor
			release={release()}
			artists={[
				{ id: "artist-1", name: "One", sortName: "One, The" },
				{ id: "artist-2", name: "Two" },
			]}
			genresLoaded
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(screen.getByLabelText("Издание"), {
		target: { value: "Updated" },
	});
	fireEvent.change(screen.getByLabelText("Дата выпуска"), {
		target: { value: "2021-06" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	await screen.findByRole("button", { name: "Редактировать" });
	const updated = onSave.mock.calls[0][0] as OriginalRelease;
	expect(updated.title).toBe("Updated");
	expect(updated.year).toBe("2021");
	expect(updated.metadata?.date).toBe("2021-06");
	expect(updated.metadataValues?.releasedate).toEqual(["2021-06"]);
	expect(
		updated.credits?.map((credit) => [credit.artistId, credit.joinPhrase]),
	).toEqual([
		["artist-1", " & "],
		["artist-2", ""],
	]);
	expect(updated.metadataGenres).toEqual([{ id: "genre-1", name: "Rock" }]);
});

it("keeps extra release scripts editable and Cancel discards the draft", () => {
	const onSave = vi.fn();
	render(
		<ReleaseMetadataEditor
			release={release()}
			artists={[]}
			genresLoaded
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(screen.getByLabelText("Издание"), {
		target: { value: "Discard me" },
	});
	fireEvent.change(screen.getByLabelText("Уточнение"), {
		target: { value: "Special edition" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Отменить" }));
	expect(screen.getByLabelText("Издание")).toHaveValue("Original title");
	expect(screen.getByLabelText("Уточнение")).toHaveValue("");
	expect(onSave).not.toHaveBeenCalled();
});
