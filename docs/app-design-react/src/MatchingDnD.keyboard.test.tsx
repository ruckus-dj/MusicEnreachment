import type { RefObject } from "react";
import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

const tracked = vi.hoisted(() => ({
	ref: null as RefObject<HTMLElement | null> | null,
	isDropTarget: false,
}));
vi.mock("react-aria/useDrop", () => ({
	useDrop: ({ ref }: { ref: RefObject<HTMLElement | null> }) => {
		tracked.ref = ref;
		return {
			dropProps: {},
			dropButtonProps: {},
			isDropTarget: tracked.isDropTarget,
		};
	},
}));

import { MatchingDropZone } from "./MatchingDnD";

it("keeps the registered keyboard target programmatically focusable without an idle tab stop", () => {
	const view = render(
		<MatchingDropZone
			groupId="north"
			trackId="north-t1"
			trackName="First Light"
			onAssign={() => true}
		>
			<span>01 - First Light.flac</span>
		</MatchingDropZone>,
	);
	const target = screen.getByRole("button", {
		name: "Добавить файл к позиции First Light",
	});
	expect(tracked.ref?.current).toBe(target);
	expect(target).toHaveAttribute("tabindex", "-1");
	expect(target).toHaveClass("pointer-events-none", "absolute", "inset-0");
	expect(
		screen.queryByText("Перетащите сюда доступный файл"),
	).not.toBeInTheDocument();
	target.focus();
	expect(target).toHaveFocus();

	tracked.isDropTarget = true;
	view.rerender(
		<MatchingDropZone
			groupId="north"
			trackId="north-t1"
			trackName="First Light"
			onAssign={() => true}
		>
			<span>01 - First Light.flac</span>
		</MatchingDropZone>,
	);
	expect(
		screen.getByText("Перетащите сюда доступный файл"),
	).toBeInTheDocument();
	tracked.isDropTarget = false;
});
