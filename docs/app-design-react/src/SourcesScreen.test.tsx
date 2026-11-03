import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalFiles } from "./catalogData";
import { initialSourceRoots, SourcesScreen } from "./SourcesScreen";
import {
	createSourceScan,
	initialSourceProcessingState,
	type SourceProcessingState,
} from "./sourceProcessingModel";

function renderSources({
	workDirectory = "",
	initialProcessingState = initialSourceProcessingState,
	onRemove = vi.fn(),
	onProcessingState = vi.fn(),
}: {
	workDirectory?: string;
	initialProcessingState?: SourceProcessingState;
	onRemove?: (id: string) => void;
	onProcessingState?: (state: SourceProcessingState) => void;
} = {}) {
	const view = render(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={originalFiles}
			removed={new Set()}
			onRemove={onRemove}
			workDirectory={workDirectory}
			initialProcessingState={initialProcessingState}
			onProcessingState={onProcessingState}
		/>,
	);
	return { ...view, onRemove, onProcessingState };
}

function sourceRow(name: string) {
	const row = screen
		.getAllByRole("row")
		.find((candidate) => within(candidate).queryByText(name));
	if (!row) throw new Error(`Missing source row: ${name}`);
	return row;
}

it("automatically starts fixed processing from Scan and retries only the failed step", () => {
	const onProcessingState = vi.fn();
	renderSources({ workDirectory: "/mnt/work", onProcessingState });
	const inboxRow = sourceRow("Входящие");
	fireEvent.click(
		within(inboxRow).getByRole("button", { name: "Сканировать и обработать" }),
	);

	let state = onProcessingState.mock.lastCall?.[0] as SourceProcessingState;
	expect(state.files.length).toBeGreaterThan(0);
	expect(
		state.files.every(
			(file) =>
				file.status === "failed" &&
				file.steps[0].status === "succeeded" &&
				file.steps[1].status === "failed",
		),
	).toBe(true);
	expect(state.copies).toHaveLength(state.files.length);
	expect(screen.getAllByRole("alert")).toHaveLength(state.files.length);

	const first = state.files[0];
	if (!first) throw new Error("Missing failed processing file");
	fireEvent.click(
		screen.getAllByRole("button", { name: "Повторить: Fingerprint" })[0],
	);
	state = onProcessingState.mock.lastCall?.[0] as SourceProcessingState;
	expect(state.files.find((file) => file.id === first.id)?.status).toBe(
		"completed",
	);
	expect(state.files.find((file) => file.id === first.id)?.steps).toMatchObject(
		[
			{ id: "audio_inspection", status: "succeeded" },
			{ id: "fingerprint", status: "succeeded" },
		],
	);
	expect(state.files.slice(1).every((file) => file.status === "failed")).toBe(
		true,
	);
	expect(screen.queryByText(/демо|демонстрац/i)).not.toBeInTheDocument();
});

it("keeps staged processing intent and offers Settings when no work directory is configured", () => {
	const onProcessingState = vi.fn();
	renderSources({ onProcessingState });
	const inboxRow = sourceRow("Входящие");
	fireEvent.click(
		within(inboxRow).getByRole("button", { name: "Сканировать и обработать" }),
	);

	const state = onProcessingState.mock.lastCall?.[0] as SourceProcessingState;
	expect(state.files.length).toBeGreaterThan(0);
	expect(
		state.files.every(
			(file) =>
				file.status === "queued" &&
				file.blockedReason === "work_directory_required",
		),
	).toBe(true);
	const rowStatus = screen
		.getAllByRole("status")
		.find((status) =>
			within(status).queryByRole("link", { name: "Настроить в Настройках" }),
		);
	expect(rowStatus).toBeVisible();
	expect(rowStatus).toHaveTextContent(
		"Файлы найдены. Для обработки через копию выберите рабочую папку в Настройках.",
	);
	expect(
		within(rowStatus!).getByRole("link", { name: "Настроить в Настройках" }),
	).toHaveAttribute("href", "#settings");
});

it("places removal in the exact source row, confirms its inventory, and removes only that source", () => {
	const onProcessingState = vi.fn();
	const { onRemove } = renderSources({
		onProcessingState,
	});
	const libraryRow = sourceRow("Музыкальная библиотека");
	fireEvent.click(
		within(libraryRow).getByRole("button", {
			name: "Удалить источник Музыкальная библиотека",
		}),
	);
	expect(screen.getByRole("dialog")).toHaveTextContent("/mnt/music");
	expect(screen.getByRole("dialog")).toHaveTextContent(
		"Записей о файлах в инвентаре: 2",
	);
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
	expect(onRemove).toHaveBeenCalledWith("root-library");
	const remaining = onProcessingState.mock
		.lastCall?.[0] as SourceProcessingState;
	expect(remaining.files.every((file) => file.rootId !== "root-library")).toBe(
		true,
	);
});

it("blocks removal for active root work and revalidates changed inventory at confirmation", () => {
	const active = createSourceScan(
		initialSourceProcessingState,
		"root-library",
		"in_place",
		originalFiles,
	);
	const onRemove = vi.fn();
	const first = renderSources({ initialProcessingState: active, onRemove });
	const libraryRow = sourceRow("Музыкальная библиотека");
	const removeButton = within(libraryRow).getByRole("button", {
		name: "Удалить источник Музыкальная библиотека",
	});
	fireEvent.click(removeButton);
	expect(screen.getByRole("status")).toHaveTextContent(
		"Удаление заблокировано: для этого источника есть записи в очереди или выполняется обработка. Дождитесь завершения и повторите попытку.",
	);
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

	const onProcessingState = vi.fn();
	first.unmount();
	const second = renderSources({ onRemove, onProcessingState });
	const secondRow = sourceRow("Музыкальная библиотека");
	fireEvent.click(
		within(secondRow).getByRole("button", {
			name: "Удалить источник Музыкальная библиотека",
		}),
	);
	const changedFiles = structuredClone(originalFiles);
	const changedLocation = Object.values(changedFiles)
		.flatMap((file) => file.locations ?? [])
		.find((location) => location.rootId === "root-library");
	if (!changedLocation) throw new Error("Missing library inventory fixture");
	changedLocation.id = `${changedLocation.id}-changed`;
	second.rerender(
		<SourcesScreen
			roots={initialSourceRoots}
			onRoots={vi.fn()}
			files={changedFiles}
			removed={new Set()}
			onRemove={onRemove}
			initialProcessingState={initialSourceProcessingState}
			onProcessingState={onProcessingState}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(screen.getByRole("status")).toHaveTextContent(
		"инвентарь источника изменился",
	);
	expect(onRemove).not.toHaveBeenCalled();
});
