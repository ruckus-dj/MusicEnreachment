import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { MetadataEditor } from "./MetadataEditor";
import type { MetadataValues } from "./projection";

it("requires a new contributor name without turning a removed draft into a tag clear", async () => {
	localStorage.setItem("music-design-v2-language", "en");
	const onSave = vi.fn<(values: MetadataValues) => void>();
	render(
		<I18nProvider>
			<MetadataEditor
				label="Recording metadata"
				scope="recording"
				values={{ title: ["Original"] }}
				onSave={onSave}
			/>
		</I18nProvider>,
	);

	fireEvent.click(screen.getByRole("button", { name: "Edit" }));
	const roles = screen
		.getByText(/^Contributors and roles ·/)
		.closest("details");
	if (!roles) throw new Error("Missing contributor disclosure");
	roles.open = true;
	fireEvent.change(screen.getByRole("combobox", { name: "Add role" }), {
		target: { value: "composer" },
	});
	const name = screen.getByRole("textbox", { name: "Contributor name" });
	await waitFor(() => expect(name).toHaveFocus());
	expect(name).toBeRequired();
	expect(name).toHaveAccessibleDescription("Contributor name is required.");
	fireEvent.change(
		screen.getByRole("textbox", { name: "Contributor MusicBrainz ID" }),
		{ target: { value: "f59790db-7f15-47cb-9e82-316807fc3cf5" } },
	);
	const title = screen.getByRole("textbox", { name: "Title" });
	fireEvent.change(title, { target: { value: "Updated" } });
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	expect(onSave).not.toHaveBeenCalled();

	fireEvent.click(screen.getByRole("button", { name: "Remove Composer 1" }));
	fireEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(onSave).toHaveBeenCalledOnce());
	expect(onSave.mock.calls[0]?.[0]).toEqual({ title: ["Updated"] });
});
