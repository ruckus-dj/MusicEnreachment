import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IncomingScreen } from "./IncomingScreen";
import { I18nProvider } from "../../i18n";
import { MatchingScreen } from "../../MatchingScreen";
import {
	createIncomingState,
	incomingFixtures,
	type IncomingFiles,
	type IncomingState,
} from "../../incomingModel";
import { initialSourceRoots } from "../../SourcesScreen";
import { splitFileIntoNewGroup } from "./incomingSplitModel";

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

function SplitHarness({
	files,
	initialState,
	onChange,
}: {
	files: IncomingFiles;
	initialState: IncomingState;
	onChange: (state: IncomingState) => void;
}) {
	const [state, setState] = useState(initialState);
	return (
		<IncomingScreen
			files={files}
			roots={initialSourceRoots}
			state={state}
			onState={(next) => {
				onChange(next);
				setState(next);
			}}
		/>
	);
}

const draftAssignment = (
	fileIds: string[],
	selectedSourceId: string,
): IncomingState["groups"][number]["drafts"][string][string] => ({
	sourceIds: fileIds,
	selectedSourceId,
	sourceMeta: Object.fromEntries(
		fileIds.map((id) => [
			id,
			{ method: "Авто · тестовый вердикт", confidence: "high" },
		]),
	),
	method: "Авто · тестовый вердикт",
	confidence: "high",
	providerTrackId: "p-north-t1",
	included: true,
});

it("splits one exact file without touching source tags or unrelated drafts", () => {
	window.history.replaceState(null, "", "#review?group=north");
	const files = structuredClone(incomingFixtures.files);
	const northTags = structuredClone(files["north-f1"].tags);
	const initial = createIncomingState();
	const north = initial.groups.find((group) => group.id === "north");
	const hours = initial.groups.find((group) => group.id === "hours");
	if (!north || !hours) throw new Error("Missing fixture groups");
	north.drafts = {
		"p-north": {
			"p-north-t1": draftAssignment(["north-f1", "north-extra"], "north-f1"),
		},
	};
	hours.drafts = {
		"p-hours": { "p-hours-t1": draftAssignment(["hours-f1"], "hours-f1") },
	};
	const hoursDrafts = structuredClone(hours.drafts);
	let latest = initial;

	render(
		<I18nProvider>
			<SplitHarness
				files={files}
				initialState={initial}
				onChange={(next) => {
					latest = next;
				}}
			/>
		</I18nProvider>,
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: "Отделить «First Light» в новую группу",
		}),
	);

	expect(
		screen.getByText("Группы: 4 · очередь источников"),
	).toBeInTheDocument();
	const splitGroup = latest.groups[1];
	expect(splitGroup?.id).toMatch(/^split-/);
	expect(latest.groups.map((group) => group.id)).toEqual([
		"north",
		splitGroup?.id,
		"hours",
		"mix",
	]);
	expect(splitGroup).toMatchObject({
		title: "Northern Lines",
		artistId: north.artistId,
		mode: "provider",
		candidateId: "p-north",
		releaseId: "north",
		fileIds: ["north-f1"],
		autoEligible: false,
		processed: false,
		drafts: { "p-north": {} },
	});
	expect(splitGroup?.reason).toEqual({ code: "incomplete" });

	const source = latest.groups[0];
	expect(source?.fileIds).toHaveLength(8);
	expect(source?.fileIds).not.toContain("north-f1");
	const kept = source?.drafts["p-north"]?.["p-north-t1"];
	expect(kept?.sourceIds).toEqual(["north-extra"]);
	expect(kept?.selectedSourceId).toBeNull();
	expect(kept?.sourceMeta).toEqual({
		"north-extra": { method: "Авто · тестовый вердикт", confidence: "high" },
	});
	expect(kept?.included).toBe(true);
	expect(latest.groups.find((group) => group.id === "hours")?.drafts).toEqual(
		hoursDrafts,
	);
	expect(latest.plan).toBeNull();
	expect(latest.inventoryVersion).toBe(initial.inventoryVersion);
	expect(files["north-f1"].tags).toEqual(northTags);
});

it("offers splitting only while another eligible file remains", () => {
	window.history.replaceState(null, "", "#review?group=north");
	const files = structuredClone(incomingFixtures.files);
	files["north-f2"].available = false;
	files["north-f2"].locations = [];
	const state = createIncomingState();
	state.groups[0].fileIds = ["north-f1", "north-f2"];

	render(
		<I18nProvider>
			<IncomingScreen files={files} roots={initialSourceRoots} state={state} />
		</I18nProvider>,
	);

	expect(
		screen.queryByRole("button", {
			name: "Отделить «First Light» в новую группу",
		}),
	).toBeNull();
	expect(
		screen.queryByRole("button", {
			name: "Отделить «Still Water» в новую группу",
		}),
	).toBeNull();
	expect(screen.getAllByRole("button", { name: "Переместить" })).toHaveLength(
		2,
	);
});

it("renders the existing Matching route for the split group without a dangling target", () => {
	const files = structuredClone(incomingFixtures.files);
	const split = splitFileIntoNewGroup(
		createIncomingState(),
		files,
		"north",
		"north-f1",
	);
	if (!split) throw new Error("Split expected");
	const group = split.groups.find((item) => item.id.startsWith("split-"));
	if (!group) throw new Error("Missing split group");

	render(
		<I18nProvider>
			<MatchingScreen
				groupId={group.id}
				state={split}
				files={files}
				providers={incomingFixtures.providerReleases}
				releases={incomingFixtures.releases}
			/>
		</I18nProvider>,
	);

	expect(
		screen.getByRole("heading", { name: "Разбор: Northern Lines" }),
	).toBeInTheDocument();
	expect(screen.queryByText("Цель сопоставления не найдена")).toBeNull();
	expect(screen.getByText("Назначено позиций: 0 из 10")).toBeInTheDocument();
	expect(screen.getByText("Доступно файлов: 1")).toBeInTheDocument();
});
