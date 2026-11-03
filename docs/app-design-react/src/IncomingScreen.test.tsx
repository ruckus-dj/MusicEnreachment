import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { IncomingScreen } from "./IncomingScreen";
import { createIncomingState, incomingFixtures } from "./incomingModel";

it("shows the original incoming identities and routes into matching for each group", () => {
	render(<IncomingScreen />);
	expect(
		screen.getByRole("heading", { name: "Входящие альбомы" }),
	).toBeInTheDocument();
	expect(screen.getByRole("link", { name: "Northern Lines" })).toHaveAttribute(
		"href",
		"#match/north",
	);
	expect(
		screen.getByRole("grid", { name: "Текущие группы входящих альбомов" }),
	).toBeInTheDocument();
	expect(screen.getAllByRole("columnheader")).toHaveLength(8);
	expect(
		screen.getByRole("link", { name: "Открыть локальный сборник" }),
	).toHaveAttribute("href", "#match/mix");
});

it("shows an explicit empty state when there are no incoming groups", () => {
	const state = createIncomingState();
	state.groups = [];
	render(<IncomingScreen state={state} />);
	expect(screen.getByText("Входящих групп пока нет.")).toBeInTheDocument();
});

it("shows labeled incoming group summaries instead of a wide table on mobile", () => {
	const originalMatchMedia = Object.getOwnPropertyDescriptor(
		window,
		"matchMedia",
	);
	Object.defineProperty(window, "matchMedia", {
		configurable: true,
		value: vi.fn().mockReturnValue({
			matches: true,
			media: "(max-width: 599px)",
			addEventListener: vi.fn(),
			removeEventListener: vi.fn(),
		}),
	});
	try {
		const northReason = createIncomingState().groups.find(
			(group) => group.id === "north",
		)?.reason;
		render(<IncomingScreen />);
		const list = screen.getByRole("list", {
			name: "Текущие группы входящих альбомов",
		});
		const groupSummaries = [...list.querySelectorAll("li")];
		expect(list).toBeInTheDocument();
		expect(
			screen.getByRole("link", { name: "Northern Lines" }),
		).toHaveAttribute("href", "#match/north");
		expect(northReason).toBeTruthy();
		expect(screen.getByText(northReason ?? "")).toBeInTheDocument();
		for (const item of groupSummaries) {
			expect(within(item).getByText(/Файлы:/)).toBeInTheDocument();
			expect(within(item).getByText(/Назначено:/)).toBeInTheDocument();
			expect(within(item).getByText(/Свободно:/)).toBeInTheDocument();
			expect(within(item).getByText(/Опубликовано:/)).toBeInTheDocument();
		}
		expect(
			screen.queryByRole("grid", {
				name: "Текущие группы входящих альбомов",
			}),
		).not.toBeInTheDocument();
	} finally {
		if (originalMatchMedia) {
			Object.defineProperty(window, "matchMedia", originalMatchMedia);
		} else {
			Reflect.deleteProperty(window, "matchMedia");
		}
	}
});

it("runs the fully eligible group and keeps incomplete groups in manual review", () => {
	const state = createIncomingState();
	const files = structuredClone(incomingFixtures.files);
	const releases = structuredClone(incomingFixtures.releases);
	const onState = vi.fn();
	const onReleases = vi.fn();
	render(
		<IncomingScreen
			state={state}
			files={files}
			releases={releases}
			onState={onState}
			onReleases={onReleases}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Запустить обработку · демо" }),
	);
	const nextState = onState.mock.calls[0][0];
	expect(
		nextState.groups.find((group: { id: string }) => group.id === "hours")
			.processed,
	).toBe(true);
	expect(
		nextState.groups.find((group: { id: string }) => group.id === "north")
			.processed,
	).toBe(false);
	expect(onReleases).toHaveBeenCalled();
	expect(nextState.notice).toContain("8 позиций Small Hours");
});
