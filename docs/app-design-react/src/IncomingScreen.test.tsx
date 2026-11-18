import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IncomingScreen } from "./IncomingScreen";
import { I18nProvider } from "./i18n";
import { createIncomingState, incomingFixtures } from "./incomingModel";
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
	window.history.replaceState(null, "", "#review");
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
	expect(
		within(row).getByRole("button", { name: "Northern lines" }),
	).toHaveAttribute("aria-expanded", "false");
});

it("restores the expanded group from a shared link", () => {
	window.history.replaceState(null, "", "#review?group=north");
	renderOverview();
	expect(
		screen.getByRole("button", { name: "Northern lines" }),
	).toHaveAttribute("aria-expanded", "true");
	expect(screen.getByText("First Light")).toBeInTheDocument();
	expect(
		screen.getByText("/mnt/inbox / north/01 - First Light.flac"),
	).toBeInTheDocument();
	expect(
		screen.getByText("/mnt/backup / Northern Lines/01 - First Light.flac"),
	).toBeInTheDocument();
});

it("orders expanded source files by tag position rather than filename", () => {
	const files = structuredClone(incomingFixtures.files);
	files["hours-f1"].tags.TRACKNUMBER = ["9"];
	window.history.replaceState(null, "", "#review?group=hours");
	render(
		<I18nProvider>
			<IncomingScreen
				files={files}
				roots={initialSourceRoots}
				state={createIncomingState()}
			/>
		</I18nProvider>,
	);

	const groupRow = screen
		.getByRole("button", { name: "Small Hours" })
		.closest("tr");
	const details = groupRow?.nextElementSibling;
	if (!(details instanceof HTMLElement))
		throw new Error("Missing expanded hours details");
	const members = within(details).getAllByRole("list")[0];
	const items = within(members).getAllByRole("listitem");

	expect(items.map((item) => item.firstElementChild?.textContent)).toEqual([
		"2",
		"3",
		"4",
		"5",
		"6",
		"7",
		"8",
		"9",
	]);
	expect(items[0]).toHaveTextContent("After Midnight");
	expect(items[items.length - 1]).toHaveTextContent("Green Room");
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
