import { useState } from "react";
import { Button, Dialog, Heading, Modal, Tooltip, TooltipTrigger } from "./ui";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import { deletionDialog, deletionFooter } from "./matchingStyles";

export interface DeletionOperation {
	kind?: "library" | "publication" | "artist";
	target: string;
	scope: string;
	publications: number;
	paths: string[];
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
	const plan = deletionPlan(release, track);
	return (
		<>
			{iconOnly ? (
				<TooltipTrigger>
					<Button
						aria-label={triggerLabel ?? "Удалить альбом"}
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
					<Tooltip>Удалить альбом</Tooltip>
				</TooltipTrigger>
			) : (
				<Button onPress={() => setOpen(true)}>
					{track ? "Удалить публикацию" : "Удалить альбом"}
				</Button>
			)}
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="deletion-title" className={deletionDialog}>
					<Heading slot="title" id="deletion-title">
						Удалить: {plan.target}?
					</Heading>
					<p>{plan.scope}</p>
					<p>
						Перед удалением строк будут безопасно удалены только файлы
						публикаций, принадлежащие приложению ({plan.publications}). После
						этого удаляются строки публикаций и указанные сущности медиатеки.
						Записи провайдера сохраняются; чужие/общие файлы не затрагиваются.
					</p>
					{!track && (
						<>
							<h3 className="mt-1.5 mb-0 text-xs">Релиз (будет удалён)</h3>
							<p>{release.title}</p>
							<h3 className="mt-1.5 mb-0 text-xs">Композиции (сохраняются)</h3>
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
								Позиции релиза (будут удалены; включая неопубликованные)
							</h3>
							<ul className="m-0 pl-5">
								{release.tracks.map((item) => (
									<li
										className="my-[3px] [overflow-wrap:anywhere]"
										key={item.id}
									>
										{release.title} · {String(item.position).padStart(2, "0")} ·{" "}
										{item.title} ·{" "}
										{item.published ? "опубликована" : "без публикации"}
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
						<p>Текущих публикаций в этой области нет.</p>
					)}
					<p>
						Асинхронная операция. Прототип только ставит демонстрационный запрос
						в список операций: реальные файлы и демонстрационные записи не
						удаляются.
					</p>
					<div className={deletionFooter}>
						<Button onPress={() => setOpen(false)}>Отмена</Button>
						<Button
							className="border-[var(--error)]! text-[var(--error)]!"
							onPress={() => {
								setOpen(false);
								onQueue(plan);
							}}
						>
							Подтвердить
						</Button>
					</div>
				</Dialog>
			</Modal>
		</>
	);
}
