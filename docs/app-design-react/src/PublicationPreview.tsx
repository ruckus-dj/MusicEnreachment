import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "./ui";
import {
	dialogFooter,
	previewTable,
	previewTableCell,
	previewTableHeading,
	previewTableWrap,
} from "./matchingStyles";
import { artistReleaseHref } from "./artistReleaseRoute";
import {
	catalogData,
	effectivePublicationTags,
	originalFiles,
	originalReleases,
	originalArtistName,
	metadataChanged,
	publicationMetadataIssues,
	type OriginalRelease,
	type SourceFiles,
	type CatalogArtist,
} from "./catalogData";
import {
	isMetadataPreviewFresh,
	type MetadataPreviewToken,
} from "./previewModel";
import {
	MetadataDiff,
	metadataSnapshotDiff,
	publicationMetadataDiff,
} from "./publicationMetadataDiff";

import {
	choosePublicationOutput,
	fixturePublicationDestination,
} from "./publicationOutput";

export function PublicationPreview({
	destination = fixturePublicationDestination,
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
	destination?: import("./publicationOutput").PublicationDestination;
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
	const availableArtists = artists ?? catalogData.artists;
	const fresh = isMetadataPreviewFresh(token, inventoryVersion, scopeKey);
	const stale = !fresh || parentRejected;
	const rows = releases.flatMap((release) =>
		release.tracks
			.filter((track) => token.selectedIds.includes(track.id))
			.map((track) => {
				const file = track.sourceId ? files[track.sourceId] : null;
				const usable = file?.available && file.locations?.length ? file : null;
				const metadataIssues = publicationMetadataIssues(release, track);
				const action = metadataIssues.length
					? "Невозможно обработать"
					: track.published
						? metadataChanged(release, track, artists)
							? "Обновить"
							: "Без изменений"
						: usable
							? "Создать"
							: "Пропустить";
				const reason =
					action === "Невозможно обработать"
						? `Некорректные метаданные: ${metadataIssues.join("; ")}`
						: action === "Создать"
							? "Создание из выбранного доступного источника"
							: action === "Обновить"
								? "Обновятся эффективные метаданные; опубликованный звук сохраняется"
								: action === "Без изменений"
									? "Эффективные метаданные совпадают со снимком публикации"
									: !track.sourceId
										? "Файл не выбран; другой источник не выбирается"
										: "Выбранный файл недоступен для этой операции; другой источник не выбирается";
				return { release, track, file: usable, action, reason };
			}),
	);
	const actions = [
		"Создать",
		"Обновить",
		"Без изменений",
		"Невозможно обработать",
		"Пропустить",
	] as const;
	const returnLink = `#${returnRoute}`;
	return (
		<section
			className="min-w-0 px-[clamp(14px,2vw,28px)] py-5"
			aria-labelledby="preview-title"
		>
			<header className="mb-3.5 flex flex-wrap items-start justify-between gap-3">
				<div>
					<h1 id="preview-title" className="m-0 text-2xl tracking-[-.02em]">
						Предпросмотр публикации
					</h1>
					<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
						Показаны существующие позиции выбранных объектов; повторяющиеся
						треки объединены. Новые сущности и альтернативные источники не
						выбираются.
					</p>
				</div>
				<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
					Контекст предпросмотра: {returnLabel}.{" "}
					<a href={returnLink}>{returnLabel}</a>
				</p>
				<Button onPress={onBack}>{returnLabel}</Button>
			</header>
			{stale && (
				<p role="alert">
					Снимок предпросмотра устарел: изменился инвентарь источников или
					контекст. Вернитесь и откройте предпросмотр заново.
				</p>
			)}
			<div
				className="my-2.5 mb-3.5 flex flex-wrap gap-2"
				aria-label="Сводка действий"
			>
				{actions.map((action) => (
					<span
						key={action}
						className="rounded border border-[var(--line)] bg-[var(--surface)] px-[9px] py-[5px] text-[11px]"
					>
						{action}: {rows.filter((row) => row.action === action).length}
					</span>
				))}
			</div>
			<div className={previewTableWrap}>
				<table className={`${previewTable} min-w-[850px]`}>
					<thead>
						<tr>
							<th className={previewTableHeading} scope="col">
								Позиция
							</th>
							<th className={previewTableHeading} scope="col">
								Источник / формат
							</th>
							<th className={previewTableHeading} scope="col">
								Путь публикации / формат
							</th>
							<th className={previewTableHeading} scope="col">
								Действие и причина
							</th>
							<th className={previewTableHeading} scope="col">
								Снимок метаданных
							</th>
						</tr>
					</thead>
					<tbody>
						{rows.map(({ release, track, file, action, reason }) => (
							<tr key={track.id}>
								<td className={`${previewTableCell} min-w-[175px]`}>
									{artistReleaseHref(release, availableArtists) ? (
										<a
											href={
												artistReleaseHref(release, availableArtists) ??
												undefined
											}
										>
											{release.title}
										</a>
									) : (
										release.title
									)}{" "}
									·{" "}
									<a href={`#track/${track.id}`}>
										{track.position}. {track.title}
									</a>
								</td>
								<td className={`${previewTableCell} min-w-[190px]`}>
									{file?.path ??
										(track.sourceId
											? "Выбранный источник недоступен"
											: "Источник не выбран")}
									<br />
									{file?.codec ?? "—"}
								</td>
								<td className={`${previewTableCell} min-w-[190px]`}>
									{track.published?.path ??
										(file
											? choosePublicationOutput(file, {
													...destination,
													artist: originalArtistName(
														release.artistId,
														availableArtists,
													),
													year: release.year,
													release: release.title,
													position: track.position,
													title: track.title,
												}).path
											: "Путь не определён: источник недоступен")}
									<br />
									{track.published?.container ??
										(file
											? choosePublicationOutput(file, {
													...destination,
													artist: originalArtistName(
														release.artistId,
														availableArtists,
													),
													year: release.year,
													release: release.title,
													position: track.position,
													title: track.title,
												}).container
											: "—")}
								</td>
								<td className={previewTableCell}>
									{action}
									<p>{reason}</p>
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
								<td className={`${previewTableCell} min-w-[230px]`}>
									{track.published ? (
										<MetadataDiff
											groups={publicationMetadataDiff(
												release,
												track,
												availableArtists,
											)}
										/>
									) : (
										<MetadataDiff
											groups={metadataSnapshotDiff(
												{},
												effectivePublicationTags(
													release,
													track,
													availableArtists,
												),
												{ coverHash: null, lyrics: null },
												{
													coverHash: track.coverHash ?? null,
													lyrics: track.lyrics ?? null,
												},
											)}
										/>
									)}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			<div className="mt-3.5 flex flex-wrap items-center justify-between gap-2.5 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] p-3 max-[600px]:items-stretch">
				<p>
					<strong>
						{
							rows.filter((row) => row.action === "Невозможно обработать")
								.length
						}{" "}
						позиций невозможно обработать.
					</strong>{" "}
					Для уже опубликованных позиций обновляются эффективные метаданные;
					опубликованный звук сохраняется. Для новых публикаций используется
					выбранный исходник. Пропущенные позиции не входят в эту выборку.
				</p>
				<div className="flex flex-wrap justify-end gap-2">
					{onApply && (
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							onPress={() => setConfirming(true)}
							isDisabled={
								stale ||
								!rows.some(
									(row) =>
										row.action === "Создать" || row.action === "Обновить",
								)
							}
						>
							Создать {rows.filter((row) => row.action === "Создать").length} ·
							обновить метаданные{" "}
							{rows.filter((row) => row.action === "Обновить").length}
						</Button>
					)}
				</div>
			</div>
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog aria-labelledby="apply-preview-title" className="grid gap-2">
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
					<p className="my-0 min-w-[240px] flex-1 text-[11px] text-[var(--muted)]">
						Новые публикации используют выбранный источник. У существующих
						публикаций меняются только метаданные; звук и качество сохраняются.
						Действие выполняется только в памяти макета, без записи файлов.
					</p>
					<div className={`${dialogFooter} flex-wrap`}>
						<Button onPress={() => setConfirming(false)}>Отмена</Button>
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							isDisabled={stale}
							onPress={() => {
								if (
									!isMetadataPreviewFresh(token, inventoryVersion, scopeKey)
								) {
									setConfirming(false);
									return;
								}
								const applicableToken = {
									...token,
									selectedIds: rows
										.filter(
											(row) =>
												row.action === "Создать" || row.action === "Обновить",
										)
										.map((row) => row.track.id),
								};
								if (onApply && !onApply(applicableToken)) {
									setParentRejected(true);
									setConfirming(false);
									return;
								}
								setConfirming(false);
							}}
						>
							Подтвердить
						</Button>
					</div>
				</Dialog>
			</Modal>
		</section>
	);
}
