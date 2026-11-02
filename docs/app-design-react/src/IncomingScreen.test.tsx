import { fireEvent, render, screen } from "@testing-library/react";
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
		screen.getByRole("link", { name: "Открыть локальный сборник" }),
	).toHaveAttribute("href", "#match/mix");
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
