import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IncomingScreen } from "./IncomingScreen";
import { createIncomingState, incomingFixtures } from "./incomingModel";
import { I18nProvider } from "./i18n";
import { initialSourceRoots } from "./SourcesScreen";

function renderOverview(state = createIncomingState()) {
	return render(
		<I18nProvider>
			<IncomingScreen
				files={structuredClone(incomingFixtures.files)}
				roots={initialSourceRoots}
				state={state}
			/>
		</I18nProvider>,
	);
}

beforeEach(() => {
	window.localStorage.setItem("music-design-v2-language", "ru");
});

afterEach(() => {
	vi.unstubAllGlobals();
});

it("renders one four-column overview with source paths and link navigation", () => {
	renderOverview();

	expect(screen.getAllByRole("table")).toHaveLength(1);
	expect(
		screen.getAllByRole("columnheader").map((header) => header.textContent),
	).toEqual([
		"Группа",
		"Уникальные файлы / пути",
		"Готово / всего",
		"Состояние оценки",
	]);
	expect(
		screen.getByText("Группы: 3 · очередь источников"),
	).toBeInTheDocument();

	const north = screen.getByRole("link", {
		name: "Открыть группу «Northern lines»",
	});
	expect(north).toHaveAttribute("href", "#match/north");
	const row = north.closest("tr");
	if (!row) throw new Error("Missing north overview row");
	expect(within(row).getByText("Mira Vale")).toBeInTheDocument();
	expect(within(row).getByText("9 / 10")).toBeInTheDocument();
	expect(within(row).getByText("9 / 9")).toBeInTheDocument();
	expect(
		within(row).getByText("Неполный релиз; одна спорная композиция"),
	).toBeInTheDocument();
	expect(within(row).getByText("Пути файлов")).toBeInTheDocument();
	expect(
		within(row).getByText("/mnt/inbox / north/01 - First Light.flac"),
	).toBeInTheDocument();
	expect(
		within(row).getByText("/mnt/backup / Northern Lines/01 - First Light.flac"),
	).toBeInTheDocument();
});

it("keeps the empty state inside the same four-column table", () => {
	renderOverview({ ...createIncomingState(), groups: [] });

	expect(screen.getAllByRole("table")).toHaveLength(1);
	expect(screen.getAllByRole("columnheader")).toHaveLength(4);
	expect(screen.getByText("Входящих групп пока нет.")).toBeInTheDocument();
});

it.each([
	true,
	false,
])("renders one four-column table at both reported breakpoints (matches: %s)", (matches) => {
	vi.stubGlobal(
		"matchMedia",
		vi.fn().mockReturnValue({
			matches,
			addEventListener: vi.fn(),
			removeEventListener: vi.fn(),
		}),
	);
	renderOverview();

	expect(screen.getAllByRole("table")).toHaveLength(1);
	expect(screen.getAllByRole("columnheader")).toHaveLength(4);
	expect(screen.getAllByRole("row")).toHaveLength(4);
});
