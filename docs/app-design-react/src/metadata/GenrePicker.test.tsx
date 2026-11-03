import {
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { GenrePicker, type GenreReference } from "./GenrePicker";

const genres = [
	{ id: "demo-a", name: "ambient" },
	{ id: "demo-r", name: "rock" },
];

function Harness({
	loaded = true,
	onLoad,
}: {
	loaded?: boolean;
	onLoad?: () => void | Promise<void>;
}) {
	const [value, setValue] = useState<GenreReference[]>([
		{ id: "legacy-id", name: "legacy genre" },
	]);
	return (
		<GenrePicker
			value={value}
			onChange={setValue}
			loaded={loaded}
			onLoad={onLoad}
			options={genres}
		/>
	);
}

describe("GenrePicker", () => {
	it("keeps unknown selections while filtering and selecting catalog entries", () => {
		render(<Harness />);
		fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
		expect(
			within(screen.getByRole("dialog")).getByText("legacy genre"),
		).toBeInTheDocument();
		const search = screen.getByRole("searchbox", { name: "Поиск жанров" });
		fireEvent.change(search, { target: { value: "rock" } });
		fireEvent.click(screen.getByRole("option", { name: "rock" }));
		expect(
			within(screen.getByRole("dialog")).getByText("legacy genre"),
		).toBeInTheDocument();
		expect(screen.getByRole("option", { name: "rock" })).toHaveAttribute(
			"aria-selected",
			"true",
		);
		fireEvent.click(
			screen.getByRole("button", { name: "Убрать legacy genre" }),
		);
		expect(screen.queryByText("legacy genre")).not.toBeInTheDocument();
	});

	it("provides an in-picker load retry and preserves selected values on rejection", async () => {
		const onLoad = vi.fn().mockRejectedValue(new Error("offline"));
		render(<Harness loaded={false} onLoad={onLoad} />);
		const trigger = screen.getByRole("button", { name: "Жанры" });
		expect(trigger).toBeEnabled();
		fireEvent.click(trigger);
		fireEvent.click(screen.getByRole("button", { name: "Загрузить каталог" }));
		await waitFor(() => expect(screen.getByRole("alert")).toBeInTheDocument());
		expect(
			within(screen.getByRole("dialog")).getByText("legacy genre"),
		).toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "Повторить загрузку" }),
		).toBeEnabled();
	});

	it("offers a settings link when no load action is available", () => {
		render(<Harness loaded={false} />);
		fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
		expect(
			screen.getByRole("link", { name: "Перейти к настройкам каталога" }),
		).toHaveAttribute("href", "#settings");
	});
});
