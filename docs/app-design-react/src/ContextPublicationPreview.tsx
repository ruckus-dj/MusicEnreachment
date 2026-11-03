import { useState } from "react";
import { Button, Checkbox, Dialog, Heading, Modal } from "./ui";
import {
	publicationMetadataIssues,
	type CatalogArtist,
	type OriginalRelease,
} from "./catalogData";
import {
	applyPlan,
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
	planRows,
	selectedSourceId,
	sourceIds,
	usableFile,
} from "./incomingModel";
import { MetadataDiff, metadataSnapshotDiff } from "./publicationMetadataDiff";
import {
	choosePublicationOutput,
	fixturePublicationDestination,
} from "./publicationOutput";
import { originalArtistName } from "./catalogData";
import {
	dialogFooter,
	objectActions,
	previewTable,
	previewTableWrap,
} from "./matchingStyles";

export function ContextPublicationPreview({
	destination = fixturePublicationDestination,
	plan,
	files,
	releases,
	providers,
	artists,
	inventoryVersion,
	confirmDowngrade,
	onPlan,
	onReleases,
	onSuccess,
	onBack,
}: {
	destination?: import("./publicationOutput").PublicationDestination;
	plan: IncomingPlan | null;
	files: IncomingFiles;
	releases: IncomingReleases;
	providers: IncomingReleases;
	artists: CatalogArtist[];
	inventoryVersion: number;
	confirmDowngrade: boolean;
	onPlan: (plan: IncomingPlan | null, confirmDowngrade?: boolean) => void;
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: string) => void;
	onBack: () => void;
}) {
	const [error, setError] = useState("");
	const [confirming, setConfirming] = useState(false);
	if (!plan)
		return (
			<section>
				<h1>Предпросмотр публикации</h1>
				<p>Нет сохранённого плана публикации. Откройте издание или позицию.</p>
				<a href="#library">К медиатеке</a>
			</section>
		);

	const rows = planRows(plan, files, releases, providers, artists);
	const existing = releases.find(
		(release) => release.id === plan.localReleaseId,
	);
	const classified = rows.map((row) => {
		if (!row.assignment?.included)
			return {
				row,
				action: "Пропустить",
				reason: "Позиция исключена из плана",
			};
		const sourceId = selectedSourceId(row.assignment);
		const metadataRelease =
			plan.mode === "local" ? (existing ?? plan.target) : plan.target;
		const metadataTrack = row.track ?? row.targetTrack;
		const metadataIssues = publicationMetadataIssues(
			metadataRelease,
			metadataTrack,
			sourceId,
		);
		if (metadataIssues.length)
			return {
				row,
				action: "Невозможно обработать",
				reason: `Некорректные метаданные: ${metadataIssues.join("; ")}`,
			};
		if (!row.file) {
			const reason = !sourceId
				? "Файл не выбран"
				: "Выбранный файл недоступен для этой операции";
			return { row, action: "Пропустить", reason };
		}
		return {
			row,
			action:
				row.action === "Без изменений"
					? "Без изменений"
					: row.previous
						? "Обновить"
						: "Создать",
			reason:
				row.action === "Без изменений"
					? "Источник и эффективные теги совпадают с публикацией"
					: row.previous
						? "Будут обновлены выбранные метаданные и аудио"
						: "Новая публикация из выбранного источника",
		};
	});
	const changed = classified.filter(
		(item) => item.action === "Создать" || item.action === "Обновить",
	);
	const downgrades = classified.filter(
		(item) => item.row.downgrade && item.action === "Обновить",
	);
	const counts = [
		"Создать",
		"Обновить",
		"Без изменений",
		"Невозможно обработать",
		"Пропустить",
	].map((action) => ({
		action,
		count: classified.filter((item) => item.action === action).length,
	}));
	const stale = plan.inventoryVersion !== inventoryVersion;
	const links = rows.filter((row) => {
		const status = classified.find(
			(item) => item.row.targetTrack.id === row.targetTrack.id,
		)?.action;
		if (status === "Пропустить" || status === "Невозможно обработать")
			return false;
		const assigned = sourceIds(row.assignment).filter((id) =>
			usableFile(files[id]),
		);
		const track = existing?.tracks.find(
			(item) => item.position === row.targetTrack.position,
		);
		const persisted = new Set([
			...(track?.sourceIds ?? []),
			...(track?.sourceId ? [track.sourceId] : []),
		]);
		return assigned.some((id) => !persisted.has(id));
	});
	const canConfirm =
		!stale &&
		(changed.length > 0 || links.length > 0) &&
		(!downgrades.length || confirmDowngrade);
	const updateIncluded = (trackId: string, included: boolean) => {
		const assignment = plan.assignments[trackId];
		if (!assignment) return;
		onPlan({
			...plan,
			assignments: {
				...plan.assignments,
				[trackId]: { ...assignment, included },
			},
		});
	};
	const confirm = () => {
		if (plan.inventoryVersion !== inventoryVersion) {
			setError(
				"Инвентарь источников изменился. Создайте новый предпросмотр перед подтверждением.",
			);
			return;
		}
		if (!canConfirm) return;
		const next = structuredClone(releases);
		const applicableIDs = new Set(
			classified
				.filter(
					(item) =>
						item.action !== "Пропустить" &&
						item.action !== "Невозможно обработать",
				)
				.map((item) => item.row.targetTrack.id),
		);
		const applicablePlan: IncomingPlan = {
			...plan,
			assignments: Object.fromEntries(
				Object.entries(plan.assignments).filter(([trackId]) =>
					applicableIDs.has(trackId),
				),
			),
		};
		const applied = applyPlan(
			applicablePlan,
			files,
			next,
			providers,
			false,
			artists,
			destination,
		);
		onReleases(next);
		onPlan(null, false);
		onSuccess(
			`Демо: ${applied} позиций опубликовано.${links.length ? ` Связи ${links.length} позиций сохранены.` : ""} Невыбранные позиции и неразмеченные файлы не изменены.`,
		);
		setConfirming(false);
	};

	return (
		<section
			className="min-w-0 px-[clamp(14px,2vw,28px)] py-5"
			aria-labelledby="context-publication-title"
		>
			<header className="demo-object-header mb-3 flex flex-wrap items-center justify-between gap-3">
				<div className="min-w-0 [overflow-wrap:anywhere]">
					<h1 id="context-publication-title">
						Предпросмотр: {plan.target.title}
					</h1>
					<p className="mt-[5px] max-w-[760px] text-[var(--muted)]">
						Контекстный предпросмотр операции. Это не отдельный список
						публикаций.
					</p>
				</div>
				<Button onPress={onBack}>Вернуться к объекту</Button>
			</header>
			{(stale || error) && (
				<p role="alert">
					{error ||
						"Инвентарь источников изменился. Создайте новый предпросмотр перед подтверждением."}
				</p>
			)}
			<p
				className="demo-preview-counts flex flex-wrap gap-2 py-3"
				aria-label="Сводка действий"
			>
				{counts.map(({ action, count }) => (
					<span
						key={action}
						className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]"
					>
						{action}: {count}
					</span>
				))}
				<span className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]">
					MKA · перепаковка без перекодирования
				</span>
			</p>
			<div className={previewTableWrap}>
				<table className={previewTable}>
					<thead>
						<tr>
							<th>Включить</th>
							<th>№ / трек</th>
							<th>Источник</th>
							<th>Сейчас опубликовано</th>
							<th>Будет записано</th>
							<th>Действие</th>
						</tr>
					</thead>
					<tbody>
						{classified.map(({ row, action, reason }) => (
							<tr key={row.targetTrack.id}>
								<td>
									<Checkbox
										aria-label={`Включить ${row.targetTrack.title}`}
										isSelected={Boolean(row.assignment?.included && row.file)}
										isDisabled={!row.file}
										onChange={(checked) =>
											updateIncluded(String(row.targetTrack.id), checked)
										}
									>
										<span className="checkbox" />
									</Checkbox>
								</td>
								<td>
									{row.targetTrack.position} · {row.targetTrack.title}
								</td>
								<td>
									{row.file
										? `${row.file.name} · ${row.file.codec}`
										: selectedSourceId(row.assignment)
											? "Выбранный источник недоступен"
											: "Источник не выбран"}
								</td>
								<td>
									{row.previous
										? `${row.previous.codec} · ${row.previous.tags.TITLE?.join(" · ") ?? ""}`
										: "Нет публикации"}
								</td>
								<td>
									{row.nextTags.TITLE?.join(" · ") ?? "—"}
									<br />
									{row.nextTags.ALBUM?.join(" · ") ?? "—"}
									{row.file && (
										<p className="text-xs wrap-anywhere">
											<code>
												{row.previous?.path ??
													choosePublicationOutput(row.file, {
														...destination,
														artist: originalArtistName(
															plan.target.artistId,
															artists,
														),
														year: plan.target.year,
														release: plan.target.title,
														position: row.targetTrack.position,
														title: row.targetTrack.title,
													}).path}
											</code>
										</p>
									)}
								</td>
								<td>
									<strong>{action}</strong>
									<p>{reason}</p>
									{row.downgrade && row.assignment?.included
										? " · Понижение качества"
										: ""}
									<MetadataDiff
										groups={metadataSnapshotDiff(
											row.previous?.tags ?? {},
											row.nextTags,
											{
												coverHash: row.previous?.coverHash ?? null,
												lyrics: row.previous?.lyrics ?? null,
											},
											{
												coverHash:
													(row.track ?? row.targetTrack).coverHash ?? null,
												lyrics: (row.track ?? row.targetTrack).lyrics ?? null,
											},
										)}
									/>
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			<p>
				<strong>Исходники сохранятся.</strong> Не включённые позиции и их
				текущие публикации не изменятся. Файлы без назначения не удаляются.
			</p>
			{downgrades.length > 0 && (
				<Checkbox
					isSelected={confirmDowngrade}
					onChange={(checked) => onPlan(plan, checked)}
				>
					<span className="checkbox" />
					Подтверждаю отдельное понижение качества ({downgrades.length} поз.)
				</Checkbox>
			)}
			<div className={objectActions}>
				<Button
					className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
					isDisabled={!canConfirm}
					onPress={() => setConfirming(true)}
				>
					Подтвердить публикацию
				</Button>
				<Button onPress={onBack}>Отмена</Button>
			</div>
			<Modal isOpen={confirming} onOpenChange={setConfirming}>
				<Dialog
					aria-labelledby="context-publication-confirm-title"
					className="grid gap-2"
				>
					{({ close }) => (
						<div className="grid gap-2">
							<Heading slot="title" id="context-publication-confirm-title">
								{changed.length
									? `Опубликовать ${changed.length} позиций?`
									: `Сохранить связи ${links.length} позиций?`}
							</Heading>
							<p>
								{plan.target.title}. Будут изменены только выбранные строки
								плана. Остальные позиции и существующие публикации сохраняются.
							</p>
							{links.length > 0 && (
								<p>
									{links.length} позиций получат новые связи с источниками;
									выбранный источник и текущая публикация сохраняются.
								</p>
							)}
							{downgrades.length > 0 && (
								<p>
									Подтверждено отдельное понижение качества для{" "}
									{downgrades.length} поз.
								</p>
							)}
							<p>
								Неразмеченные файлы останутся во входящих. Все исходники
								сохраняются. Демо: запись аудиофайлов не выполняется.
							</p>
							{(stale || error) && (
								<p role="alert">
									{error ||
										"Инвентарь источников изменился. Создайте новый предпросмотр перед подтверждением."}
								</p>
							)}
							<div className={dialogFooter}>
								<Button
									className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
									isDisabled={!canConfirm}
									onPress={confirm}
								>
									Подтвердить
								</Button>
								<Button onPress={close}>Отмена</Button>
							</div>
						</div>
					)}
				</Dialog>
			</Modal>
		</section>
	);
}
