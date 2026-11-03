import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalFiles, originalReleases } from "./catalogData";
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
	fireEvent.click(screen.getByText(/Аудио и публикация/));
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
	fireEvent.click(screen.getByText("Сравнить теги файла и публикации"));
	expect(screen.getByRole("table")).toBeInTheDocument();
});

it("reports cover and lyrics metadata changes and observed file time", () => {
	const original = originalReleases.find((item) => item.id === "north");
	const baseline = original?.tracks.find((item) => item.id === "north-t2");
	if (!original || !baseline?.published)
		throw new Error("Missing published fixture");
	const release = structuredClone(original);
	const track = release.tracks.find((item) => item.id === baseline.id);
	if (!track?.published) throw new Error("Missing published track fixture");
	track.metadataBaselineExtras = {
		coverHash: track.published.coverHash,
		lyrics: track.published.lyrics,
	};
	track.coverHash = "new-cover-hash";
	track.lyrics = "Updated lyrics";
	track.observedFile = { state: "changed", mtime: "2026-10-31T12:34:56Z" };
	render(
		<TrackScreen
			release={release}
			track={track}
			onSource={vi.fn()}
			onPreview={vi.fn()}
		/>,
	);
	expect(screen.getByText(/Метаданные изменились/)).toBeInTheDocument();
	expect(screen.getByText("Файл изменён вне приложения")).toBeInTheDocument();
	fireEvent.click(screen.getByText("Состояние и технические сведения"));
	expect(screen.getByText("2026-10-31T12:34:56Z")).toBeInTheDocument();
});

it("does not report a missing publication when its output root is unavailable", () => {
	const original = originalReleases.find((item) => item.id === "north");
	const baseline = original?.tracks.find((item) => item.id === "north-t2");
	if (!original || !baseline?.published)
		throw new Error("Missing published fixture");
	const release = structuredClone(original);
	const track = release.tracks.find((item) => item.id === baseline.id);
	if (!track) throw new Error("Missing track fixture");
	track.observedFile = { state: "missing", rootAvailable: false };
	render(
		<TrackScreen
			release={release}
			track={track}
			onSource={vi.fn()}
			onPreview={vi.fn()}
		/>,
	);
	expect(
		screen.getByText(
			/выходной каталог недоступен; наличие файла не установлено/,
		),
	).toBeInTheDocument();
	expect(
		screen.queryByText(/^Файл публикации: отсутствует\.$/),
	).not.toBeInTheDocument();
});

it("reports missing output only when the root was available for inspection", () => {
	const original = originalReleases.find((item) => item.id === "north");
	const baseline = original?.tracks.find((item) => item.id === "north-t2");
	if (!original || !baseline?.published)
		throw new Error("Missing published fixture");
	const release = structuredClone(original);
	const track = release.tracks.find((item) => item.id === baseline.id);
	if (!track) throw new Error("Missing track fixture");
	track.observedFile = { state: "missing", rootAvailable: true };
	render(
		<TrackScreen
			release={release}
			track={track}
			onSource={vi.fn()}
			onPreview={vi.fn()}
		/>,
	);
	expect(screen.getByText("Файл публикации: отсутствует.")).toBeInTheDocument();
});

it("keeps the current publication path and tag snapshot in its own disclosure", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track?.published || !track.sourceId)
		throw new Error("Missing published track fixture");
	const published = track.published;
	const localPath = `/local/${track.sourceId}`;
	render(
		<TrackScreen
			release={release}
			track={track}
			files={{
				...originalFiles,
				[track.sourceId]: { ...originalFiles[track.sourceId], path: localPath },
			}}
			onSource={vi.fn()}
			onPreview={vi.fn()}
		/>,
	);
	const publicationTrigger = screen.getByRole("button", {
		name: "Текущая публикация",
	});
	expect(publicationTrigger).toHaveAttribute("aria-expanded", "false");
	expect(screen.getByText(localPath)).not.toBeVisible();
	fireEvent.click(screen.getByText(/Аудио и публикация/));
	fireEvent.click(screen.getByText("Состояние и технические сведения"));
	expect(screen.getByText(localPath)).toBeInTheDocument();
	fireEvent.click(publicationTrigger);
	expect(screen.getByText(published.path)).toBeInTheDocument();
	expect(
		screen.getByText(
			(content, element) =>
				element?.tagName === "DD" &&
				content.includes(`TITLE: ${published.tags.TITLE[0]}`),
		),
	).toBeInTheDocument();
	expect(screen.getByText(published.readAt)).toBeInTheDocument();
	expect(
		screen.queryByText("Путь и снимок публикации"),
	).not.toBeInTheDocument();
});
