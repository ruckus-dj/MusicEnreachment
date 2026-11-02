import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { ManualReleaseScreen } from "./ManualReleaseScreen";
import type { CatalogArtist } from "./catalogData";
import {
	emptyManualPosition,
	type ManualReleaseDraft,
} from "./manualReleaseModel";

const blankDraft = (): ManualReleaseDraft => ({
	title: "",
	metadata: { date: "", country: "", label: "", catalogNumber: "", type: "" },
	artists: [],
	media: [],
	coverId: "",
});

function Harness({ initial = blankDraft() }: { initial?: ManualReleaseDraft }) {
	const [draft, setDraft] = useState(initial);
	return (
		<ManualReleaseScreen
			draft={draft}
			artists={[] as CatalogArtist[]}
			onDraft={setDraft}
			onCreate={() => {}}
			onReset={() => setDraft(blankDraft())}
		/>
	);
}

describe("ManualReleaseScreen", () => {
	it("keeps the release form optional and permits creation with blank fields", () => {
		render(<Harness />);
		expect(
			screen.getByText("Поля необязательны; неполный релиз допустим"),
		).toBeInTheDocument();
		for (const label of [
			"Название релиза",
			"Дата релиза",
			"Страна",
			"Лейбл",
			"Номер каталога",
			"Тип / уточнение",
		])
			expect(screen.getByLabelText(label)).toHaveValue("");
		expect(
			screen.getByRole("button", { name: "Создать демо-релиз" }),
		).toBeEnabled();
	});

	it("reorders media and positions independently", () => {
		const draft = blankDraft();
		draft.media = [
			{
				title: "Disc A",
				format: "CD",
				positions: [
					{ ...emptyManualPosition(), title: "One" },
					{ ...emptyManualPosition(), title: "Two" },
				],
			},
			{
				title: "Disc B",
				format: "LP",
				positions: [{ ...emptyManualPosition(), title: "Three" }],
			},
		];
		render(<Harness initial={draft} />);
		fireEvent.click(
			screen.getAllByRole("button", { name: "Переместить позицию ниже" })[0],
		);
		let values = screen
			.getAllByLabelText("Название в этом релизе")
			.map((input) => (input as HTMLInputElement).value);
		expect(values).toEqual(["Two", "One", "Three"]);
		fireEvent.click(
			screen.getAllByRole("button", { name: "Переместить носитель ниже" })[0],
		);
		values = screen
			.getAllByLabelText("Название в этом релизе")
			.map((input) => (input as HTMLInputElement).value);
		expect(values).toEqual(["Three", "Two", "One"]);
	});

	it("adds a pending source without selecting it automatically", () => {
		const draft = blankDraft();
		draft.media = [
			{ title: "", format: "", positions: [emptyManualPosition()] },
		];
		render(<Harness initial={draft} />);
		fireEvent.click(
			screen.getByRole("button", { name: /Выбрать вариант исходника/ }),
		);
		fireEvent.click(
			screen.getByRole("option", { name: /01 - First Light\.flac/ }),
		);
		fireEvent.click(screen.getByRole("button", { name: "Добавить к позиции" }));
		expect(
			screen.getByRole("radio", { name: /01 - First Light.flac/ }),
		).not.toBeChecked();
		expect(
			screen.getByRole("button", { name: /Выбрать вариант исходника/ }),
		).not.toHaveTextContent(/First Light/);
	});

	it("confirmed replacement clears a no-longer-available selection without fallback", () => {
		const draft = blankDraft();
		draft.media = [
			{
				title: "",
				format: "",
				positions: [
					{
						...emptyManualPosition(),
						sourceIds: ["north-f1", "north-f2"],
						selectedSourceId: "north-f1",
					},
				],
			},
		];
		render(<Harness initial={draft} />);
		fireEvent.click(
			screen.getByRole("button", { name: /Выбрать вариант исходника/ }),
		);
		fireEvent.click(
			within(screen.getByRole("listbox")).getByRole("option", {
				name: /03 - Northbound \(live\?\)\.flac/,
			}),
		);
		fireEvent.click(screen.getByRole("button", { name: "Заменить набор…" }));
		const dialog = screen.getByRole("alertdialog");
		fireEvent.click(
			within(dialog).getByRole("button", { name: "Заменить набор" }),
		);
		expect(
			screen.getByRole("radio", { name: /03 - Northbound/ }),
		).not.toBeChecked();
		expect(screen.getByRole("status")).toHaveTextContent(
			"1 назначений исходников",
		);
	});
});
