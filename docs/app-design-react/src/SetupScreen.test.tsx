import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { SetupScreen } from "./SetupScreen";
import { initialDemoSettings, type DemoSettings } from "./SettingsScreen";
import { musicBrainzConfigurationFingerprint } from "./providerValidation";

function StatefulSetup() {
	const [settings, setSettings] = useState(initialDemoSettings);
	return <SetupScreen settings={settings} onSettings={setSettings} />;
}

function chooseCurrentToolsPath() {
	fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
	const picker = screen.getByRole("dialog", { name: "Папка на сервере" });
	fireEvent.click(
		within(picker).getByRole("button", { name: "Выбрать папку" }),
	);
}

function installTool(packageName: string, release: string) {
	const region = screen.getByRole("region", { name: packageName });
	fireEvent.click(within(region).getByRole("button", { name: /Версия/ }));
	fireEvent.click(screen.getByRole("option", { name: release }));
	fireEvent.click(within(region).getByRole("button", { name: "Установить" }));
	const dialog = screen.getByRole("dialog", { name: "Установить и проверить" });
	fireEvent.click(
		within(dialog).getByRole("button", { name: "Установить и проверить" }),
	);
}

it("validates selected directories automatically, requires verified tools, and validates publication settings together", () => {
	render(<StatefulSetup />);
	const next = screen.getByRole("button", { name: "Продолжить" });
	expect(next).toBeDisabled();
	chooseCurrentToolsPath();
	expect(next).toBeEnabled();
	fireEvent.click(next);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	installTool("Пакет FFmpeg", "8.0.1");
	installTool("fpcalc", "1.5.1");
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	const publication = screen
		.getAllByRole("heading", { name: "Публикация" })[0]!
		.closest("section");
	if (!publication) throw new Error("Missing publication section");
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	fireEvent.click(
		within(publication).getByRole("button", { name: "Выбрать папку" }),
	);
	const picker = screen.getByRole("dialog", { name: "Папка на сервере" });
	fireEvent.click(screen.getByRole("button", { name: "srv ›" }));
	fireEvent.click(screen.getByRole("button", { name: "music ›" }));
	fireEvent.click(
		within(picker).getByRole("button", { name: "Выбрать папку" }),
	);
	fireEvent.click(screen.getByRole("button", { name: /MusicBrainz/ }));
	fireEvent.click(screen.getByRole("option", { name: "Публичный сервис" }));
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled();
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	expect(screen.getByRole("heading", { name: "Итог" })).toBeInTheDocument();
	expect(
		screen.getByText("Каталог жанров · необязательно"),
	).toBeInTheDocument();
});

it("does not let an earlier install confirmation authorize changed version or path", () => {
	function ChangeWhileConfirming() {
		const [settings, setSettings] = useState(initialDemoSettings);
		return (
			<>
				<SetupScreen settings={settings} onSettings={setSettings} />
				<button
					type="button"
					onClick={() =>
						setSettings({ ...settings, toolsPath: "/srv/other-tools" })
					}
				>
					Изменить каталог
				</button>
			</>
		);
	}
	render(<ChangeWhileConfirming />);
	chooseCurrentToolsPath();
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	const ffmpeg = screen.getByRole("region", { name: "Пакет FFmpeg" });
	fireEvent.click(within(ffmpeg).getByRole("button", { name: /Версия/ }));
	fireEvent.click(screen.getByRole("option", { name: "8.0.1" }));
	fireEvent.click(within(ffmpeg).getByRole("button", { name: "Установить" }));
	fireEvent.click(screen.getByText("Изменить каталог"));
	fireEvent.click(
		screen.getByRole("button", { name: "Установить и проверить" }),
	);
	expect(
		screen.getByText(/параметры переноса изменились/i),
	).toBeInTheDocument();
});

it("rechecks all setup requirements before applying the selected output directory", () => {
	function ReadySetup() {
		const [settings, setSettings] = useState<DemoSettings>({
			...initialDemoSettings,
			pathChecked: true,
			selectedRelease: { ffmpeg: "8.0.1", fpcalc: "1.5.1" },
			setupVerified: { ffmpeg: "8.0.1", fpcalc: "1.5.1" },
			installations: [
				{
					id: "ffmpeg-8.0.1",
					package: "ffmpeg" as const,
					release: "8.0.1",
					status: "active" as const,
					occupied: false,
				},
				{
					id: "fpcalc-1.5.1",
					package: "fpcalc" as const,
					release: "1.5.1",
					status: "active" as const,
					occupied: false,
				},
			],
			outputCandidate: "/srv/new-library",
			outputChecked: true,
			musicBrainzMode: "public" as const,
			musicBrainzVerified: true,
			musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
				mode: "public",
			}),
		});
		return (
			<>
				<SetupScreen settings={settings} onSettings={setSettings} step={3} />
				<output>{settings.currentOutput}</output>
			</>
		);
	}
	render(<ReadySetup />);
	fireEvent.click(screen.getByRole("button", { name: "Завершить настройку" }));
	expect(screen.getByRole("status")).toHaveTextContent("/srv/new-library");
});
