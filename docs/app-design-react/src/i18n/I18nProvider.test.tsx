import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider, useI18n } from "./I18nProvider";

function Probe() {
	const { locale, preference, persistenceError, setPreference, t } = useI18n();
	return (
		<div>
			<span data-testid="locale">{locale}</span>
			<span data-testid="preference">{preference}</span>
			<span data-testid="message">{t("common.language")}</span>
			<span data-testid="error">{persistenceError}</span>
			<button type="button" onClick={() => setPreference("ru")}>
				Choose Russian
			</button>
			<button type="button" onClick={() => setPreference("system")}>
				Choose system
			</button>
			<input aria-label="Draft" defaultValue="unsaved draft" />
		</div>
	);
}

function setLanguages(languages: string[]) {
	Object.defineProperty(navigator, "languages", {
		configurable: true,
		value: languages,
	});
}

describe("I18nProvider", () => {
	beforeEach(() => {
		localStorage.clear();
		setLanguages(["en-US"]);
		document.documentElement.lang = "";
		vi.restoreAllMocks();
	});

	it("defaults to system preference and updates the document locale", () => {
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);

		expect(screen.getByTestId("preference")).toHaveTextContent("system");
		expect(screen.getByTestId("locale")).toHaveTextContent("en");
		expect(screen.getByTestId("message")).toHaveTextContent("Language");
		expect(document.documentElement.lang).toBe("en");
	});

	it("tracks languagechange only while the preference is system and preserves child draft state", () => {
		setLanguages(["fr-FR", "ru-RU"]);
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);
		const draft = screen.getByRole("textbox", { name: "Draft" });
		fireEvent.change(draft, { target: { value: "edited draft" } });
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");

		setLanguages(["de-DE", "en-GB"]);
		fireEvent(window, new Event("languagechange"));
		expect(screen.getByTestId("locale")).toHaveTextContent("en");
		expect(draft).toHaveValue("edited draft");
		expect(document.documentElement.lang).toBe("en");

		fireEvent.click(screen.getByRole("button", { name: "Choose Russian" }));
		setLanguages(["en-US"]);
		fireEvent(window, new Event("languagechange"));
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");
	});

	it("refreshes system languages immediately when returning from an explicit preference", () => {
		setLanguages(["ru-RU"]);
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Choose Russian" }));

		setLanguages(["en-GB"]);
		fireEvent(window, new Event("languagechange"));
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");

		fireEvent.click(screen.getByRole("button", { name: "Choose system" }));
		expect(screen.getByTestId("locale")).toHaveTextContent("en");
	});

	it("persists explicit preferences", () => {
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);

		fireEvent.click(screen.getByRole("button", { name: "Choose Russian" }));
		expect(localStorage.getItem("music-design-v2-language")).toBe("ru");
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");
	});

	it("restores a previously selected preference", () => {
		localStorage.setItem("music-design-v2-language", "ru");
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);

		expect(screen.getByTestId("preference")).toHaveTextContent("ru");
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");
	});

	it("keeps the preference usable and exposes a warning when storage is unavailable", () => {
		vi.spyOn(localStorage, "getItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		vi.spyOn(localStorage, "setItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		render(
			<I18nProvider>
				<Probe />
			</I18nProvider>,
		);

		expect(screen.getByTestId("error")).toHaveTextContent("could not be read");
		fireEvent.click(screen.getByRole("button", { name: "Choose Russian" }));
		expect(screen.getByTestId("locale")).toHaveTextContent("ru");
		expect(screen.getByTestId("error")).toHaveTextContent(
			"не удалось сохранить",
		);
	});
});
