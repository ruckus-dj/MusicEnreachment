import { useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import { catalogueStyles } from "./catalogueStyles";
import {
	type DeletionOperation,
	publicationSelectionPlan,
} from "./DeletionRequest";
import { useI18n } from "./i18n/I18nProvider";
import {
	type LibraryRowsMessage,
	libraryRowsMessage,
} from "./libraryRowsMessages";
import { Button, Dialog, Heading, Modal } from "./ui";

type FrozenPublication = {
	releaseId: string;
	track: OriginalTrack;
	path: string;
};

export function SelectionDock({
	selected,
	visiblePositionIds,
	releases,
	onSelect,
	onPreview,
	onQueueDeletion,
}: {
	selected: Set<string>;
	visiblePositionIds?: ReadonlySet<string>;
	releases: OriginalRelease[];
	onSelect: (ids: string[], value: boolean) => void;
	onPreview: (ids: string[]) => void;
	onQueueDeletion: (operation: DeletionOperation) => void;
}) {
	const { locale } = useI18n();
	const m = (
		key: LibraryRowsMessage,
		values?: Record<string, string | number>,
	) => libraryRowsMessage(locale, key, values);
	const [open, setOpen] = useState(false);
	const [reviewOpen, setReviewOpen] = useState(false);
	const reviewClose = useRef<HTMLButtonElement>(null);
	const [frozen, setFrozen] = useState<FrozenPublication[]>([]);
	const [frozenSelectionCount, setFrozenSelectionCount] = useState(0);
	const selectedTracks = releases.flatMap((release) =>
		release.tracks
			.filter((track) => selected.has(track.id))
			.map((track) => ({
				releaseId: release.id,
				releaseTitle: release.title,
				track,
			})),
	);
	const hiddenCount = visiblePositionIds
		? [...selected].filter((id) => !visiblePositionIds.has(id)).length
		: 0;
	const currentPublications = useMemo(
		() =>
			frozen.flatMap(({ releaseId, track, path }) => {
				const current = releases
					.find((release) => release.id === releaseId)
					?.tracks.find((candidate) => candidate.id === track.id);
				return current?.published?.path === path
					? [{ releaseId, track: current, path }]
					: [];
			}),
		[frozen, releases],
	);

	if (typeof document === "undefined") return null;
	const openDeleteConfirmation = () => {
		setFrozenSelectionCount(selectedTracks.length);
		setFrozen(
			selectedTracks.flatMap(({ releaseId, track }) =>
				track.published
					? [{ releaseId, track, path: track.published.path }]
					: [],
			),
		);
		setOpen(true);
	};
	const skippedUnpublished = frozenSelectionCount - frozen.length;
	const changeReviewOpen = (nextOpen: boolean) => {
		setReviewOpen(nextOpen);
		if (!nextOpen && selected.size === 0) {
			requestAnimationFrame(() =>
				document.querySelector<HTMLElement>("#content h1")?.focus(),
			);
		}
	};
	return createPortal(
		<>
			{selected.size > 0 && (
				<section
					className={catalogueStyles.dock}
					aria-label={m("selectSelectedPositions")}
				>
					<span role="status">
						{m("selectedPositionsCount", { count: selected.size })}
						{hiddenCount > 0 &&
							` · ${m("hiddenSelectedPositions", { count: hiddenCount })}`}
					</span>
					<div className={catalogueStyles.dockActions}>
						{visiblePositionIds && (
							<Button onPress={() => setReviewOpen(true)}>
								{m("reviewSelection")}
							</Button>
						)}
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							onPress={() => onPreview([...selected])}
						>
							{visiblePositionIds ? m("previewSelection") : m("publish")}
						</Button>
						<Button onPress={openDeleteConfirmation}>
							{m("deletePublications")}
						</Button>
						<Button onPress={() => onSelect([...selected], false)}>
							{m("clearSelection")}
						</Button>
					</div>
				</section>
			)}
			<Modal isOpen={reviewOpen} onOpenChange={changeReviewOpen} isDismissable>
				<Dialog
					aria-labelledby="library-selection-title"
					className={catalogueStyles.dockDialog}
				>
					<Heading slot="title" id="library-selection-title">
						{m("reviewSelection")}
					</Heading>
					{selectedTracks.length ? (
						<ul className="my-3 max-h-[52dvh] list-none overflow-auto p-0">
							{selectedTracks.map(({ releaseId, releaseTitle, track }) => (
								<li
									key={`${releaseId}:${track.id}`}
									className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] py-2"
								>
									<span className="min-w-0 wrap-anywhere">
										<strong>{track.title}</strong> · {releaseTitle}
									</span>
									<Button
										aria-label={`${m("deselectPosition")}: ${track.title} · ${releaseTitle}`}
										onPress={(event) => {
											const row = event.target.closest("li");
											const adjacent =
												row?.nextElementSibling ?? row?.previousElementSibling;
											(
												adjacent?.querySelector("button") ?? reviewClose.current
											)?.focus();
											onSelect([track.id], false);
										}}
									>
										{m("deselectPosition")}
									</Button>
								</li>
							))}
						</ul>
					) : (
						<p>{m("emptySelection")}</p>
					)}
					<div className={catalogueStyles.dockDialogFooter}>
						<Button ref={reviewClose} onPress={() => changeReviewOpen(false)}>
							{m("closeSelection")}
						</Button>
					</div>
				</Dialog>
			</Modal>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog
					aria-labelledby="selection-deletion-title"
					className={catalogueStyles.dockDialog}
				>
					<Heading slot="title" id="selection-deletion-title">
						{m("deleteSelectedPublicationsQuestion")}
					</Heading>
					<p>
						{m("frozenDeletionCounts", {
							selected: frozenSelectionCount,
							published: frozen.length,
							skipped: skippedUnpublished,
						})}
					</p>
					<p>{m("selectedPublicationScope")}</p>
					{frozen.length > currentPublications.length && (
						<p role="status">{m("changedPublications")}</p>
					)}
					{currentPublications.length ? (
						<ul>
							{currentPublications.map(({ releaseId, track, path }) => (
								<li key={`${releaseId}:${track.id}`}>
									{releases.find((release) => release.id === releaseId)?.title}{" "}
									· {String(track.position).padStart(2, "0")} · {track.title} ·{" "}
									<code>{path}</code>
								</li>
							))}
						</ul>
					) : (
						<p>{m("noPublicationsToDelete")}</p>
					)}
					<p>{m("deletionDemoNotice")}</p>
					<div className={catalogueStyles.dockDialogFooter}>
						<Button onPress={() => setOpen(false)}>{m("cancel")}</Button>
						<Button
							className="border-[var(--error)]! bg-[var(--error)]! text-white!"
							isDisabled={!currentPublications.length}
							onPress={() => {
								const confirmed = currentPublications.flatMap(
									({ track, path }) =>
										track.published?.path === path ? [track] : [],
								);
								if (!confirmed.length) return;
								onQueueDeletion(publicationSelectionPlan(confirmed, releases));
								setOpen(false);
							}}
						>
							{m("confirmDeletion")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</>,
		document.body,
	);
}
