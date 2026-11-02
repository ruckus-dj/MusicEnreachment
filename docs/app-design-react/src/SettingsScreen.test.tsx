import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { SettingsScreen, initialDemoSettings } from "./SettingsScreen";

function StatefulSettings() {
	const [settings, setSettings] = useState(initialDemoSettings);
	return <SettingsScreen settings={settings} onSettings={setSettings} />;
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

	expect(
		screen.getByLabelText("Текущий каталог публикации · только чтение"),
	).toHaveValue("/srv/music");
	chooseOutputCandidate();
	expect(
		screen.getByLabelText("Новый путь · кандидат, ещё не применён"),
	).toHaveValue("/srv/media");
	expect(
		screen.getByLabelText("Текущий каталог публикации · только чтение"),
	).toHaveValue("/srv/music");
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
					: "Операция захвачена обработчиком",
		}),
	);
	expect(
		screen.getByRole("button", {
			name: "Подтвердить смену каталога публикации",
		}),
	).toBeDisabled();
	expect(screen.getByRole("alert")).toHaveTextContent("Смена заблокирована");
});

it("permits queued operations to proceed to demo confirmation without mutating the current output", () => {
	render(<StatefulSettings />);
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
	fireEvent.click(within(dialog).getByRole("button", { name: "Подтвердить" }));
	expect(screen.getByRole("status")).toHaveTextContent("реального сброса нет");
	expect(
		screen.getByLabelText("Текущий каталог публикации · только чтение"),
	).toHaveValue("/srv/music");
	expect(
		screen.getByLabelText("Новый путь · кандидат, ещё не применён"),
	).toHaveValue("/srv/media");
});
