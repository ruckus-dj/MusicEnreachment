import { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import {
	type DeletionOperation,
	publicationSelectionPlan,
} from "./DeletionRequest";
import { Button, Dialog, Heading, Modal } from "./ui";

type FrozenPublication = {
	releaseId: string;
	track: OriginalTrack;
	path: string;
};

export function SelectionDock({
	selected,
	releases,
	onSelect,
	onPreview,
	onQueueDeletion,
}: {
	selected: Set<string>;
	releases: OriginalRelease[];
	onSelect: (ids: string[], value: boolean) => void;
	onPreview: (ids: string[]) => void;
	onQueueDeletion: (operation: DeletionOperation) => void;
}) {
	const [open, setOpen] = useState(false);
	const [frozen, setFrozen] = useState<FrozenPublication[]>([]);
	const [frozenSelectionCount, setFrozenSelectionCount] = useState(0);
	const selectedTracks = releases.flatMap((release) =>
		release.tracks
			.filter((track) => selected.has(track.id))
			.map((track) => ({ releaseId: release.id, track })),
	);
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
	return createPortal(
		<>
			{selected.size > 0 && (
				<div
					className="selection-dock"
					role="region"
					aria-label="Выбранные позиции"
				>
					<span role="status">Выбрано позиций: {selected.size}</span>
					<div className="selection-dock-actions">
						<Button
							className="ui-button--primary"
							onPress={() => onPreview([...selected])}
						>
							Опубликовать
						</Button>
						<Button onPress={openDeleteConfirmation}>Удалить публикации</Button>
						<Button onPress={() => onSelect([...selected], false)}>
							Снять выбор
						</Button>
					</div>
				</div>
			)}
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog
					aria-labelledby="selection-deletion-title"
					className="deletion-dialog-content"
				>
					<Heading slot="title" id="selection-deletion-title">
						Удалить выбранные публикации?
					</Heading>
					<p>
						Выбрано позиций: {frozenSelectionCount}. Текущих публикаций в
						зафиксированном выборе: {frozen.length}. Без публикации пропущено:{" "}
						{skippedUnpublished}.
					</p>
					<p>
						Будут затронуты только перечисленные текущие публикации. Позиции,
						альбомы, композиции, исходники и записи провайдера не удаляются.
					</p>
					{frozen.length > currentPublications.length && (
						<p role="status">
							Данные публикаций изменились после открытия подтверждения;
							изменённые публикации будут пропущены.
						</p>
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
						<p>Текущих публикаций для удаления нет.</p>
					)}
					<p>
						Асинхронная операция. Прототип только ставит демонстрационный запрос
						в список операций: реальные файлы и демонстрационные записи не
						удаляются.
					</p>
					<div className="deletion-dialog-footer">
						<Button onPress={() => setOpen(false)}>Отмена</Button>
						<Button
							className="ui-button--danger"
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
							Подтвердить удаление
						</Button>
					</div>
				</Dialog>
			</Modal>
		</>,
		document.body,
	);
}
