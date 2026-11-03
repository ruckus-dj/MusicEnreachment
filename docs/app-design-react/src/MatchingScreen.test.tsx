import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MatchingScreen } from "./MatchingScreen";
import {
	makePlan,
	createIncomingState,
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
	expect(screen.getAllByText("Высокий*").length).toBeGreaterThan(0);
	fireEvent.click(screen.getAllByRole("button", { name: "Доказательства" })[0]);
	expect(
		screen.getByText("Высокая уверенность · демонстрационные данные"),
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
		screen.queryByRole("button", { name: /Mira Vale.*ID fixture: mira/ }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Найти" }));
	fireEvent.click(
		await screen.findByRole("button", { name: /Mira Vale.*ID fixture: mira/ }),
	);
	expect(
		screen.getByText(/Издания выбранного артиста: Mira Vale/),
	).toBeInTheDocument();
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
			"Связь с композицией добавлена в черновик. Название и порядок локального сборника сохранены.",
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
		"Инвентарь источников изменился",
	);
	expect(
		screen.getByRole("button", { name: "Подтвердить публикацию" }),
	).toBeDisabled();
});
