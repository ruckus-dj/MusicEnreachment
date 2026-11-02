import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import { TrackScreen } from "./TrackScreen";

it("shows every linked audio variant and requests an explicit source change", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track) throw new Error("Missing baseline fixture");
	const before = structuredClone(track.published);
	const onSource = vi.fn();
	render(
		<TrackScreen
			release={release}
			track={track}
			onSource={onSource}
			onPreview={vi.fn()}
		/>,
	);
	const variants = screen.getAllByRole("radio");
	expect(variants).toHaveLength(2);
	fireEvent.click(variants[1]);
	expect(onSource).toHaveBeenCalledWith(track.sourceIds?.[1]);
	expect(track.published).toEqual(before);
});

it("opens a contextual metadata preview without requiring selection", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t2");
	if (!release || !track) throw new Error("Missing baseline fixture");
	const onPreview = vi.fn();
	render(
		<TrackScreen
			release={release}
			track={track}
			onSource={vi.fn()}
			onPreview={onPreview}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Обновить метаданные" }));
	expect(onPreview).toHaveBeenCalledOnce();
	expect(screen.getByRole("table")).toBeInTheDocument();
});
