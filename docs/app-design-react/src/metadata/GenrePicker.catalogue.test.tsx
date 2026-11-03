import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { GenrePicker, type GenreReference } from "./GenrePicker";

it("keeps exact catalogue matches accessible on the first search page and retains selections across searches", () => {
	function Picker() {
		const [value, setValue] = useState<GenreReference[]>([]);
		return (
			<>
				<GenrePicker value={value} onChange={setValue} loaded />
				<output aria-label="Selected genre names">
					{value.map((item) => item.name).join(" | ")}
				</output>
			</>
		);
	}
	render(<Picker />);
	fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
	expect(screen.getAllByRole("option")).toHaveLength(60);
	const search = screen.getByRole("searchbox", { name: "Поиск жанров" });
	fireEvent.change(search, { target: { value: "rock" } });
	expect(screen.getAllByRole("option")[0]).toHaveAccessibleName("Rock");
	fireEvent.click(screen.getByRole("option", { name: "Rock" }));
	fireEvent.change(search, { target: { value: "ambient" } });
	fireEvent.click(screen.getByRole("option", { name: "Ambient" }));
	fireEvent.click(screen.getByRole("button", { name: "Готово" }));
	expect(
		screen.getByRole("status", { name: "Selected genre names" }),
	).toHaveTextContent("Rock | Ambient");
});
