import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	applyMetadataPreview,
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
	const onBack = vi.fn();
	render(
		<PublicationPreview
			token={token(ids)}
			inventoryVersion={4}
			scopeKey="selection:test"
			onBack={onBack}
		/>,
	);
	expect(screen.getByText("Создать: 4")).toBeInTheDocument();
	expect(screen.getByText("Обновить: 13")).toBeInTheDocument();
	expect(screen.getByText("Без изменений: 1")).toBeInTheDocument();
	expect(screen.getByText("Пропустить: 11")).toBeInTheDocument();
	expect(screen.getAllByRole("row")).toHaveLength(30);
	fireEvent.click(
		screen.getByRole("button", { name: "Вернуться в медиатеку" }),
	);
	expect(onBack).toHaveBeenCalledOnce();
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
		screen.getByRole("button", { name: "Опубликовать / обновить" }),
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
		screen.getByRole("button", { name: "Опубликовать / обновить" }),
	).toBeDisabled();
	fireEvent.click(
		screen.getByRole("button", { name: "Опубликовать / обновить" }),
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
		screen.getByRole("button", { name: "Опубликовать / обновить" }),
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
