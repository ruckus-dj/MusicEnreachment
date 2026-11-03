import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MatchingScreen } from "./MatchingScreen";
import {
	decodeMatchingFilePayload,
	MATCHING_FILE_DRAG_TYPE,
} from "./MatchingDnD";
import {
	makePlan,
	createIncomingState,
	getDraft,
	incomingFixtures,
	removeRootFromIncomingInventory,
	type IncomingState,
} from "./incomingModel";

it("does not expose another group's frozen publication preview", () => {
	const state = createIncomingState();
	const files = structuredClone(incomingFixtures.files);
	const releases = structuredClone(incomingFixtures.releases);
	state.plan = makePlan(state, files, releases);
	const view = render(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			releases={releases}
			onState={vi.fn()}
		/>,
	);
	expect(
		screen.getByRole("heading", { name: /Предпросмотр: Northern Lines/ }),
	).toBeInTheDocument();
	view.rerender(
		<MatchingScreen
			groupId="mix"
			state={state}
			files={files}
			releases={releases}
			onState={vi.fn()}
		/>,
	);
	expect(
		screen.queryByRole("heading", { name: /Предпросмотр: Northern Lines/ }),
	).not.toBeInTheDocument();
	expect(
		screen.queryByRole("button", { name: "Подтвердить публикацию" }),
	).not.toBeInTheDocument();
});

it("starts with the Northern Lines candidate draft and leaves missing positions assignable", () => {
	render(<MatchingScreen groupId="north" />);
	expect(
		screen.getByRole("heading", { name: /Разбор: Northern Lines/ }),
	).toBeInTheDocument();
	expect(
		screen.getByRole("checkbox", { name: "Публиковать First Light" }),
	).toBeChecked();
	expect(
		screen.getByRole("checkbox", { name: "Публиковать Winter Birds" }),
	).toBeDisabled();
	expect(screen.getByText("session fragment.flac")).toBeInTheDocument();
	expect(screen.getAllByText("Высокая*").length).toBeGreaterThan(0);
	const selectedSource = screen.getAllByRole("radio")[0];
	expect(selectedSource).toBeChecked();
	expect(
		selectedSource.closest("label")?.querySelector(".indicator"),
	).toBeInTheDocument();
	fireEvent.click(screen.getAllByRole("button", { name: "Доказательства" })[0]);
	expect(
		screen.getByText("Высокая уверенность · пример данных"),
	).toBeInTheDocument();
});

it("requires submitted searches, supports artist-to-release selection, and reports unavailable URL identities", async () => {
	render(<MatchingScreen groupId="north" />);
	expect(
		screen.queryByRole("button", { name: /Northern Lines.*2022/ }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Артисты" }));
	fireEvent.change(screen.getByRole("searchbox", { name: "Поиск артистов" }), {
		target: { value: "Mira Vale" },
	});
	expect(
		screen.queryByRole("button", { name: /Mira Vale.*ID артиста: mira/ }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Найти" }));
	fireEvent.click(
		await screen.findByRole("button", { name: /Mira Vale.*ID артиста: mira/ }),
	);
	expect(screen.getByText(/Издания артиста Mira Vale:/)).toBeInTheDocument();
	expect(
		screen.getByRole("button", { name: /Northern Lines.*2021/ }),
	).toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Точный ID / URL" }));
	fireEvent.change(
		screen.getByRole("textbox", { name: "Идентификатор или MusicBrainz URL" }),
		{
			target: {
				value:
					"https://musicbrainz.org/release/00000000-0000-4000-8000-000000000001",
			},
		},
	);
	fireEvent.click(screen.getByRole("button", { name: "Найти" }));
	expect(
		await screen.findByText(/нет MusicBrainz MBID\/URL/),
	).toBeInTheDocument();
});

it("clears the selected source file on Escape and persists the state transition", () => {
	const state = createIncomingState();
	state.selectedFile = "north-f1";
	const onState = vi.fn();
	const { container } = render(
		<MatchingScreen groupId="north" state={state} onState={onState} />,
	);
	fireEvent.keyDown(container.querySelector("main")!, { key: "Escape" });
	expect(onState).toHaveBeenCalledWith(
		expect.objectContaining({ selectedFile: null }),
	);
});

it("supports keyboard assignment and removal without changing the chosen publication source", () => {
	let state: IncomingState = createIncomingState();
	const files = structuredClone(incomingFixtures.files);
	const extraFile = structuredClone(files["north-f1"]);
	extraFile.id = "north-extra";
	extraFile.name = "Extra session.flac";
	files[extraFile.id] = extraFile;
	state.groups
		.find((group) => group.id === "north")
		?.fileIds.push(extraFile.id);
	state.selectedFile = extraFile.id;
	const onState = vi.fn((next: IncomingState) => {
		state = next;
	});
	const view = render(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			onState={onState}
		/>,
	);
	const targetTable =
		view.container.querySelector<HTMLElement>(".matching-target")!;
	const firstLightRow = () =>
		within(targetTable).getByRole("row", { name: /First Light/ });
	const addSelected = within(firstLightRow()).getByRole("button", {
		name: "Добавить выбранный файл: Extra session.flac",
	});
	fireEvent.keyDown(addSelected, { key: "Enter" });
	fireEvent.keyUp(addSelected, { key: "Enter" });
	expect(onState).toHaveBeenCalled();
	view.rerender(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			onState={onState}
		/>,
	);
	const group = state.groups.find((item) => item.id === "north");
	expect(group).toBeDefined();
	const getAssignments = () => getDraft(group!, files);
	const assigned = Object.values(getAssignments()).find((item) =>
		item.sourceIds.includes(extraFile.id),
	);
	expect(assigned).toBeDefined();
	expect(assigned?.selectedSourceId).toBe("north-f1");
	const remove = within(firstLightRow()).getByRole("button", {
		name: "Снять связь Extra session.flac с First Light",
	});
	fireEvent.keyDown(remove, { key: "Enter" });
	fireEvent.keyUp(remove, { key: "Enter" });
	view.rerender(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			onState={onState}
		/>,
	);
	expect(
		Object.values(getAssignments()).some((item) =>
			item.sourceIds.includes(extraFile.id),
		),
	).toBe(false);
});

it("rejects malformed and cross-group drag payloads and keeps reduced-motion styling", () => {
	const payload = (groupId: string, fileId: string) =>
		JSON.stringify({ groupId, fileId });
	expect(decodeMatchingFilePayload(payload("north", "north-f3"), "north")).toBe(
		"north-f3",
	);
	expect(
		decodeMatchingFilePayload(payload("mix", "north-f3"), "north"),
	).toBeNull();
	expect(decodeMatchingFilePayload("not-json", "north")).toBeNull();
	expect(MATCHING_FILE_DRAG_TYPE).toContain("melotrove-matching-file");

	const { container } = render(<MatchingScreen groupId="north" />);
	const zone = container.querySelector("[data-track-id]");
	expect(zone).toHaveClass("motion-safe:transition-colors");
});

it("switches candidates without replacing the other candidate's draft", async () => {
	render(<MatchingScreen groupId="north" />);
	const firstInclude = screen.getByRole("checkbox", {
		name: "Публиковать Still Water",
	});
	fireEvent.click(firstInclude);
	fireEvent.click(screen.getByRole("button", { name: "Точный ID / URL" }));
	fireEvent.change(
		screen.getByRole("textbox", { name: "Идентификатор или MusicBrainz URL" }),
		{
			target: { value: "p-japan" },
		},
	);
	fireEvent.click(screen.getByRole("button", { name: "Найти" }));
	fireEvent.click(
		await screen.findByRole("button", { name: /Northern Lines.*2022/ }),
	);
	const japan = screen.getByRole("button", { name: /p-japan/ });
	expect(japan).toHaveAttribute("aria-pressed", "true");
	expect(
		screen.getByRole("checkbox", { name: "Публиковать Still Water" }),
	).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Точный ID / URL" }));
	fireEvent.change(
		screen.getByRole("textbox", { name: "Идентификатор или MusicBrainz URL" }),
		{
			target: { value: "p-north" },
		},
	);
	fireEvent.click(screen.getByRole("button", { name: "Найти" }));
	fireEvent.click(
		await screen.findByRole("button", { name: /Northern Lines.*2021/ }),
	);
	expect(
		screen.getByRole("checkbox", { name: "Публиковать Still Water" }),
	).not.toBeChecked();
});

it("keeps local compilation names/order when associating a provider recording", () => {
	render(<MatchingScreen groupId="mix" />);
	const title = screen.getAllByRole("heading", {
		name: /Road Notes — личный сборник/,
	})[0];
	expect(title).toBeInTheDocument();
	fireEvent.click(
		screen.getAllByRole("button", { name: "Найти композицию" })[0],
	);
	fireEvent.change(screen.getByRole("textbox", { name: "Название / артист" }), {
		target: { value: "Relay" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Найти композиции" }));
	fireEvent.click(screen.getByRole("button", { name: "Связать композицию" }));
	expect(
		screen.getAllByRole("heading", { name: /Road Notes — личный сборник/ }),
	).toHaveLength(2);
	expect(
		screen.getByText(
			"Связь с композицией добавлена в черновик. Название и порядок сборника сохранены.",
		),
	).toBeInTheDocument();
});

it("warns and prevents confirmation after inventory changes", () => {
	let state: IncomingState = createIncomingState();
	const files = structuredClone(incomingFixtures.files);
	const releases = structuredClone(incomingFixtures.releases);
	const onState = vi.fn((next: IncomingState) => {
		state = next;
	});
	const view = render(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			releases={releases}
			onState={onState}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Предпросмотр публикации" }),
	);
	view.rerender(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			releases={releases}
			onState={onState}
		/>,
	);
	expect(
		screen.getByRole("heading", { name: /Предпросмотр: Northern Lines/ }),
	).toBeInTheDocument();
	removeRootFromIncomingInventory("root-inbox", state, files, releases);
	view.rerender(
		<MatchingScreen
			groupId="north"
			state={state}
			files={files}
			releases={releases}
			onState={onState}
		/>,
	);
	expect(screen.getByRole("alert")).toHaveTextContent(
		"Предпросмотр устарел из-за изменения инвентаря",
	);
	expect(
		screen.getByRole("button", { name: "Подтвердить публикацию" }),
	).toBeDisabled();
});
