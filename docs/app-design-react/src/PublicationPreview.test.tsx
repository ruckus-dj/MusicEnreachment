import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	applyMetadataPreview,
	metadataChanged,
	originalFiles,
	originalReleases,
} from "./catalogData";
import { PublicationPreview } from "./PublicationPreview";
import { captureMetadataPreview } from "./previewModel";

const token = (ids: string[]) =>
	captureMetadataPreview(4, "selection:test", ids);

it("matches original Mira preview counts without selecting alternative sources", () => {
	const ids = originalReleases
		.filter((release) => release.artistId === "mira")
		.flatMap((release) => release.tracks.map((track) => track.id));
	const actionCounts = {
		Создать: 0,
		Обновить: 0,
		"Без изменений": 0,
		"Невозможно обработать": 0,
		Пропустить: 0,
	};
	for (const release of originalReleases.filter(
		(item) => item.artistId === "mira",
	))
		for (const track of release.tracks) {
			if (!track.published) {
				const file = track.sourceId ? originalFiles[track.sourceId] : undefined;
				actionCounts[
					file?.available && file.locations?.length ? "Создать" : "Пропустить"
				]++;
			} else {
				actionCounts[
					metadataChanged(release, track) ? "Обновить" : "Без изменений"
				]++;
			}
		}
	const onBack = vi.fn();
	render(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={onBack}
		/>,
	);
	for (const [action, count] of Object.entries(actionCounts))
		expect(screen.getByText(`${action}: ${count}`)).toBeInTheDocument();
	expect(screen.getAllByRole("row")).toHaveLength(ids.length + 1);
	expect(
		Object.values(actionCounts).reduce((sum, count) => sum + count, 0),
	).toBe(ids.length);
	fireEvent.click(
		screen.getByRole("button", { name: "Вернуться в медиатеку" }),
	);
	expect(onBack).toHaveBeenCalledOnce();
});

it("skips an unavailable source without claiming the file is missing", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const track = release.tracks.find((item) => item.id === "north-t9");
	if (!track) throw new Error("Missing north-t9 fixture");
	track.sourceId = "unknown-source";
	render(
		<PublicationPreview
			token={token([track.id])}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={vi.fn()}
			releases={[release]}
			files={{}}
		/>,
	);
	expect(screen.getByText("Пропустить: 1")).toBeInTheDocument();
	expect(
		screen.getByText(/файл недоступен для этой операции/i),
	).toBeInTheDocument();
	expect(screen.queryByText(/файл отсутствует/i)).not.toBeInTheDocument();
});

it("marks invalid metadata as unprocessable and explains the validation issue", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const track = release.tracks.find((item) => item.id === "north-t2");
	if (!track) throw new Error("Missing north-t2 fixture");
	track.recordingMetadata = { explicitstatus: ["maybe"] };
	render(
		<PublicationPreview
			token={token([track.id])}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={vi.fn()}
			releases={[release]}
		/>,
	);
	expect(screen.getByText("Невозможно обработать: 1")).toBeInTheDocument();
	expect(
		screen.getByText(/Некорректные метаданные: explicitstatus/),
	).toBeInTheDocument();
});

it("applies only create/update rows and excludes unprocessable metadata", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const invalidTrack = release.tracks.find((item) => item.id === "north-t2");
	if (!invalidTrack) throw new Error("Missing north-t2 fixture");
	invalidTrack.recordingMetadata = { explicitstatus: ["maybe"] };
	const onApply = vi.fn(() => true);
	render(
		<PublicationPreview
			token={token(["north-t2", "north-t6"])}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={vi.fn()}
			onApply={onApply}
			releases={[release]}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: /Создать \d+ · обновить метаданные \d+/,
		}),
	);
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onApply).toHaveBeenCalledWith({
		...token(["north-t2", "north-t6"]),
		selectedIds: ["north-t6"],
	});
});

it.each([
	[5, "selection:test", "inventory"],
	[4, "selection:other", "scope"],
] as const)("disables stale preview confirmation after %s changes", (_, nextScope, reason) => {
	const ids = originalReleases
		.filter((release) => release.artistId === "mira")
		.flatMap((release) => release.tracks.map((track) => track.id));
	const { rerender } = render(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={vi.fn()}
			onApply={vi.fn(() => true)}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: /Создать \d+ · обновить метаданные \d+/,
		}),
	);
	expect(screen.getByRole("dialog")).toBeInTheDocument();
	rerender(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={reason === "inventory" ? 5 : 4}
			scopeKey={nextScope}
			onBack={vi.fn()}
			onApply={vi.fn(() => true)}
		/>,
	);
	expect(screen.getByRole("alert")).toHaveTextContent("устарел");
	expect(screen.getByRole("button", { name: "Подтвердить" })).toBeDisabled();
});

it("marks an already stale preview and blocks opening confirmation", () => {
	const ids = originalReleases
		.filter((release) => release.artistId === "mira")
		.flatMap((release) => release.tracks.map((track) => track.id));
	render(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={5}
			scopeKey="selection:test"
			onBack={vi.fn()}
			onApply={vi.fn(() => true)}
		/>,
	);
	expect(screen.getByRole("alert")).toHaveTextContent("устарел");
	expect(
		screen.getByRole("button", {
			name: /Создать \d+ · обновить метаданные \d+/,
		}),
	).toBeDisabled();
	fireEvent.click(
		screen.getByRole("button", {
			name: /Создать \d+ · обновить метаданные \d+/,
		}),
	);
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("keeps a parent-rejected confirmation unapplied and communicates staleness", () => {
	const ids = originalReleases
		.filter((release) => release.artistId === "mira")
		.flatMap((release) => release.tracks.map((track) => track.id));
	const onApply = vi.fn(() => false);
	render(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={vi.fn()}
			onApply={onApply}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: /Создать \d+ · обновить метаданные \d+/,
		}),
	);
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onApply).toHaveBeenCalledOnce();
	expect(screen.getByRole("alert")).toHaveTextContent("устарел");
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("preserves published audio and does not fall back to an alternate source", () => {
	const source = Object.values(originalFiles).find(
		(file) => file.available && file.locations?.length,
	);
	expect(source).toBeDefined();
	const releases = structuredClone(originalReleases);
	const release = releases.find((item) =>
		item.tracks.some((track) => track.published),
	);
	expect(release).toBeDefined();
	const track = release?.tracks.find((item) => item.published);
	expect(track?.published).toBeDefined();
	if (!track?.published || !source || !release) return;
	const publishedAudio = structuredClone(track.published);
	track.sourceId = "unavailable-selected-source";
	track.sourceIds = [source.id];
	track.metadataBaseline = {
		...track.published.tags,
		TITLE: ["outdated title"],
	};
	const next = applyMetadataPreview(
		releases,
		new Set([track.id]),
		originalFiles,
	);
	const result = next
		.flatMap((item) => item.tracks)
		.find((item) => item.id === track.id);
	expect(result?.published).toMatchObject({
		path: publishedAudio.path,
		codec: publishedAudio.codec,
		sourceId: publishedAudio.sourceId,
	});
	expect(result?.published?.path).toBe(publishedAudio.path);
});
