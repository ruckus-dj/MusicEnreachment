import { fireEvent, render, screen, within } from "@testing-library/react";
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
	expect(
		within(screen.getAllByRole("table")[0]).getAllByRole("row"),
	).toHaveLength(11);
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

it("shows manual medium count and medium-position numbering", () => {
	const source = originalReleases.find((item) => item.id === "north");
	if (!source) throw new Error("Missing baseline fixture");
	const release = structuredClone(source);
	release.manual = true;
	release.media = [
		{ title: "Disc one", format: "CD", positions: [] },
		{ title: "Disc two", format: "CD", positions: [] },
	];
	release.tracks[0] = { ...release.tracks[0], medium: 1, mediumPosition: 1 };
	release.tracks[1] = { ...release.tracks[1], medium: 2, mediumPosition: 1 };
	render(
		<ReleaseScreen
			release={release}
			selected={new Set()}
			onSelect={vi.fn()}
			onPreview={vi.fn()}
		/>,
	);
	expect(
		screen.getByRole("heading", { name: "Список треков · 2 носителя" }),
	).toBeInTheDocument();
	const rows = screen.getAllByRole("row");
	expect(rows[1]).toHaveTextContent("1.01");
	expect(rows[2]).toHaveTextContent("2.01");
});

it("selects all release positions and represents a partial aggregate selection", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const onSelect = vi.fn();
	const firstTrackId = release.tracks[0]?.id;
	if (!firstTrackId) throw new Error("Missing baseline track");
	const { rerender } = render(
		<ReleaseScreen
			release={release}
			selected={new Set([firstTrackId])}
			onSelect={onSelect}
			onPreview={vi.fn()}
		/>,
	);
	const aggregate = screen.getByRole("checkbox", {
		name: `Выбрать все позиции релиза ${release.title}`,
	});
	expect((aggregate as HTMLInputElement).indeterminate).toBe(true);
	fireEvent.click(aggregate);
	expect(onSelect).toHaveBeenCalledWith(
		release.tracks.map((track) => track.id),
		true,
	);

	rerender(
		<ReleaseScreen
			release={release}
			selected={new Set(release.tracks.map((track) => track.id))}
			onSelect={onSelect}
			onPreview={vi.fn()}
		/>,
	);
	expect(
		screen.getByRole("checkbox", {
			name: `Выбрать все позиции релиза ${release.title}`,
		}),
	).toBeChecked();
});
