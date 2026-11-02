import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "react-aria-components";
import {
	originalFiles,
	originalReleases,
	originalArtistName,
	metadataChanged,
	desiredTags,
	type OriginalRelease,
	type SourceFiles,
} from "./catalogData";

export function PublicationPreview({
	selected,
	onBack,
	releases = originalReleases,
	onApply,
	files = originalFiles,
}: {
	selected: Set<string>;
	onBack: () => void;
	releases?: OriginalRelease[];
	onApply?: () => void;
	files?: SourceFiles;
}) {
	const [confirming, setConfirming] = useState(false);
	const rows = releases.flatMap((release) =>
		release.tracks
			.filter((track) => selected.has(track.id))
			.map((track) => {
				const file = track.sourceId ? files[track.sourceId] : null;
				const usable = file?.available && file.locations?.length ? file : null;
				const action = !track.published
					? usable
						? "Создать"
						: "Пропустить"
					: metadataChanged(release, track)
						? "Обновить"
						: "Без изменений";
				return { release, track, file: usable, action };
			}),
	);
	return (
		<section aria-labelledby="preview-title">
			<h1 id="preview-title">Предпросмотр медиатеки</h1>
			<p>
				Показаны существующие позиции выбранных объектов; повторяющиеся треки
				объединены. Новые сущности и альтернативные источники не выбираются.
			</p>
			<button type="button" onClick={onBack}>
				Вернуться в медиатеку
			</button>
			<div className="demo-preview-counts">
				{["Создать", "Обновить", "Без изменений", "Пропустить"].map(
					(action) => (
						<span key={action}>
							{action}: {rows.filter((row) => row.action === action).length}
						</span>
					),
				)}
			</div>
			<div className="demo-preview-table">
				<table>
					<thead>
						<tr>
							<th scope="col">Позиция</th>
							<th scope="col">Источник / формат</th>
							<th scope="col">Путь публикации / формат</th>
							<th scope="col">Действие и причина</th>
							<th scope="col">Снимок метаданных</th>
						</tr>
					</thead>
					<tbody>
						{rows.map(({ release, track, file, action }) => (
							<tr key={track.id}>
								<td>
									{release.title} · {track.position}. {track.title}
								</td>
								<td>
									{file?.path ?? "Нет выбранного источника"}
									<br />
									{file?.codec ?? "—"}
								</td>
								<td>
									{track.published?.path ??
										`/srv/music/${originalArtistName(release.artistId)}/${release.year} - ${release.title}/${String(track.position).padStart(2, "0")} - ${track.title}.mka`}
									<br />
									MKA
								</td>
								<td>
									{action}
									<p>
										{action === "Пропустить"
											? "Нет выбранного источника и текущей публикации; другой источник не выбирается"
											: action === "Без изменений"
												? "Метаданные совпадают со снимком публикации"
												: file
													? "Выбранный источник доступен"
													: "Можно применить к существующей публикации без источника"}
									</p>
									{track.published && (
										<p>
											Звук и качество останутся прежними (
											{track.published.codec}).
										</p>
									)}
								</td>
								<td>
									{desiredTags(release, track).TITLE.join(" · ")}
									<br />
									{release.title}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			<p>
				Для уже опубликованных позиций это действие обновляет только метаданные:
				опубликованный звук и его качество остаются прежними. Для новых
				публикаций используется выбранный исходник.
			</p>
			{onApply && (
				<Button
					onPress={() => setConfirming(true)}
					isDisabled={
						!rows.some(
							(row) => row.action === "Создать" || row.action === "Обновить",
						)
					}
				>
					Опубликовать / обновить
				</Button>
			)}
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog aria-labelledby="apply-preview-title">
					<Heading slot="title" id="apply-preview-title">
						Применить изменения для{" "}
						{
							rows.filter(
								(row) => row.action === "Создать" || row.action === "Обновить",
							).length
						}{" "}
						позиций?
					</Heading>
					<p>
						Новые публикации используют выбранный источник. У существующих
						публикаций меняются только метаданные; звук и качество сохраняются.
						Действие выполняется только в памяти макета, без записи файлов.
					</p>
					<Button onPress={() => setConfirming(false)}>Отмена</Button>
					<Button
						onPress={() => {
							onApply?.();
							setConfirming(false);
						}}
					>
						Подтвердить
					</Button>
				</Dialog>
			</Modal>
		</section>
	);
}
