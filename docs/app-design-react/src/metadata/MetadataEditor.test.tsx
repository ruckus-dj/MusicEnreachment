import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MetadataEditor } from "./MetadataEditor";
import { fieldsForScope, type MetadataScope } from "./registry";
import type { MetadataValues } from "./projection";

describe("MetadataEditor", () => {
	it.each([
		"release",
		"recording",
		"track",
		"audio",
	] as MetadataScope[])("renders every registered %s field in its own structured control", (scope) => {
		const { container } = render(
			<MetadataEditor
				label="Метаданные"
				scope={scope}
				values={{}}
				onSave={vi.fn()}
			/>,
		);
		const marked = [
			...container.querySelectorAll("[data-metadata-keys]"),
		].flatMap(
			(element) => element.getAttribute("data-metadata-keys")?.split(",") ?? [],
		);
		for (const field of fieldsForScope(scope))
			expect(marked).toContain(field.key);
	});

	it("edits in place and preserves unrelated and unknown metadata values", () => {
		const onSave = vi.fn();
		const values: MetadataValues = {
			title: ["Old title"],
			unknown_vendor_tag: ["keep"],
			artist: ["Artist"],
		};
		render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={values}
				onSave={onSave}
			/>,
		);
		const title = screen.getByRole("textbox", { name: "Название" });
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		expect(screen.getByRole("textbox", { name: "Название" })).toBe(title);
		fireEvent.change(title, { target: { value: "New title" } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledOnce();
		expect(onSave.mock.calls[0][0]).toMatchObject({
			title: ["New title"],
			unknown_vendor_tag: ["keep"],
			artist: ["Artist"],
		});
		expect(onSave.mock.calls[0][1]).toBeUndefined();
	});

	it("validates partial dates and keeps invalid edits in the draft", () => {
		const onSave = vi.fn();
		render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={{ originaldate: ["1999"] }}
				onSave={onSave}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		const date = screen.getByRole("textbox", {
			name: "Дата первоначального выпуска",
		});
		fireEvent.change(date, { target: { value: "1999-13" } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(screen.getByRole("alert")).toHaveTextContent("YYYY");
		expect(date).toHaveValue("1999-13");
		expect(onSave).not.toHaveBeenCalled();
	});

	it("edits explicitly supplied release genres without adding a recording tag", () => {
		const onSave = vi.fn();
		render(
			<MetadataEditor
				label="Издание"
				scope="release"
				values={{ album: ["Album"] }}
				genres={[]}
				onSave={onSave}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
		fireEvent.click(screen.getByRole("option", { name: "ambient" }));
		fireEvent.click(screen.getByRole("button", { name: "Готово" }));
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledWith(
			{ album: ["Album"] },
			{ genres: [{ id: "demo-genre-ambient", name: "ambient" }] },
		);
	});

	it("allows editing descriptive identifiers while protecting entity identities", () => {
		render(
			<MetadataEditor
				label="Издание"
				scope="release"
				values={{ asin: ["old-asin"], musicbrainz_discid: ["old-disc"] }}
				onSave={vi.fn()}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		expect(screen.getByRole("textbox", { name: "ASIN" })).not.toHaveProperty(
			"readOnly",
			true,
		);
		expect(
			screen.getByRole("textbox", { name: "MusicBrainz Disc ID" }),
		).not.toHaveProperty("readOnly", true);
	});

	it("keeps an asynchronous failure editable and offers impact confirmation before saving", async () => {
		const onSave = vi.fn().mockRejectedValueOnce(new Error("Try again"));
		render(
			<MetadataEditor
				label="Издание"
				scope="release"
				values={{ album: ["Old"] }}
				onSave={onSave}
				impact={["Позиция 1", "Позиция 2"]}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		const album = screen.getByRole("textbox", { name: "Издание" });
		fireEvent.change(album, { target: { value: "New" } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		const dialog = screen.getByRole("dialog", {
			name: "Подтверждение затронутых позиций",
		});
		expect(dialog).toHaveTextContent("Позиция 1");
		expect(dialog).toHaveTextContent("Позиция 2");
		expect(onSave).not.toHaveBeenCalled();
		fireEvent.click(
			screen.getByRole("button", { name: "Подтвердить сохранение" }),
		);
		expect(await screen.findByRole("alert")).toHaveTextContent("Try again");
		expect(album).toHaveValue("New");
		expect(album).not.toHaveProperty("readOnly", true);
	});

	it("saves required-fields validation and separately exposes local extra fields", () => {
		const onSave = vi.fn();
		render(
			<MetadataEditor
				label="Издание"
				scope="release"
				values={{ album: [] }}
				onSave={onSave}
				requiredKeys={["album"]}
				extraFields={[
					{
						key: "displayTitle",
						label: "Название в каталоге",
						value: "Old display title",
					},
				]}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		const extra = screen.getByRole("textbox", { name: "Название в каталоге" });
		fireEvent.change(extra, { target: { value: "New display title" } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(screen.getByRole("alert")).toHaveTextContent("обязательно");
		fireEvent.change(screen.getByRole("textbox", { name: "Издание" }), {
			target: { value: "Album" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave.mock.calls[0][1]).toEqual({
			extraValues: { displayTitle: "New display title" },
		});
	});
});
