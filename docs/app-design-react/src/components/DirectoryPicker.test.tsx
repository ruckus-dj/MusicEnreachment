import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n/I18nProvider";
import { DirectoryPicker } from "./DirectoryPicker";
import { directoryPickerMessages } from "./DirectoryPickerMessages";

afterEach(() => {
	cleanup();
	localStorage.clear();
});

it.each([
	"en",
	"ru",
] as const)("localizes directory controls and associated errors in %s", async (locale) => {
	localStorage.setItem("music-design-v2-language", locale);
	const copy = directoryPickerMessages[locale];
	const onChoose = vi.fn();
	render(
		<I18nProvider>
			<DirectoryPicker initialPath="/" onChoose={onChoose} onClose={vi.fn()} />
		</I18nProvider>,
	);
	expect(screen.getByRole("heading", { name: copy.title })).toBeInTheDocument();
	const picker = screen.getByRole("button", { name: `${copy.editPath}: /` });
	fireEvent.click(picker);
	const field = screen.getByRole("textbox", { name: copy.pathLabel });
	fireEvent.change(field, { target: { value: "/not-in-the-demo-tree" } });
	fireEvent.keyDown(field, { key: "Enter" });
	expect(await screen.findByRole("alert")).toHaveTextContent(copy.notFound);
	expect(field).toHaveAttribute("aria-invalid", "true");
	expect(field).toHaveAccessibleDescription(copy.notFound);
	expect(field).toHaveValue("/not-in-the-demo-tree");
	expect(screen.getByRole("button", { name: copy.choose })).toBeDisabled();
	expect(onChoose).not.toHaveBeenCalled();
});
