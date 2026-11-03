import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MetadataEditor } from "./MetadataEditor";
import type { MetadataValues } from "./projection";
import { fieldsForScope, type MetadataScope } from "./registry";

describe("MetadataEditor", () => {
	it.each([
		"release",
		"recording",
	] as const)("keeps %s disclosures in a single stable column", (scope) => {
		const { container } = render(
			<MetadataEditor
				label="Details"
				scope={scope}
				values={{}}
				onSave={vi.fn()}
			/>,
		);
		const stack = container.querySelector<HTMLElement>(
			"[data-metadata-sections]",
		);
		expect(stack).toHaveClass("grid-cols-1");
		expect(stack).not.toHaveClass("sm:grid-cols-2");
		const groups = [
			...stack!.querySelectorAll<HTMLDetailsElement>(":scope > details"),
		];
		expect(groups.length).toBeGreaterThan(1);
		for (const group of groups) {
			expect(group.className).not.toContain("col-span");
			expect(group.open).toBe(false);
		}
		const header = groups[0].querySelector("summary")!;
		fireEvent.click(header);
		expect(groups[0].open).toBe(true);
		expect(groups[1].open).toBe(false);
		expect(header).toHaveClass("w-full");
		fireEvent.click(header);
		expect(groups[0].open).toBe(false);
	});
	it("shows compact essentials first and keeps empty role rows out of the default view", () => {
		const { container } = render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={{ title: ["Track title"], composer: [], arranger: [] }}
				onSave={vi.fn()}
			/>,
		);
		expect(screen.getByRole("textbox", { name: "Название" })).toHaveValue(
			"Track title",
		);
		expect(container.querySelectorAll("details[open]")).toHaveLength(0);
		expect(screen.queryByRole("textbox", { name: "Имя участника" })).toBeNull();
		expect(
			container.querySelectorAll("[data-metadata-keys='composer'] input"),
		).toHaveLength(0);
	});

	it("retains an edited advanced draft when its disclosure is collapsed", () => {
		const onSave = vi.fn();
		const { container } = render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={{ title: ["Track title"], originaldate: ["1999"] }}
				onSave={onSave}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		const summary = [...container.querySelectorAll("details > summary")].find(
			(node) => node.textContent?.includes("Даты и музыкальные сведения"),
		);
		if (!summary)
			throw new Error("advanced composition disclosure was not rendered");
		fireEvent.click(summary);
		const date = screen.getByRole("textbox", {
			name: "Дата первоначального выпуска",
		});
		fireEvent.change(date, { target: { value: "2001-02" } });
		fireEvent.click(summary);
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledWith({
			title: ["Track title"],
			originaldate: ["2001-02"],
		});
	});

	it("keeps linked credits readable in the summary and editable in their disclosure", () => {
		render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={{ artist: ["Main credit", "Guest credit"] }}
				credits={[
					{
						artistId: "artist-1",
						creditName: "Main credit",
						joinPhrase: " & ",
					},
					{ artistId: "artist-2", creditName: "Guest credit", joinPhrase: "" },
				]}
				artists={[
					{ id: "artist-1", name: "Main artist" },
					{ id: "artist-2", name: "Guest artist" },
				]}
				onSave={vi.fn()}
			/>,
		);
		const credits = screen.getByText("Исполнители записи").parentElement;
		expect(credits).toHaveTextContent("Main credit & Guest credit");
		fireEvent.click(screen.getByText("Состав исполнителей и порядок"));
		expect(
			screen.getByRole("combobox", { name: "Связанный исполнитель 1" }),
		).toHaveValue("artist-1");
		expect(
			screen.getAllByRole("textbox", { name: "Имя в зачёте" })[0],
		).toHaveValue("Main credit");
	});

	it("does not reserve empty list-action space in read-only advanced sections", () => {
		const { container } = render(
			<MetadataEditor
				label="Запись"
				scope="recording"
				values={{ mood: ["Dream pop", "Ambient"] }}
				onSave={vi.fn()}
			/>,
		);
		const summary = [...container.querySelectorAll("details > summary")].find(
			(node) => node.textContent?.includes("Даты и музыкальные сведения"),
		);
		if (!summary) throw new Error("composition disclosure was not rendered");
		fireEvent.click(summary);
		expect(
			screen.queryByRole("button", { name: "Добавить значение" }),
		).toBeNull();
		expect(
			screen.queryByRole("button", { name: "Выше: Настроение 1" }),
		).toBeNull();
	});

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
		for (const summary of document.querySelectorAll("details > summary"))
			fireEvent.click(summary);
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
		for (const summary of document.querySelectorAll("details > summary"))
			fireEvent.click(summary);
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
		for (const summary of document.querySelectorAll("details > summary"))
			fireEvent.click(summary);
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
it("uses one genre picker rather than a second free-text genre editor", () => {
	const onSave = vi.fn();
	const { container } = render(
		<MetadataEditor
			label="Recording"
			scope="recording"
			values={{ title: ["Song"], genre: [] }}
			genres={[]}
			genresLoaded
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	expect(
		container.querySelectorAll("input[data-metadata-keys='genre']"),
	).toHaveLength(0);
	expect(screen.getAllByRole("button", { name: "Жанры" })).toHaveLength(1);
	fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
	fireEvent.click(screen.getByRole("option", { name: "rock" }));
	fireEvent.click(screen.getByRole("button", { name: "Готово" }));
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	expect(onSave).toHaveBeenCalledWith(
		expect.objectContaining({ genre: ["rock"] }),
		expect.objectContaining({
			genres: [{ id: "demo-genre-rock", name: "rock" }],
		}),
	);
});
