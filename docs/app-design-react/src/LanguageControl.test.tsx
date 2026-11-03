import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { LanguageControl } from "./LanguageControl";
import { I18nProvider } from "./i18n";
import { DesignShell } from "./DesignShell";

afterEach(() => {
	localStorage.removeItem("music-design-v2-language");
	vi.restoreAllMocks();
});

it("switches navigation and document language without losing an edited child draft", () => {
	localStorage.setItem("music-design-v2-language", "ru");
	function Draft() {
		const [value, setValue] = useState("");
		return (
			<input
				aria-label="Musical title"
				value={value}
				onChange={(event) => setValue(event.target.value)}
			/>
		);
	}
	render(
		<I18nProvider>
			<DesignShell>
				<Draft />
			</DesignShell>
		</I18nProvider>,
	);
	fireEvent.change(screen.getByRole("textbox", { name: "Musical title" }), {
		target: { value: "Моя музыка" },
	});
	fireEvent.click(screen.getByRole("button", { name: /Язык интерфейса/ }));
	fireEvent.click(screen.getByRole("option", { name: "English" }));
	expect(
		screen.getByRole("navigation", { name: "Main navigation" }),
	).toBeInTheDocument();
	expect(document.documentElement.lang).toBe("en");
	expect(screen.getByRole("textbox", { name: "Musical title" })).toHaveValue(
		"Моя музыка",
	);
	expect(localStorage.getItem("music-design-v2-language")).toBe("en");
});

it("shows language choices with their own readable language names", () => {
	render(<LanguageControl />);
	fireEvent.click(screen.getByRole("button", { name: /Язык интерфейса/ }));
	expect(
		screen.getByRole("option", { name: "Как в системе" }),
	).toBeInTheDocument();
	expect(screen.getByRole("option", { name: "English" })).toBeInTheDocument();
	expect(screen.getByRole("option", { name: "Русский" })).toBeInTheDocument();
});
