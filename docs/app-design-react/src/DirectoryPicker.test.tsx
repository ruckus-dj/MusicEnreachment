import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { DirectoryPicker } from "./DirectoryPicker";
import { I18nProvider } from "./i18n/I18nProvider";
import { directoryMessages } from "./i18n/namespaces/directories";

afterEach(() => {
	cleanup();
	localStorage.clear();
});

it.each([
	"en",
	"ru",
] as const)("localizes directory controls and associated errors in %s", (locale) => {
	localStorage.setItem("music-design-v2-language", locale);
	const copy = directoryMessages[locale];
	const onChoose = vi.fn();
	render(
		<I18nProvider>
			<DirectoryPicker initialPath="/" onChoose={onChoose} onClose={vi.fn()} />
		</I18nProvider>,
	);
	expect(screen.getByRole("heading", { name: copy.title })).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: copy.editPath }));
	const field = screen.getByRole("textbox", { name: copy.absolutePath });
	fireEvent.change(field, { target: { value: "/not-in-the-demo-tree" } });
	fireEvent.click(screen.getByRole("button", { name: copy.go }));
	expect(field).toHaveAttribute("aria-invalid", "true");
	expect(field).toHaveAccessibleDescription(
		`${copy.notFound} ${copy.invalidPath}`,
	);
	expect(field).toHaveValue("/not-in-the-demo-tree");
	expect(screen.getByRole("button", { name: copy.choose })).toBeDisabled();
	expect(onChoose).not.toHaveBeenCalled();
});
