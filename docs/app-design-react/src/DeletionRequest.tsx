import { useState } from "react";
import "./matching-publication.css";
import { Button, Dialog, Heading, Modal } from "./ui";
import type { OriginalRelease, OriginalTrack } from "./catalogData";

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
}: {
	release: OriginalRelease;
	track?: OriginalTrack;
	onQueue: (operation: DeletionOperation) => void;
}) {
	const [open, setOpen] = useState(false);
	const plan = deletionPlan(release, track);
	return (
		<>
			<Button onPress={() => setOpen(true)}>
				{track ? "Удалить публикацию" : "Удалить альбом"}
			</Button>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog
					aria-labelledby="deletion-title"
					className="deletion-dialog-content"
				>
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
							<h3>Релиз (будет удалён)</h3>
							<p>{release.title}</p>
							<h3>Композиции (сохраняются)</h3>
							<ul>
								{[
									...new Map(
										release.tracks
											.filter((item) => item.recordingId)
											.map((item) => [item.recordingId, item]),
									).values(),
								].map((item) => (
									<li key={item.recordingId}>
										{item.title} · {item.recordingId}
									</li>
								))}
							</ul>
							<h3>Позиции релиза (будут удалены; включая неопубликованные)</h3>
							<ul>
								{release.tracks.map((item) => (
									<li key={item.id}>
										{release.title} · {String(item.position).padStart(2, "0")} ·{" "}
										{item.title} ·{" "}
										{item.published ? "опубликована" : "без публикации"}
									</li>
								))}
							</ul>
						</>
					)}
					{plan.paths.length ? (
						<ul>
							{plan.paths.map((path) => (
								<li key={path}>
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
					<div className="deletion-dialog-footer">
						<Button onPress={() => setOpen(false)}>Отмена</Button>
						<Button
							className="ui-button--danger"
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
