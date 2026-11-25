import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../../app/i18n/host";
import { initialDemoSettings } from "../settings/SettingsScreen";
import { SetupScreen } from "./SetupScreen";

const conflict = "/var/lib/melotrove/tools/ffmpeg/8.0.1/ffmpeg";

function setup(selectedRelease: string, onSettings = vi.fn()) {
	render(
		<I18nProvider>
			<SetupScreen
				settings={{
					...initialDemoSettings,
					selectedRelease: { ffmpeg: selectedRelease },
				}}
				onSettings={onSettings}
				step={1}
			/>
		</I18nProvider>,
	);
	return onSettings;
}

beforeEach(() => {
	localStorage.setItem("music-design-v2-language", "en");
	window.location.hash = "";
});
afterEach(() => vi.restoreAllMocks());

it("starts verification directly when the demo preflight is clean", () => {
	const onSettings = setup("8.0");
	fireEvent.click(screen.getByRole("button", { name: "Install FFmpeg" }));
	expect(screen.queryByRole("dialog")).toBeNull();
	expect(onSettings).toHaveBeenCalledExactlyOnceWith(
		expect.objectContaining({
			installations: expect.arrayContaining([
				expect.objectContaining({
					package: "ffmpeg",
					release: "8.0",
					status: "active",
				}),
			]),
			setupVerified: expect.objectContaining({ ffmpeg: "8.0" }),
		}),
	);
});

it("shows exact preflight conflicts and confirms or cancels the retained plan", async () => {
	const onSettings = setup("8.0.1");
	fireEvent.click(screen.getByRole("button", { name: "Install FFmpeg" }));
	const dialog = await screen.findByRole("dialog", {
		name: "Overwrite only these files:",
	});
	expect(within(dialog).getByText(conflict)).toBeVisible();
	expect(onSettings).not.toHaveBeenCalled();
	fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
	expect(screen.queryByRole("dialog")).toBeNull();
	expect(onSettings).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Install FFmpeg" }));
	fireEvent.click(
		within(
			await screen.findByRole("dialog", {
				name: "Overwrite only these files:",
			}),
		).getByRole("button", { name: "Confirm overwrite" }),
	);
	expect(onSettings).toHaveBeenCalledExactlyOnceWith(
		expect.objectContaining({
			setupVerified: expect.objectContaining({ ffmpeg: "8.0.1" }),
			unmanagedToolFiles: [],
		}),
	);
});
