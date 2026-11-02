import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { SetupScreen } from "./SetupScreen";
import { initialDemoSettings } from "./SettingsScreen";

function StatefulSetup() {
	const [settings, setSettings] = useState(initialDemoSettings);
	return <SetupScreen settings={settings} onSettings={setSettings} />;
}

function RemountingSetup() {
	const [settings, setSettings] = useState(initialDemoSettings);
	const [mounted, setMounted] = useState(true);
	return (
		<>
			<button onClick={() => setMounted((value) => !value)}>
				Toggle setup
			</button>
			{mounted && <SetupScreen settings={settings} onSettings={setSettings} />}
		</>
	);
}

it("requires only a checked tools path; genres and work folder are not prerequisites", () => {
	render(<StatefulSetup />);
	const next = screen.getByRole("button", { name: "Продолжить" });
	expect(next).toBeDisabled();
	expect(screen.queryByText(/рабочая папка/i)).not.toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Проверить путь" }));
	expect(next).toBeEnabled();
	fireEvent.click(next);
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));

	expect(
		screen.getByRole("heading", { name: "Публикация" }),
	).toBeInTheDocument();
	expect(
		screen.getByText("Библиотека жанров · необязательно для продолжения"),
	).toBeInTheDocument();
	expect(
		screen.getByText(
			"Жанры не загружены. Выбор жанров недоступен; локальные записи можно сохранять без жанров.",
		),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	expect(screen.getByRole("heading", { name: "Итог" })).toBeInTheDocument();
});

it("rejects a relative tools path and allows choosing genres without blocking continuation", () => {
	render(<StatefulSetup />);
	fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
	fireEvent.click(screen.getByRole("button", { name: "Сервер" }));
	fireEvent.click(screen.getByRole("button", { name: "var ›" }));
	fireEvent.click(screen.getByRole("button", { name: "lib ›" }));
	fireEvent.click(screen.getByRole("button", { name: "melotrove ›" }));
	fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
	fireEvent.click(screen.getByRole("button", { name: "Проверить путь" }));
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled();

	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	fireEvent.click(
		screen.getByRole("button", { name: "Загрузить каталог в демо" }),
	);
	expect(
		screen.getByText("Каталог доступен в демонстрации"),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	expect(screen.getByRole("heading", { name: "Итог" })).toBeInTheDocument();
});

it("retains a checked tools path in parent state across setup remounts", () => {
	render(<RemountingSetup />);
	fireEvent.click(screen.getByRole("button", { name: "Проверить путь" }));
	fireEvent.click(screen.getByRole("button", { name: "Toggle setup" }));
	expect(
		screen.queryByRole("heading", { name: "Первый запуск" }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Toggle setup" }));
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled();
});
