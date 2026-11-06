import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "./i18n/I18nProvider";
import { ObjectMetadataForm } from "./ObjectMetadataForm";

beforeEach(() => localStorage.clear());

describe.each(["ru", "en"] as const)("ObjectMetadataForm (%s)", (locale) => {
	it("associates required errors, focuses first missing field, and preserves draft", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSave = vi.fn();
		render(
			<I18nProvider>
				<ObjectMetadataForm
					label="Metadata"
					fields={[
						{ key: "title", label: "Title", value: "Original" },
						{ key: "year", label: "Year", value: "2024" },
					]}
					required={["title", "year"]}
					onSave={onSave}
				/>
			</I18nProvider>,
		);
		fireEvent.click(
			screen.getByRole("button", { name: /edit|редактировать/i }),
		);
		const title = screen.getByRole("textbox", { name: "Title" });
		fireEvent.change(title, { target: { value: "Draft title" } });
		fireEvent.change(screen.getByRole("textbox", { name: "Year" }), {
			target: { value: " " },
		});
		fireEvent.click(screen.getByRole("button", { name: /save|сохранить/i }));
		await waitFor(() =>
			expect(screen.getByRole("textbox", { name: "Year" })).toHaveFocus(),
		);
		const year = screen.getByRole("textbox", { name: "Year" });
		expect(year).toHaveAttribute("aria-invalid", "true");
		expect(year).toHaveAccessibleDescription(/Year/);
		expect(title).not.toHaveAttribute("aria-invalid");
		expect(title).not.toHaveAttribute("aria-describedby");
		expect(title).toHaveValue("Draft title");
		expect(onSave).not.toHaveBeenCalled();
		fireEvent.change(screen.getByRole("textbox", { name: "Year" }), {
			target: { value: "2025" },
		});
		expect(screen.getByRole("textbox", { name: "Year" })).not.toHaveAttribute(
			"aria-invalid",
		);
		expect(title).toHaveValue("Draft title");
		fireEvent.change(title, { target: { value: "" } });
		fireEvent.change(year, { target: { value: "" } });
		fireEvent.click(screen.getByRole("button", { name: /save|сохранить/i }));
		await waitFor(() => expect(title).toHaveFocus());
		for (const field of [title, year]) {
			expect(field).toHaveAttribute("aria-invalid", "true");
			expect(
				document.getElementById(field.getAttribute("aria-describedby")!),
			).toBeInTheDocument();
		}
	});
});
