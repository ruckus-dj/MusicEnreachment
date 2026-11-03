import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { MatchingScreen } from "./MatchingScreen";

it("reserves feedback space before an assignment and does not select the added audio automatically", () => {
	const { container } = render(<MatchingScreen groupId="north" />);
	const feedback = container.querySelector("[data-matching-feedback]");
	expect(feedback).toHaveClass("h-12");
	fireEvent.click(
		screen.getByRole("button", { name: /^session fragment\.flac/ }),
	);
	const target = container.querySelector<HTMLElement>(
		'[data-track-id="p-north-t1"]',
	);
	if (!target) throw new Error("Missing target position");
	fireEvent.click(
		within(target).getByRole("button", {
			name: "Добавить выбранный файл: session fragment.flac",
		}),
	);
	expect(container.querySelector("[data-matching-feedback]")).toBe(feedback);
	expect(feedback).toHaveClass("h-12");
	const row = target.closest("tr");
	if (!row) throw new Error("Missing assignment row");
	const radios = within(row).getAllByRole("radio");
	expect(radios).toHaveLength(2);
	expect(radios[0]).toBeChecked();
	expect(radios[1]).not.toBeChecked();
});
