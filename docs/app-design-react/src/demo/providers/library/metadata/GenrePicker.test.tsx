import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { I18nProvider } from "../../demoPreferences";
import { GenrePicker } from "./GenrePicker";

it("retains a saved name-only genre when removing another selection", () => {
	localStorage.setItem("music-design-v2-language", "en");
	const onChange = vi.fn();
	render(
		<I18nProvider>
			<GenrePicker
				value={[{ name: "Rock" }, { id: "jazz", name: "Jazz" }]}
				options={[
					{ id: "rock", name: "Rock" },
					{ id: "jazz", name: "Jazz" },
				]}
				loaded
				onChange={onChange}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Genres" }));
	fireEvent.click(
		within(screen.getByRole("dialog")).getByRole("button", {
			name: "Remove Jazz",
		}),
	);
	expect(onChange).toHaveBeenCalledExactlyOnceWith([{ name: "Rock" }]);
});
