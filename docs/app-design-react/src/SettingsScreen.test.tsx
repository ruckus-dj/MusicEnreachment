import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "./i18n/I18nProvider";
import { initialDemoSettings, SettingsScreen } from "./SettingsScreen";

beforeEach(() => localStorage.clear());

describe.each(["ru", "en"] as const)("SettingsScreen (%s)", (locale) => {
	it("keeps cleanup off by default and saves its changed value through the same flow across tabs", () => {
		localStorage.setItem("music-design-v2-language", locale);
		let current = { ...initialDemoSettings };
		const onSettings = vi.fn((next) => {
			current = next;
			renderScreen.rerender(
				<I18nProvider>
					<SettingsScreen settings={current} onSettings={onSettings} />
				</I18nProvider>,
			);
		});
		const renderScreen = render(
			<I18nProvider>
				<SettingsScreen settings={current} onSettings={onSettings} />
			</I18nProvider>,
		);
		const section = screen.getByRole("region", {
			name: /source deletion|удаление исходников/i,
		});
		const cleanup = within(section).getByRole("checkbox");
		expect(cleanup).not.toBeChecked();
		expect(
			within(section).getByText(/filesystem permission|разрешени/i),
		).toBeInTheDocument();
		fireEvent.click(cleanup);
		expect(cleanup).toBeChecked();
		fireEvent.click(screen.getByRole("tab", { name: /folders|папки/i }));
		fireEvent.click(screen.getByRole("tab", { name: /general|общие/i }));
		expect(
			within(
				screen.getByRole("region", {
					name: /source deletion|удаление исходников/i,
				}),
			).getByRole("checkbox"),
		).toBeChecked();
		fireEvent.click(
			screen.getByRole("button", {
				name: /save settings|сохранить настройки/i,
			}),
		);
		expect(onSettings).toHaveBeenCalledWith(
			expect.objectContaining({
				sourceCleanupEnabled: true,
				runtimeSaved: true,
			}),
		);
	});
});
