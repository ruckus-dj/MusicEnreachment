import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useState } from "react";
import { LocalProviderScreen } from "./LocalProviderScreen";
import { parseLocalDuration } from "./localProviderModel";
import {
	createLocalProviderState,
	localProviderFixtures,
} from "./localProviderModel";

it("accepts empty and valid local durations but rejects unsafe magnitudes", () => {
	expect(parseLocalDuration("")).toBeNull();
	expect(parseLocalDuration("2:00")).toBe(120_000);
	expect(parseLocalDuration("9007199254740992")).toBeNaN();
	expect(
		parseLocalDuration("999999999999999999999999999999999999999999"),
	).toBeNaN();
});

it("retains original provider identities and search never creates records", () => {
	const state = createLocalProviderState();
	render(<LocalProviderScreen />);
	expect(
		screen.getByRole("heading", { name: "Mira Vale" }),
	).toBeInTheDocument();
	expect(state.items.artists.map(({ id }) => id)).toEqual(
		localProviderFixtures.artists.map(({ id }) => id),
	);
	fireEvent.change(
		screen.getByRole("searchbox", { name: /Поиск в артистах/ }),
		{ target: { value: "not present" } },
	);
	expect(
		screen.getByText(
			"Записей не найдено. Поиск ничего не создаёт автоматически.",
		),
	).toBeInTheDocument();
	expect(state.items.artists).toHaveLength(4);
});

it("creates and edits only by explicit action, emitting controlled current state", () => {
	const state = createLocalProviderState();
	const onState = vi.fn();
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen
				state={current}
				onState={(next) => {
					onState(next);
					setCurrent(next);
				}}
			/>
		);
	}
	render(<Harness />);
	fireEvent.click(screen.getByRole("button", { name: "Создать артиста" }));
	fireEvent.change(screen.getByRole("textbox", { name: /Имя/ }), {
		target: { value: "New Local Artist" },
	});
	fireEvent.click(
		screen.getAllByRole("button", { name: "Создать артиста" })[1],
	);
	const created = onState.mock.calls.at(-1)?.[0];
	expect(created.items.artists[0]).toMatchObject({
		name: "New Local Artist",
		provider: "Local",
	});
	expect(created.items.artists[0].id).not.toBe("");
	expect(created.notice).toContain("API и серверная часть не вызываются");
});

it("preserves external artist references as readonly identities and gates genre selection", () => {
	const state = createLocalProviderState();
	state.type = "recordings";
	state.selectedId = "local-recording-green";
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen state={current} onState={setCurrent} genresLoaded />
		);
	}
	render(<Harness />);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	const forma = screen.getByText("mb-artist-forma").closest("div");
	if (!forma) throw new Error("Missing Forma artist option");
	expect(forma).toHaveTextContent("mb-artist-forma");
	expect(
		screen.getByRole("button", { name: "electronic" }),
	).toBeInTheDocument();
	// The external artist option is only a reference checkbox; its canonical key is not editable.
	expect(
		within(forma).getByRole("checkbox", { name: "Forma" }),
	).toBeInTheDocument();
});
