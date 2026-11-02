import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import { SelectionDock } from "./SelectionDock";

it("shows the floating actions only for a shared selection and queues publication preview", () => {
	const onPreview = vi.fn();
	const onSelect = vi.fn();
	const publishedTrack = originalReleases
		.flatMap((release) => release.tracks)
		.find((track) => track.published);
	if (!publishedTrack) throw new Error("Missing published track fixture");
	const { rerender } = render(
		<SelectionDock
			selected={new Set()}
			releases={originalReleases}
			onSelect={onSelect}
			onPreview={onPreview}
			onQueueDeletion={vi.fn()}
		/>,
	);
	expect(
		screen.queryByRole("region", { name: "Выбранные позиции" }),
	).toBeNull();
	rerender(
		<SelectionDock
			selected={new Set([publishedTrack.id])}
			releases={originalReleases}
			onSelect={onSelect}
			onPreview={onPreview}
			onQueueDeletion={vi.fn()}
		/>,
	);
	const dock = screen.getByRole("region", { name: "Выбранные позиции" });
	expect(within(dock).getByRole("status")).toHaveTextContent("1");
	fireEvent.click(within(dock).getByRole("button", { name: "Опубликовать" }));
	expect(onPreview).toHaveBeenCalledWith([publishedTrack.id]);
	fireEvent.click(within(dock).getByRole("button", { name: "Снять выбор" }));
	expect(onSelect).toHaveBeenCalledWith([publishedTrack.id], false);
});

it("confirms only the frozen selected current publications and skips unpublished tracks", () => {
	const tracks = originalReleases.flatMap((release) => release.tracks);
	const publishedTrack = tracks.find((track) => track.published);
	const unpublishedTrack = tracks.find((track) => !track.published);
	if (!publishedTrack || !unpublishedTrack)
		throw new Error("Missing publication selection fixtures");
	const onQueueDeletion = vi.fn();
	render(
		<SelectionDock
			selected={new Set([publishedTrack.id, unpublishedTrack.id])}
			releases={originalReleases}
			onSelect={vi.fn()}
			onPreview={vi.fn()}
			onQueueDeletion={onQueueDeletion}
		/>,
	);
	fireEvent.click(
		within(screen.getByRole("region", { name: "Выбранные позиции" })).getByRole(
			"button",
			{ name: "Удалить публикации" },
		),
	);
	const dialog = screen.getByRole("dialog");
	expect(dialog).toHaveTextContent("Без публикации пропущено: 1");
	expect(dialog).toHaveTextContent(publishedTrack.published?.path ?? "");
	fireEvent.click(
		within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
	);
	expect(onQueueDeletion).toHaveBeenCalledWith(
		expect.objectContaining({
			kind: "publication",
			publications: 1,
			paths: [publishedTrack.published?.path],
		}),
	);
});

it("disables confirmation if a frozen publication path has changed", () => {
	const release = originalReleases.find((item) =>
		item.tracks.some((track) => track.published),
	);
	const track = release?.tracks.find((item) => item.published);
	const published = track?.published;
	if (!release || !track || !published)
		throw new Error("Missing published track fixture");
	const selected = new Set([track.id]);
	const { rerender } = render(
		<SelectionDock
			selected={selected}
			releases={originalReleases}
			onSelect={vi.fn()}
			onPreview={vi.fn()}
			onQueueDeletion={vi.fn()}
		/>,
	);
	fireEvent.click(
		within(screen.getByRole("region", { name: "Выбранные позиции" })).getByRole(
			"button",
			{ name: "Удалить публикации" },
		),
	);
	const changedReleases = originalReleases.map((item) =>
		item.id === release.id
			? {
					...item,
					tracks: item.tracks.map((candidate) =>
						candidate.id === track.id
							? {
									...candidate,
									published: { ...published, path: "/changed/path.flac" },
								}
							: candidate,
					),
				}
			: item,
	);
	rerender(
		<SelectionDock
			selected={selected}
			releases={changedReleases}
			onSelect={vi.fn()}
			onPreview={vi.fn()}
			onQueueDeletion={vi.fn()}
		/>,
	);
	const dialog = screen.getByRole("dialog");
	expect(dialog).toHaveTextContent("изменённые публикации будут пропущены");
	expect(
		within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
	).toBeDisabled();
});
