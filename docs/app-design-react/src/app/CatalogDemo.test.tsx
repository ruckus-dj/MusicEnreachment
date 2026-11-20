import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { CatalogDemo } from "./CatalogDemo";

it("renders one keyboard-operable four-option filter radio group", () => {
	render(<CatalogDemo />);
	const group = screen.getByRole("radiogroup", { name: "Состояние" });
	const labels = ["Все", "Не хватает", "В наличии", "Опубликовано"];
	const radios = labels.map((name) =>
		within(group).getByRole("radio", { name }),
	);
	expect(screen.getAllByRole("radiogroup")).toHaveLength(1);
	expect(radios[0]).toBeChecked();
	radios[0].focus();
	fireEvent.keyDown(radios[0], { key: "ArrowRight" });
	expect(radios[1]).toBeChecked();
	fireEvent.keyDown(radios[1], { key: "ArrowRight" });
	expect(radios[2]).toBeChecked();
	fireEvent.keyDown(radios[2], { key: "ArrowRight" });
	expect(radios[3]).toBeChecked();
});
