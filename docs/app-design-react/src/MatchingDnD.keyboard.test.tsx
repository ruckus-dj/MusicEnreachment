import type { RefObject } from "react";
import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

const tracked = vi.hoisted(() => ({
	ref: null as RefObject<HTMLElement | null> | null,
}));
vi.mock("react-aria/useDrop", () => ({
	useDrop: ({ ref }: { ref: RefObject<HTMLElement | null> }) => {
		tracked.ref = ref;
		return { dropProps: {}, dropButtonProps: {}, isDropTarget: false };
	},
}));

import { MatchingDropZone } from "./MatchingDnD";

it("registers the focusable drop button rather than its nonfocusable layout wrapper", () => {
	render(
		<MatchingDropZone
			groupId="north"
			trackId="north-t1"
			trackName="First Light"
			selectedFileId={null}
			selectedFileName={null}
			onAssign={() => true}
		/>,
	);
	const target = screen.getByRole("button", {
		name: "Добавить файл к позиции First Light",
	});
	expect(tracked.ref?.current).toBe(target);
	target.focus();
	expect(target).toHaveFocus();
});
