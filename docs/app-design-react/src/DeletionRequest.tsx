import { useState } from "react";
import { Button, Dialog, Heading, Modal, Tooltip, TooltipTrigger } from "./ui";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import { deletionDialog, deletionFooter } from "./matchingStyles";
import { useI18n } from "./i18n/I18nProvider";
import { catalogueMessage, type CatalogueMessage } from "./catalogueMessages";

export interface DeletionOperation {
	kind?: "library" | "publication" | "artist";
	target: string;
	scope: string;
	publications: number;
	paths: string[];
	libraryReleaseIds?: string[];
	libraryTrackIds?: string[];
	libraryArtistId?: string;
}

export function publicationSelectionPlan(
	tracks: OriginalTrack[],
	releases: OriginalRelease[],
): DeletionOperation {
	const titles = tracks.map((track) => {
		const release = releases.find((item) =>
			item.tracks.some((candidate) => candidate.id === track.id),
		);
		return `${release?.title ?? "Релиз не найден"} · ${String(track.position).padStart(2, "0")} · ${track.title}`;
	});
	return {
		kind: "publication",
		libraryTrackIds: tracks
			.filter((track) => track.published)
			.map((track) => track.id),
		target: `${tracks.length} выбранных публикаций`,
		scope: `Только текущие публикации выбранных позиций (${titles.join("; ")}); позиции медиатеки, композиции, записи провайдера, исходники и другие публикации сохраняются.`,
		publications: tracks.filter((track) => track.published).length,
		paths: tracks.flatMap((track) =>
			track.published ? [track.published.path] : [],
		),
	};
}

export function deletionPlan(
	release: OriginalRelease,
	track?: OriginalTrack,
): DeletionOperation {
	const tracks = track ? [track] : release.tracks;
	return {
		kind: track ? "publication" : "library",
		libraryReleaseIds: track ? [] : [release.id],
		libraryTrackIds: tracks.map((item) => item.id),
		target: track
			? `${release.title} · ${String(track.position).padStart(2, "0")} · ${track.title}`
			: release.title || "Релиз без названия",
		scope: track
			? "Только выбранная публикация; позиция, издание, композиция, записи провайдера и другие публикации сохраняются."
			: `${release.tracks.length} позиций релиза; самостоятельные композиции, записи провайдера и публикации других релизов сохраняются.`,
		publications: tracks.filter((item) => item.published).length,
		paths: tracks.flatMap((item) =>
			item.published ? [item.published.path] : [],
		),
	};
}

export function DeletionRequest({
	release,
	track,
	onQueue,
	iconOnly = false,
	triggerLabel,
}: {
	release: OriginalRelease;
	track?: OriginalTrack;
	onQueue: (operation: DeletionOperation) => void;
	iconOnly?: boolean;
	triggerLabel?: string;
}) {
	const [open, setOpen] = useState(false);
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const plan = deletionPlan(release, track);
	const displayedTarget = track
		? `${release.title} · ${String(track.position).padStart(2, "0")} · ${track.title}`
		: release.title || m("unnamedRelease");
	const displayedScope = track
		? m("deletePublicationScope")
		: m("deleteReleaseScope", { count: release.tracks.length });
	return (
		<>
			{iconOnly ? (
				<TooltipTrigger>
					<Button
						aria-label={triggerLabel ?? m("deleteAlbum")}
						className="artist-album-delete ui-button--danger text-[var(--error)]"
						onPress={() => setOpen(true)}
					>
						<svg
							viewBox="0 0 24 24"
							width="18"
							height="18"
							fill="none"
							stroke="currentColor"
							strokeWidth="1.8"
							strokeLinecap="round"
							strokeLinejoin="round"
							aria-hidden="true"
						>
							<path d="M3 6h18M8 6V4h8v2m3 0-1 14H6L5 6m4 4v6m6-6v6" />
						</svg>
					</Button>
					<Tooltip>{m("deleteAlbum")}</Tooltip>
				</TooltipTrigger>
			) : (
				<Button onPress={() => setOpen(true)}>
					{track ? m("deletePublication") : m("deleteAlbum")}
				</Button>
			)}
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="deletion-title" className={deletionDialog}>
					<Heading slot="title" id="deletion-title">
						{m("deleteQuestion", { target: displayedTarget })}
					</Heading>
					<p>{displayedScope}</p>
					<p>{m("ownedFilesDeletion", { count: plan.publications })}</p>
					{!track && (
						<>
							<h3 className="mt-1.5 mb-0 text-xs">
								{m("releaseWillBeDeleted")}
							</h3>
							<p>{release.title}</p>
							<h3 className="mt-1.5 mb-0 text-xs">{m("recordingsKept")}</h3>
							<ul className="m-0 pl-5">
								{[
									...new Map(
										release.tracks
											.filter((item) => item.recordingId)
											.map((item) => [item.recordingId, item]),
									).values(),
								].map((item) => (
									<li
										className="my-[3px] [overflow-wrap:anywhere]"
										key={item.recordingId}
									>
										{item.title} · {item.recordingId}
									</li>
								))}
							</ul>
							<h3 className="mt-1.5 mb-0 text-xs">
								{m("releasePositionsWillBeDeleted")}
							</h3>
							<ul className="m-0 pl-5">
								{release.tracks.map((item) => (
									<li
										className="my-[3px] [overflow-wrap:anywhere]"
										key={item.id}
									>
										{release.title} · {String(item.position).padStart(2, "0")} ·{" "}
										{item.title} ·{" "}
										{m(item.published ? "published" : "unpublished")}
									</li>
								))}
							</ul>
						</>
					)}
					{plan.paths.length ? (
						<ul className="m-0 pl-5">
							{plan.paths.map((path) => (
								<li className="my-[3px] [overflow-wrap:anywhere]" key={path}>
									<code>{path}</code>
								</li>
							))}
						</ul>
					) : (
						<p>{m("noCurrentPublications")}</p>
					)}
					<p>{m("deletionRequestNotice")}</p>
					<div className={deletionFooter}>
						<Button onPress={() => setOpen(false)}>{m("cancel")}</Button>
						<Button
							className="border-[var(--error)]! text-[var(--error)]!"
							onPress={() => {
								setOpen(false);
								onQueue(plan);
							}}
						>
							{m("confirm")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</>
	);
}
