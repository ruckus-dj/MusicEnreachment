import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { I18nProvider } from "../demoPreferences";
import { IncomingFileActions } from "../../../ui/features/incoming/IncomingFileActions";

const fileId = "north-f1";
const fileTitle = "First Light";

const destinations = Array.from({ length: 150 }, (_, index) => {
	const number = index + 1;
	const name = `Группа ${String(number).padStart(3, "0")}`;
	return {
		id: `dest-${number}`,
		name,
		paths: number === 87 ? ["/library/arch/Deep Cuts"] : [],
	};
});

const destinationMenu = () =>
	screen.getByRole("menu", { name: "Переместить в…" });

it("filters 150 move destinations and commits only an explicit choice", async () => {
	window.localStorage.setItem("music-design-v2-language", "ru");
	const onMove = vi.fn();
	const onCreateGroup = vi.fn();
	render(
		<I18nProvider>
			<IncomingFileActions
				fileId={fileId}
				fileTitle={fileTitle}
				destinations={destinations}
				canCreateGroup={false}
				onMove={onMove}
				onCreateGroup={onCreateGroup}
			/>
		</I18nProvider>,
	);

	fireEvent.click(
		screen.getByRole("button", {
			name: `Действия с файлом: ${fileTitle}`,
		}),
	);
	const move = await screen.findByRole("menuitem", { name: "Переместить в…" });
	move.focus();
	fireEvent.keyDown(move, { key: "ArrowRight" });

	const search = await screen.findByLabelText("Поиск группы");
	const initial = within(destinationMenu()).getAllByRole("menuitem");
	expect(initial).toHaveLength(150);
	expect(initial[0]).toHaveTextContent("Группа 001");
	expect(initial[149]).toHaveTextContent("Группа 150");

	fireEvent.change(search, { target: { value: "Группа 1" } });
	const filtered = within(destinationMenu()).getAllByRole("menuitem");
	expect(
		filtered.map((item) => item.textContent?.match(/Группа \d{3}/)?.[0]),
	).toEqual(
		Array.from(
			{ length: 51 },
			(_, index) => `Группа ${String(index + 100).padStart(3, "0")}`,
		),
	);
	expect(onMove).not.toHaveBeenCalled();

	fireEvent.keyDown(search, { key: "Enter" });
	expect(onMove).not.toHaveBeenCalled();

	fireEvent.change(search, { target: { value: "Группа 999" } });
	expect(await screen.findByText("Группы не найдены")).toBeInTheDocument();
	expect(screen.queryByRole("menuitem", { name: "Группа 087" })).toBeNull();
	expect(onMove).not.toHaveBeenCalled();

	fireEvent.change(search, { target: { value: "" } });
	expect(within(destinationMenu()).getAllByRole("menuitem")).toHaveLength(150);

	fireEvent.change(search, { target: { value: "deep cuts" } });
	const [only] = within(destinationMenu()).getAllByRole("menuitem");
	expect(only).toHaveTextContent("Группа 087");
	expect(onMove).not.toHaveBeenCalled();
	fireEvent.click(only);

	expect(onMove.mock.calls).toEqual([[fileId, "dest-87"]]);
	expect(onCreateGroup).not.toHaveBeenCalled();
});
