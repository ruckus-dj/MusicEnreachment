import { useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../demoPreferences";
import { LibrarySelectionDeletionView } from "../../../ui/features/library/LibrarySelectionDeletionView";
import { LibrarySelectionDockView } from "../../../ui/features/library/LibrarySelectionDockView";
import { LibrarySelectionReviewView } from "../../../ui/features/library/LibrarySelectionReviewView";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import {
	type DeletionOperation,
	publicationSelectionPlan,
} from "./DeletionRequest";

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
	const [open, setOpen] = useState(false);
	const [reviewOpen, setReviewOpen] = useState(false);
	const reviewTrigger = useRef<HTMLButtonElement>(null);
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
	return createPortal(
		<>
			{selected.size > 0 && (
				<LibrarySelectionDockView
					locale={locale}
					count={selected.size}
					hiddenCount={hiddenCount}
					review={
						visiblePositionIds
							? {
									triggerRef: reviewTrigger,
									onOpen: () => setReviewOpen(true),
								}
							: undefined
					}
					onPreview={() => onPreview([...selected])}
					onDelete={openDeleteConfirmation}
					onClear={() => onSelect([...selected], false)}
				/>
			)}
			<LibrarySelectionReviewView
				isOpen={reviewOpen}
				triggerRef={reviewTrigger}
				rows={selectedTracks.map(({ releaseId, releaseTitle, track }) => ({
					id: `${releaseId}:${track.id}`,
					title: track.title,
					releaseTitle,
				}))}
				onClose={() => setReviewOpen(false)}
				onDeselect={(row) => {
					const selection = selectedTracks.find(
						({ releaseId, track }) => `${releaseId}:${track.id}` === row.id,
					);
					if (selection) onSelect([selection.track.id], false);
				}}
			/>
			<LibrarySelectionDeletionView
				locale={locale}
				open={open}
				onOpenChange={setOpen}
				selected={frozenSelectionCount}
				published={frozen.length}
				rows={currentPublications.map(({ releaseId, track, path }) => ({
					id: `${releaseId}:${track.id}`,
					release: releases.find((release) => release.id === releaseId)?.title,
					position: track.position,
					title: track.title,
					path,
				}))}
				simulated
				onConfirm={() => {
					const confirmed = currentPublications.flatMap(({ track, path }) =>
						track.published?.path === path ? [track] : [],
					);
					if (!confirmed.length) return;
					onQueueDeletion(publicationSelectionPlan(confirmed, releases));
					setOpen(false);
				}}
			/>
		</>,
		document.body,
	);
}
