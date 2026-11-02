import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalFiles } from "./catalogData";
import { initialSourceRoots, SourcesScreen } from "./SourcesScreen";

it("refuses root removal while an operation is active", () => {
	const onRemove = vi.fn();
	render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={onRemove}
		/>,
	);
	fireEvent.click(
		screen.getByText("Демонстрация удаления источника", { exact: true }),
	);
	fireEvent.click(
		screen.getByRole("checkbox", {
			name: "Для /mnt/music выполняется операция",
		}),
	);
	fireEvent.click(
		screen.getAllByRole("button", { name: "Удалить каталог" })[1],
	);
	expect(screen.getByRole("status")).toHaveTextContent("Удаление отклонено");
	expect(onRemove).not.toHaveBeenCalled();
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("does not invent a processing mode or require a work directory for source admission", () => {
	const onRoots = vi.fn();
	render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={onRoots}
			files={originalFiles}
			removed={new Set()}
			onRemove={vi.fn()}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Имя источника" }), {
		target: { value: "New source" },
	});
	fireEvent.click(
		screen.getAllByRole("button", { name: "Добавить источник" })[1],
	);
	expect(onRoots).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("radio", { name: "Через рабочую копию" }));
	fireEvent.click(
		screen.getAllByRole("button", { name: "Добавить источник" })[1],
	);
	expect(onRoots).toHaveBeenCalledWith(
		expect.arrayContaining([
			expect.objectContaining({
				name: "New source",
				mode: "staged",
				available: null,
			}),
		]),
	);
});
