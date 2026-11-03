import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { SettingsScreen, initialDemoSettings } from "./SettingsScreen";

function StatefulSettings({
	onOutputReset,
	workDirectoryBusy,
}: {
	onOutputReset?: (nextPath: string) => void;
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

function chooseOutputCandidate() {
	const output = screen
		.getByRole("heading", { name: "Каталог публикации" })
		.closest("section");
	if (!output) throw new Error("output section was not rendered");
	fireEvent.click(
		within(output).getByRole("button", { name: "Выбрать папку" }),
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

it("loads the shared genre catalog and keeps the publication path candidate-only", () => {
	render(<StatefulSettings />);
	fireEvent.click(
		screen.getByRole("button", { name: "Загрузить / обновить каталог в демо" }),
	);
	expect(
		screen.getByText("Каталог доступен в демонстрации"),
	).toBeInTheDocument();

	expect(screen.getByLabelText("Текущий каталог публикации")).toHaveValue(
		"/srv/music",
	);
	chooseOutputCandidate();
	expect(
		screen.getByLabelText("Новый путь · кандидат, ещё не применён"),
	).toHaveValue("/srv/media");
	expect(screen.getByLabelText("Текущий каталог публикации")).toHaveValue(
		"/srv/music",
	);
});

it.each([
	"running",
	"claimed",
] as const)("blocks reset when an operation is %s", (execution) => {
	render(<StatefulSettings />);
	chooseOutputCandidate();
	fireEvent.click(
		screen.getByRole("button", { name: /Нет активных операций/ }),
	);
	fireEvent.click(
		screen.getByRole("option", {
			name:
				execution === "running"
					? "Выполняется операция"
					: "Операция уже захвачена обработчиком",
		}),
	);
	expect(
		screen.getByRole("button", {
			name: "Подтвердить смену каталога публикации",
		}),
	).toBeDisabled();
	expect(screen.getByRole("alert")).toHaveTextContent("Смена заблокирована");
});

it("permits queued operations to reset output and notifies parent after confirmation", () => {
	const onOutputReset = vi.fn();
	render(<StatefulSettings onOutputReset={onOutputReset} />);
	chooseOutputCandidate();
	fireEvent.click(
		screen.getByRole("button", { name: /Нет активных операций/ }),
	);
	fireEvent.click(
		screen.getByRole("option", { name: "Есть только ожидающие операции" }),
	);
	expect(screen.getByRole("note")).toHaveTextContent(
		"Ожидающие задачи не блокируют смену",
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: "Подтвердить смену каталога публикации",
		}),
	);
	const dialog = screen.getByRole("dialog");
	fireEvent.click(
		within(dialog).getByRole("button", { name: "Подтвердить смену" }),
	);
	expect(screen.getByText(/каталог сменён/)).toBeInTheDocument();
	expect(screen.getByLabelText("Текущий каталог публикации")).toHaveValue(
		"/srv/media",
	);
	expect(
		screen.getByLabelText("Новый путь · кандидат, ещё не применён"),
	).toHaveValue("/srv/media");
	expect(onOutputReset).toHaveBeenCalledWith("/srv/media");
});

it("manual catalog refresh bypasses cooldown without installing or activating", () => {
	render(<StatefulSettings />);
	fireEvent.click(
		screen.getByRole("button", { name: "Обновить каталог вручную" }),
	);
	expect(screen.getByText(/обходом 24-часового/)).toHaveTextContent(
		"Ничего не скачано и не активировано",
	);
	expect(screen.getByText(/Последняя успешная проверка:/)).toBeInTheDocument();
});

it("requires an explicit release choice, then leaves a downloaded version inactive", () => {
	render(<StatefulSettings />);
	const tools = screen
		.getByRole("heading", { name: "Управляемые инструменты" })
		.closest("section");
	if (!tools) throw new Error("tools section was not rendered");
	const installButtons = within(tools).getAllByRole("button", {
		name: "Подготовить установку · оставить неактивной",
	});
	expect(installButtons[0]).toBeDisabled();
	const ffSelect = within(tools).getByRole("button", {
		name: /Выбрать доступную версию · ffmpeg/,
	});
	fireEvent.click(ffSelect);
	fireEvent.click(screen.getByRole("option", { name: "8.0.1 · demo" }));
	fireEvent.click(installButtons[0]);
	const preflight = screen.getByRole("dialog", {
		name: "Проверка установки ffmpeg · 8.0.1",
	});
	expect(
		within(preflight).getByText(/ffmpeg\/8\.0\.1\/bin\/ffmpeg/),
	).toBeInTheDocument();
	expect(
		within(preflight).getByText(/ffmpeg\/8\.0\.1\/bin\/ffprobe/),
	).toBeInTheDocument();
	fireEvent.click(
		within(preflight).getByRole("button", { name: "Подтвердить проверку" }),
	);
	expect(
		screen.getByText(/добавлен как verified installed; он не активирован/),
	).toBeInTheDocument();
	const activation = within(tools).getAllByRole("button", {
		name: "Активировать",
	});
	expect(activation.length).toBeGreaterThan(0);
});

it("defaults LRCLIB on and explains that disabling it preserves existing lyrics and publications", () => {
	render(<StatefulSettings />);
	const control = screen.getByLabelText("Использовать LRCLIB");
	expect(control).toBeChecked();
	expect(
		screen.getByText(
			/Сохранённые тексты и текущие публикации остаются без изменений/,
		),
	).toBeInTheDocument();
	fireEvent.click(control);
	expect(control).not.toBeChecked();
});

it("blocks work-directory changes while source work/copies occupy that path, but permits tools-root preflight", () => {
	render(<StatefulSettings workDirectoryBusy />);
	const work = screen
		.getByRole("heading", { name: "Рабочая папка анализа источников" })
		.closest("section");
	if (!work) throw new Error("work directory section was not rendered");
	expect(
		within(work).getByRole("button", { name: "Выбрать папку" }),
	).toBeDisabled();
	const tools = screen
		.getByRole("heading", { name: "Управляемые инструменты" })
		.closest("section");
	if (!tools) throw new Error("tools section was not rendered");
	const moveButton = within(tools).getByRole("button", {
		name: "Выполнить preflight переноса",
	});
	expect(moveButton).toBeEnabled();
	fireEvent.change(
		within(tools).getByRole("textbox", { name: "Новый tools root · кандидат" }),
		{ target: { value: "/srv/tools" } },
	);
	fireEvent.click(moveButton);
	expect(
		screen.getByRole("dialog", { name: "Preflight переноса tools root" }),
	).toBeInTheDocument();
});
