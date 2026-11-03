import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalFiles } from "./catalogData";
import { initialSourceRoots, SourcesScreen } from "./SourcesScreen";
import {
	createSourceScan,
	initialSourceProcessingState,
	processSourceQueue,
	type SourceProcessingState,
} from "./sourceProcessingModel";

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

it("uses the registered root identity and inventory count for removal confirmation", () => {
	render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={vi.fn()}
		/>,
	);
	fireEvent.click(
		screen.getByText("Демонстрация удаления источника", { exact: true }),
	);
	const libraryRow = screen.getByText("root-library").closest("tr");
	expect(libraryRow).toHaveTextContent("/mnt/music");
	expect(libraryRow).toHaveTextContent("2");
	if (!libraryRow) throw new Error("Missing library root row");
	fireEvent.click(
		within(libraryRow).getByRole("button", { name: "Удалить каталог" }),
	);
	expect(screen.getByRole("dialog")).toHaveTextContent("/mnt/music");
	expect(screen.getByRole("dialog")).toHaveTextContent(
		"Записей о файлах в инвентаре: 2",
	);
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
				id: expect.any(String),
				mode: "staged",
				available: null,
			}),
		]),
	);
});

it("scan queues without copies; explicit processing creates staged failure and copy", () => {
	const onProcessingState = vi.fn();
	render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={vi.fn()}
			workDirectory="/mnt/work"
			onProcessingState={onProcessingState}
		/>,
	);

	fireEvent.click(
		screen.getAllByRole("button", { name: "Сканировать · демо" })[0],
	);
	let state: SourceProcessingState = onProcessingState.mock.lastCall?.[0];
	expect(state.files.length).toBeGreaterThan(0);
	expect(
		state.files.every(
			(file) =>
				file.status === "queued" &&
				file.steps.every((step) => step.status === "queued"),
		),
	).toBe(true);
	expect(state.copies).toEqual([]);

	fireEvent.click(
		screen.getAllByRole("button", { name: "Обработать найденные · демо" })[0],
	);
	state = onProcessingState.mock.lastCall?.[0];
	expect(
		state.files.every(
			(file) =>
				file.status === "failed" &&
				file.steps[0].status === "succeeded" &&
				file.steps[1].status === "failed",
		),
	).toBe(true);
	expect(state.copies).toHaveLength(state.files.length);
	expect(state.copies.every((copy) => copy.status === "retained")).toBe(true);
	expect(screen.getAllByText(/Защищена: обработка не завершена/).length).toBe(
		state.copies.length,
	);
});

it("root removal clears only that root's processing state and rechecks active work at confirmation", () => {
	const inbox = processSourceQueue(
		createSourceScan(
			initialSourceProcessingState,
			"root-inbox",
			"staged",
			originalFiles,
		),
		"root-inbox",
	);
	const initial = createSourceScan(
		inbox,
		"root-library",
		"in_place",
		originalFiles,
	);
	const onProcessingState = vi.fn();
	const onRemove = vi.fn();
	const firstRender = render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={onRemove}
			initialProcessingState={initial}
			onProcessingState={onProcessingState}
		/>,
	);
	fireEvent.click(
		screen.getByText("Демонстрация удаления источника", { exact: true }),
	);
	const inboxRow = screen.getByText("root-inbox").closest("tr");
	if (!inboxRow) throw new Error("Missing inbox root row");
	fireEvent.click(
		within(inboxRow).getByRole("button", { name: "Удалить каталог" }),
	);
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onRemove).toHaveBeenCalledWith("root-inbox");
	const remaining: SourceProcessingState = onProcessingState.mock.lastCall?.[0];
	expect(remaining.files.length).toBeGreaterThan(0);
	expect(remaining.files.every((file) => file.rootId === "root-library")).toBe(
		true,
	);
	expect(remaining.copies).toEqual([]);

	const active = createSourceScan(
		initialSourceProcessingState,
		"root-inbox",
		"staged",
		originalFiles,
	);
	const guardedRemove = vi.fn();
	firstRender.unmount();
	const { unmount } = render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={guardedRemove}
			workDirectory="/mnt/work"
			initialProcessingState={active}
		/>,
	);
	fireEvent.click(
		screen.getByText("Демонстрация удаления источника", { exact: true }),
	);
	const activeRow = screen.getByText("root-inbox").closest("tr");
	if (!activeRow) throw new Error("Missing active root row");
	fireEvent.click(
		within(activeRow).getByRole("button", { name: "Удалить каталог" }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("Удаление отклонено");
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(guardedRemove).not.toHaveBeenCalled();
	unmount();
});
