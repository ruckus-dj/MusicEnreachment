import { useState } from "react";
import { DeletionRequest, type DeletionOperation } from "./DeletionRequest";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Modal,
} from "react-aria-components";
import {
	originalFiles,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";
import {
	explicitlyRepublish,
	refreshProviderHint,
	renameLocalTrack,
	upgradeAudioOnly,
} from "./trackActionModel";

export function TrackActions({
	release,
	track,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
}) {
	const [action, setAction] = useState<
		"rename" | "upgrade" | "republish" | null
	>(null);
	const [title, setTitle] = useState(track.title);
	const [downgrade, setDowngrade] = useState(false);
	const [notice, setNotice] = useState("");
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const downgradeRequired =
		track.published?.codec === "FLAC" && file?.codec === "MP3";
	const open = (next: typeof action) => {
		setTitle(track.title);
		setDowngrade(false);
		setNotice("");
		setAction(next);
	};
	return (
		<>
			<details>
				<summary>Другие действия</summary>
				<div className="demo-object-actions">
					<Button onPress={() => open("rename")}>
						Изменить локальное название
					</Button>
					<Button
						onPress={() => {
							onMutate(refreshProviderHint);
							setNotice(
								"Кэш провайдера обновлён отдельно. Публикация и снимок метаданных не изменились.",
							);
						}}
					>
						Обновить кэш провайдера
					</Button>
					{track.published?.codec === "MP3" && file?.codec === "FLAC" && (
						<Button onPress={() => open("upgrade")}>
							Демо: улучшить только качество аудио
						</Button>
					)}
					{track.published && (
						<Button
							onPress={() => {
								if (file) open("republish");
								else
									setNotice(
										"Для явной перепубликации нужно выбрать исходник. Альтернативный вариант автоматически не выбирается.",
									);
							}}
						>
							Перепубликовать явно
						</Button>
					)}
				</div>
				{track.published && onQueueDeletion && (
					<DeletionRequest
						release={release}
						track={track}
						onQueue={onQueueDeletion}
					/>
				)}
			</details>
			{notice && <p role="status">{notice}</p>}
			<Modal
				isOpen={action !== null}
				onOpenChange={(open) => {
					if (!open) setAction(null);
				}}
				isDismissable
			>
				<Dialog aria-labelledby="track-action-title">
					{notice && <p role="alert">{notice}</p>}
					<form
						onSubmit={(event) => {
							event.preventDefault();
							if (action === "republish" && downgradeRequired && !downgrade) {
								setNotice("Понижение отменено: требуется явное подтверждение.");
								document
									.querySelector<HTMLElement>(".demo-downgrade input")
									?.focus();
								return;
							}
							if (action === "rename") {
								onMutate((current) => renameLocalTrack(current, title));
								setNotice(
									"Локальное название изменено. Сравните с текущей публикацией; её теги ещё прежние.",
								);
							} else if (action === "upgrade") {
								onMutate((current) => upgradeAudioOnly(current, files));
								setNotice(
									"Демо: изменилось только аудио. Теги, обложка, текст песни и прежний снимок метаданных публикации сохранены.",
								);
							} else if (action === "republish") {
								onMutate((current) =>
									explicitlyRepublish(release, current, downgrade, files),
								);
								setNotice(
									"Демо: явная перепубликация выполнена; текущее состояние и снимок метаданных заменены.",
								);
							}
							setAction(null);
						}}
					>
						<Heading slot="title" id="track-action-title">
							{action === "rename"
								? "Изменить только локальные теги"
								: action === "upgrade"
									? "Улучшить только аудио?"
									: "Явно перепубликовать из выбранного исходника?"}
						</Heading>
						{action === "rename" ? (
							<>
								<label className="demo-search">
									Название позиции
									<input
										value={title}
										onChange={(event) => setTitle(event.currentTarget.value)}
										required
									/>
								</label>
								<p>
									Исходник, кэш провайдера и текущая публикация останутся
									прежними.
								</p>
							</>
						) : (
							<>
								<p>
									{track.title} · {file?.path} · {file?.codec}
								</p>
								<p>
									{action === "upgrade"
										? "Метаданные, встроенная обложка и текст песни текущей публикации сохраняются без изменений."
										: "Полная подготовка и замена выполняются, даже если данные не изменились. Это отдельное действие, а не обычный повтор."}
								</p>
								<p>
									Демонстрация: файловая подготовка и запись не выполняются.
								</p>
							</>
						)}
						{action === "republish" && downgradeRequired && (
							<Checkbox
								isSelected={downgrade}
								onChange={setDowngrade}
								className="demo-downgrade"
							>
								<span aria-hidden="true">{downgrade ? "☑" : "☐"}</span>{" "}
								Понижение качества подтверждаю
							</Checkbox>
						)}
						<Button type="button" onPress={() => setAction(null)}>
							Отмена
						</Button>
						<Button type="submit">Подтвердить</Button>
					</form>
				</Dialog>
			</Modal>
		</>
	);
}
