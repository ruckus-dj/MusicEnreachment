import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { SettingsScreen, initialDemoSettings } from "./SettingsScreen";

function StatefulSettings({
	onOutputReset,
	workDirectoryBusy,
}: {
	onOutputReset?: (path: string) => void;
	workDirectoryBusy?: boolean;
} = {}) {
	const [settings, setSettings] = useState(initialDemoSettings);
	return (
		<SettingsScreen
			settings={settings}
			onSettings={setSettings}
			onOutputReset={onOutputReset}
			workDirectoryBusy={workDirectoryBusy}
		/>
	);
}

function openTab(name: string) {
	fireEvent.click(screen.getByRole("tab", { name }));
}

function chooseOutputCandidate() {
	openTab("Папки");
	const section = screen
		.getByRole("heading", { name: "Каталог публикации" })
		.closest("section");
	if (!section)
		throw new Error("publication directory section was not rendered");
	fireEvent.click(
		within(section).getByRole("button", { name: "Выбрать папку" }),
	);
	const picker = screen.getByRole("dialog", { name: "Папка на сервере" });
	fireEvent.click(
		within(picker).getByRole("button", { name: "На уровень выше" }),
	);
	fireEvent.click(screen.getByRole("button", { name: "media ›" }));
	fireEvent.click(
		within(picker).getByRole("button", { name: "Выбрать папку" }),
	);
}

it("groups settings into navigable categories and keeps the genre library in its own tab", () => {
	render(<StatefulSettings />);
	for (const name of ["Общие", "Папки", "Инструменты", "Провайдеры", "Жанры"])
		expect(screen.getByRole("tab", { name })).toBeInTheDocument();
	openTab("Жанры");
	expect(screen.getByRole("heading", { name: "Жанры" })).toBeInTheDocument();
	expect(
		screen.getByRole("searchbox", { name: "Поиск жанров" }),
	).toBeInTheDocument();
	expect(screen.getByRole("list", { name: "Результаты" })).toBeInTheDocument();
});

it("keeps a selected publication path as a candidate until destructive confirmation", () => {
	const onOutputReset = vi.fn();
	render(<StatefulSettings onOutputReset={onOutputReset} />);
	chooseOutputCandidate();
	expect(screen.getByLabelText("Текущий каталог")).toHaveValue("/srv/music");
	expect(screen.getByLabelText("Новый каталог")).toHaveValue("/srv/media");
	fireEvent.click(
		screen.getByRole("button", { name: "Сменить каталог публикации" }),
	);
	const dialog = screen.getByRole("dialog");
	fireEvent.click(
		within(dialog).getByRole("button", { name: "Подтвердить смену" }),
	);
	expect(screen.getByLabelText("Текущий каталог")).toHaveValue("/srv/media");
	expect(onOutputReset).toHaveBeenCalledWith("/srv/media");
});

it("requires an explicit release and installs it inactive in the settings workflow", () => {
	render(<StatefulSettings />);
	openTab("Инструменты");
	const tools = screen.getByRole("tabpanel", { name: "Инструменты" });
	const ffmpeg = within(tools).getByRole("region", {
		name: "FFmpeg · ffmpeg + ffprobe",
	});
	const install = within(ffmpeg).getByRole("button", { name: "Установить" });
	expect(install).toBeDisabled();
	fireEvent.click(within(ffmpeg).getByRole("button", { name: /Версия/ }));
	fireEvent.click(screen.getByRole("option", { name: "8.0.1" }));
	fireEvent.click(install);
	const dialog = screen.getByRole("dialog", { name: "Установить · ffmpeg" });
	fireEvent.click(within(dialog).getByRole("button", { name: "Установить" }));
	const installedVersion = within(ffmpeg).getByText(/8\.0\.1 · Установлена/);
	expect(installedVersion).toBeInTheDocument();
	const versionRow = installedVersion.closest("div");
	if (!versionRow) throw new Error("installed version row was not rendered");
	expect(
		within(versionRow).getByRole("button", { name: "Активировать" }),
	).toBeInTheDocument();
});

it("preserves the approved LRCLIB-on default and disables future lookup when turned off", () => {
	render(<StatefulSettings />);
	openTab("Провайдеры");
	const control = screen.getByLabelText("Использовать LRCLIB");
	expect(control).toBeChecked();
	fireEvent.click(control);
	expect(control).not.toBeChecked();
});

it("prevents changes to an occupied work directory and keeps move preflight protected", () => {
	render(<StatefulSettings workDirectoryBusy />);
	openTab("Папки");
	const work = screen
		.getByRole("heading", { name: "Рабочая папка анализа" })
		.closest("section");
	if (!work) throw new Error("work directory section was not rendered");
	expect(
		within(work).getByRole("button", { name: "Выбрать папку" }),
	).toBeDisabled();
	openTab("Инструменты");
	fireEvent.change(
		screen.getByRole("textbox", { name: "Новый каталог инструментов" }),
		{
			target: { value: "/srv/new-tools" },
		},
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить возможность переноса" }),
	);
	expect(
		screen.getByRole("dialog", { name: "Перенести каталог инструментов" }),
	).toBeInTheDocument();
});
