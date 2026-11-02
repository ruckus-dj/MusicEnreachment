import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import { ReleaseScreen } from "./ReleaseScreen";

it("shows the complete edition and previews precisely its own positions", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const onPreview = vi.fn();
	render(
		<ReleaseScreen
			release={release}
			selected={new Set()}
			onSelect={vi.fn()}
			onPreview={onPreview}
		/>,
	);
	expect(screen.getAllByRole("row")).toHaveLength(11);
	expect(screen.getByRole("link", { name: "Winter Birds" })).toHaveAttribute(
		"href",
		"#track/north-t9",
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Предпросмотр публикации" }),
	);
	expect(onPreview).toHaveBeenCalledWith(
		release.tracks.map((track) => track.id),
	);
	expect(screen.getByText(/5\/10 · Частично опубликовано/)).toBeInTheDocument();
});
