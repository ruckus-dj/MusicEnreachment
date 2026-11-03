import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ObjectMetadataForm } from "./ObjectMetadataForm";
import { I18nProvider, useI18n } from "./i18n/I18nProvider";

const initialFields = [
	{ key: "title", label: "Title", value: "Saved title" },
	{ key: "artist", label: "Artist", value: "Saved artist" },
];
const provider = { key: "provider", label: "Provider", value: "MusicBrainz" };

function Harness({
	onSave = vi.fn(),
}: {
	onSave?: (values: Record<string, string>) => void;
}) {
	const [fields, setFields] = useState(initialFields);
	return (
		<ObjectMetadataForm
			label="Object metadata"
			fields={fields}
			readOnlyFields={[provider]}
			required={["title"]}
			onSave={(values) => {
				onSave(values);
				setFields((current) =>
					current.map((field) => ({ ...field, value: values[field.key] })),
				);
			}}
		/>
	);
}

function LocalePreferenceControls() {
	const { setPreference } = useI18n();
	return (
		<div>
			<button type="button" onClick={() => setPreference("en")}>
				Use English
			</button>
			<button type="button" onClick={() => setPreference("ru")}>
				Use Russian
			</button>
		</div>
	);
}

describe("ObjectMetadataForm", () => {
	it("edits and saves using the same DOM controls, preserving fixed action dimensions", () => {
		const onSave = vi.fn();
		function UpdatingHarness() {
			const [fields, setFields] = useState(initialFields);
			return (
				<ObjectMetadataForm
					label="Object metadata"
					fields={fields}
					readOnlyFields={[provider]}
					required={["title"]}
					onSave={(values) => {
						onSave(values);
						setFields((current) =>
							current.map((field) => ({ ...field, value: values[field.key] })),
						);
					}}
				/>
			);
		}
		render(<UpdatingHarness />);
		const title = screen.getByRole("textbox", { name: "Title" });
		const artist = screen.getByRole("textbox", { name: "Artist" });
		const providerInput = screen.getByRole("textbox", { name: "Provider" });
		const edit = screen.getByRole("button", { name: "Редактировать" });
		const cancel = screen.getByRole("button", {
			name: "Отмена",
			hidden: true,
		});
		const fieldGrid = title.closest("label")?.parentElement;
		expect(fieldGrid).toHaveClass(
			"grid-cols-1",
			"sm:grid-cols-2",
			"lg:grid-cols-4",
		);
		expect(edit.className).toContain("w-[132px]");
		expect(cancel.className).toContain("w-[132px]");
		expect(cancel.className).toContain("invisible");
		fireEvent.click(edit);
		expect(screen.getByRole("textbox", { name: "Title" })).toBe(title);
		expect(screen.getByRole("textbox", { name: "Artist" })).toBe(artist);
		expect(screen.getByRole("button", { name: "Отмена" })).toBe(cancel);
		expect(cancel.className).toContain("visible");
		expect(title).not.toHaveProperty("readOnly", true);
		expect(providerInput).toHaveProperty("readOnly", true);
		fireEvent.change(title, { target: { value: "New title" } });
		fireEvent.change(artist, { target: { value: "New artist" } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledExactlyOnceWith({
			title: "New title",
			artist: "New artist",
		});
		expect(screen.getByRole("textbox", { name: "Title" })).toBe(title);
		expect(title).toHaveProperty("readOnly", true);
		expect(title).toHaveValue("New title");
		expect(artist).toHaveValue("New artist");
		expect(providerInput).toHaveValue("MusicBrainz");
		expect(cancel.className).toContain("invisible");
	});

	it("cancels and handles Escape by restoring saved values and focusing after the animation frame", () => {
		const raf = vi
			.spyOn(window, "requestAnimationFrame")
			.mockImplementation((callback) => {
				callback(0);
				return 1;
			});
		render(<Harness />);
		const title = screen.getByRole("textbox", { name: "Title" });
		const edit = screen.getByRole("button", { name: "Редактировать" });
		fireEvent.click(edit);
		fireEvent.change(title, { target: { value: "Discard me" } });
		fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
		expect(title).toHaveValue("Saved title");
		expect(title).toHaveProperty("readOnly", true);
		expect(edit).toHaveFocus();
		expect(raf).toHaveBeenCalledOnce();

		fireEvent.click(edit);
		fireEvent.change(title, { target: { value: "Also discard" } });
		fireEvent.keyDown(screen.getByRole("region", { name: "Object metadata" }), {
			key: "Escape",
		});
		expect(title).toHaveValue("Saved title");
		expect(edit).toHaveFocus();
		expect(raf).toHaveBeenCalledTimes(2);
		raf.mockRestore();
	});

	it("keeps an invalid required-field draft and clears the error when corrected", () => {
		const onSave = vi.fn();
		render(<Harness onSave={onSave} />);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		const title = screen.getByRole("textbox", { name: "Title" });
		fireEvent.change(title, { target: { value: "   " } });
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(screen.getByText("Заполните поле «Title».")).toBeInTheDocument();
		expect(title).toHaveValue("   ");
		expect(title).not.toHaveProperty("readOnly", true);
		expect(onSave).not.toHaveBeenCalled();
		fireEvent.change(title, { target: { value: "Corrected" } });
		expect(
			screen.queryByText("Заполните поле «Title». "),
		).not.toBeInTheDocument();
		expect(
			screen.queryByText("Заполните поле «Title»."),
		).not.toBeInTheDocument();
	});

	it("retains the shared edit draft when the locale changes", () => {
		const onSave = vi.fn();
		render(
			<I18nProvider>
				<LocalePreferenceControls />
				<ObjectMetadataForm
					label="Object metadata"
					fields={initialFields}
					required={["title"]}
					onSave={onSave}
				/>
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Use English" }));
		fireEvent.click(screen.getByRole("button", { name: "Edit" }));
		const title = screen.getByRole("textbox", { name: "Title" });
		fireEvent.change(title, { target: { value: "Unsaved draft" } });

		fireEvent.click(screen.getByRole("button", { name: "Use Russian" }));
		expect(title).toHaveValue("Unsaved draft");
		expect(
			screen.getByRole("button", { name: "Сохранить" }),
		).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledExactlyOnceWith({
			title: "Unsaved draft",
			artist: "Saved artist",
		});
	});

	it("keeps the genre control fixed-height and only submits picker changes", () => {
		const onSave = vi.fn();
		const genreField = {
			value: ["legacy-unavailable-id"],
			displayValue: "legacy genre",
			loaded: true,
		};
		const { rerender } = render(
			<ObjectMetadataForm
				label="Artist metadata"
				fields={initialFields}
				genreField={genreField}
				onSave={onSave}
			/>,
		);
		const genreControl = screen.getByRole("button", { name: "Жанры" });
		expect(genreControl).toHaveClass("h-8");
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		expect(screen.getByRole("button", { name: "Жанры" })).toBe(genreControl);
		expect(genreControl).toHaveClass("h-8");
		fireEvent.click(genreControl);
		fireEvent.change(screen.getByRole("searchbox", { name: "Поиск жанров" }), {
			target: { value: "rock" },
		});
		fireEvent.change(screen.getByRole("searchbox", { name: "Поиск жанров" }), {
			target: { value: "rock" },
		});
		fireEvent.click(screen.getByRole("option", { name: "Rock" }));
		fireEvent.click(screen.getByRole("button", { name: "Готово" }));
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledWith(
			expect.objectContaining({ title: "Saved title" }),
			["legacy-unavailable-id", "0e3fc579-2d24-4f20-9dae-736e1ec78798"],
		);

		onSave.mockClear();
		rerender(
			<ObjectMetadataForm
				label="Artist metadata"
				fields={initialFields}
				genreField={genreField}
				onSave={onSave}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
		fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
		expect(onSave).toHaveBeenCalledExactlyOnceWith(
			expect.objectContaining({ title: "Saved title" }),
		);
	});
});
