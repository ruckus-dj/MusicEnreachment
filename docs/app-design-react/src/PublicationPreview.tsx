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
	type CatalogArtist,
} from "./catalogData";
import {
	isMetadataPreviewFresh,
	type MetadataPreviewToken,
} from "./previewModel";

export function PublicationPreview({
	token,
	inventoryVersion,
	scopeKey,
	onBack,
	releases = originalReleases,
	onApply,
	files = originalFiles,
	artists,
	returnRoute = "library",
	returnLabel = "Вернуться в медиатеку",
}: {
	token: MetadataPreviewToken;
	inventoryVersion: number;
	scopeKey: string;
	onBack: () => void;
	releases?: OriginalRelease[];
	onApply?: (token: MetadataPreviewToken) => boolean;
	files?: SourceFiles;
	artists?: CatalogArtist[];
	returnRoute?: string;
	returnLabel?: string;
}) {
	const [confirming, setConfirming] = useState(false);
	const [parentRejected, setParentRejected] = useState(false);
	const fresh = isMetadataPreviewFresh(token, inventoryVersion, scopeKey);
	const stale = !fresh || parentRejected;
	const rows = releases.flatMap((release) =>
		release.tracks
			.filter((track) => token.selectedIds.includes(track.id))
			.map((track) => {
				const file = track.sourceId ? files[track.sourceId] : null;
				const usable = file?.available && file.locations?.length ? file : null;
				const action = !track.published
					? usable
						? "Создать"
						: "Пропустить"
					: metadataChanged(release, track, artists)
						? "Обновить"
						: "Без изменений";
				return { release, track, file: usable, action };
			}),
	);
	const unprocessable = rows.filter(
		(row) => !row.file && !row.track.published,
	).length;
	const returnLink = `#${returnRoute}`;
	return (
		<section aria-labelledby="preview-title">
			<h1 id="preview-title">Предпросмотр медиатеки</h1>
			<p>
				Показаны существующие позиции выбранных объектов; повторяющиеся треки
				объединены. Новые сущности и альтернативные источники не выбираются.
			</p>
			<p>
				Контекст предпросмотра: {returnLabel}.{" "}
				<a href={returnLink}>{returnLabel}</a>
			</p>
			<button type="button" onClick={onBack}>
				{returnLabel}
			</button>
			{stale && (
				<p role="alert">
					Снимок предпросмотра устарел: изменился инвентарь источников или
					контекст. Вернитесь и откройте предпросмотр заново.
				</p>
			)}
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
									<a href={`#release/${release.id}`}>{release.title}</a> ·{" "}
									<a href={`#track/${track.id}`}>
										{track.position}. {track.title}
									</a>
								</td>
								<td>
									{file?.path ?? "Нет выбранного источника"}
									<br />
									{file?.codec ?? "—"}
								</td>
								<td>
									{track.published?.path ??
										`/srv/music/${originalArtistName(release.artistId, artists)}/${release.year} - ${release.title}/${String(track.position).padStart(2, "0")} - ${track.title}.mka`}
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
									{track.published &&
										file &&
										file.codec !== track.published.codec && (
											<p>
												Выбранный источник {file.codec} в этом действии не
												применяется.
											</p>
										)}
									{!track.published && file && (
										<p>
											Новая публикация из выбранного источника ({file.codec}).
										</p>
									)}
								</td>
								<td>
									{desiredTags(release, track, artists).TITLE.join(" · ")}
									<br />
									{release.title}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			<p>{unprocessable} не обрабатываемых позиций.</p>
			<p>
				Для уже опубликованных позиций это действие обновляет только метаданные:
				опубликованный звук и его качество остаются прежними. Для новых
				публикаций используется выбранный исходник.
			</p>
			{onApply && (
				<Button
					onPress={() => setConfirming(true)}
					isDisabled={
						stale ||
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
					{stale && (
						<p role="alert">
							Снимок предпросмотра устарел: изменился инвентарь источников или
							контекст. Вернитесь и откройте предпросмотр заново.
						</p>
					)}
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
						isDisabled={stale}
						onPress={() => {
							if (!isMetadataPreviewFresh(token, inventoryVersion, scopeKey)) {
								setConfirming(false);
								return;
							}
							if (onApply && !onApply(token)) {
								setParentRejected(true);
								setConfirming(false);
								return;
							}
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
