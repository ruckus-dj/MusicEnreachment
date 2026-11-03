import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { catalogData, originalFiles, originalReleases } from "./catalogData";
import { TagsScreen } from "./TagsScreen";

it("provides scoped four-column comparison and explicit source-file selection", () => {
	const onSelectFile = vi.fn();
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!track) throw new Error("Missing baseline track");
	render(
		<TagsScreen
			trackId={track.id}
			scope="artist"
			releases={originalReleases}
			artists={catalogData.artists}
			files={originalFiles}
			onSelectFile={onSelectFile}
		/>,
	);
	const table = screen.getByRole("table");
	expect(within(table).getAllByRole("columnheader")).toHaveLength(5);
	expect(
		within(table).getByRole("rowheader", { name: /(?:^|·\s*)ARTIST$/ }),
	).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", { name: /Исходный файл для сравнения/ }),
	);
	fireEvent.click(
		screen.getByRole("option", {
			name: /01 - First Light\.mp3/,
		}),
	);
	expect(onSelectFile).toHaveBeenCalledWith(track.id, track.sourceIds?.[1]);
	fireEvent.click(screen.getByRole("tab", { name: "Альбом" }));
	expect(
		within(table).getByRole("rowheader", { name: /(?:^|·\s*)ALBUM$/ }),
	).toBeInTheDocument();
});

it("keeps publication tags distinct and offers differences-only filtering", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track) throw new Error("Missing baseline fixture");
	render(
		<TagsScreen
			trackId={track.id}
			releases={[release]}
			artists={catalogData.artists}
			files={originalFiles}
		/>,
	);
	expect(screen.getByText(/Публикация: .*прочитано/)).toBeInTheDocument();
	const comparison = screen
		.getByRole("heading", { name: "Контекст сравнения" })
		.closest("section");
	if (!comparison) throw new Error("Missing comparison section");
	fireEvent.click(
		within(comparison).getByRole("checkbox", { name: /Только отличия/ }),
	);
	expect(screen.getByRole("table")).toBeInTheDocument();
});

it("keeps source values empty when the selected track has no source", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track) throw new Error("Missing baseline fixture");
	const withoutSource = {
		...release,
		tracks: [{ ...track, sourceId: null, sourceIds: [] }],
	};
	render(
		<TagsScreen
			trackId={track.id}
			releases={[withoutSource]}
			artists={catalogData.artists}
			files={originalFiles}
		/>,
	);
	expect(
		screen.getByRole("button", { name: /Исходный файл для сравнения/ }),
	).toHaveTextContent("Исходник не выбран");
	expect(
		within(screen.getByRole("table")).getAllByRole("row")[1],
	).toHaveTextContent("—");
});
