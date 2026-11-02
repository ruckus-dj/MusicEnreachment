import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ComponentsScreen } from "./ComponentsScreen";
import { originalFiles } from "./catalogData";

it("shows component states, the technical comparison and explicit file selection", () => {
	const onSelectFile = vi.fn();
	render(<ComponentsScreen onSelectFile={onSelectFile} />);
	expect(
		screen.getByRole("heading", { name: "Компоненты · плотный интерфейс" }),
	).toBeInTheDocument();
	expect(screen.getByText("Высокая уверенность")).toHaveClass(
		"bg-[var(--success-soft)]",
		"text-[var(--success)]",
	);
	expect(screen.getByRole("grid")).toBeInTheDocument();
	expect(
		screen.getByRole("tablist", { name: "Примеры компонентов" }),
	).toBeInTheDocument();
	expect(
		screen.getByRole("textbox", { name: "Поиск в библиотеке" }),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("tab", { name: "Выбор" }));
	expect(
		screen.getByRole("checkbox", { name: "Показывать только различия" }),
	).toBeInTheDocument();
	const fileChip = screen.getByRole("button", { name: /Northbound/ });
	expect(fileChip.parentElement).toHaveAttribute("draggable", "true");
	fireEvent.click(fileChip);
	expect(fileChip).toHaveAttribute("aria-pressed", "true");
	expect(onSelectFile).toHaveBeenCalledWith("north-f3");
	expect(
		screen.getByText(/Политика matching-rapidfuzz-v1 утверждена/),
	).toBeInTheDocument();
});

it("rounds displayed durations to the nearest second", () => {
	const files = {
		...originalFiles,
		"duration-test": {
			...originalFiles["north-f3"],
			id: "duration-test",
			duration: 119.6,
		},
	};
	render(<ComponentsScreen files={files} fileId="duration-test" />);
	expect(screen.getByRole("button", { name: /2:00/ })).toBeInTheDocument();
});
