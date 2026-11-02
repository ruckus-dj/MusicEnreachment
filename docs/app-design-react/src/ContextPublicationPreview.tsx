import { useState } from "react";
import type { CatalogArtist } from "./catalogData";
import type { OriginalRelease } from "./catalogData";
import {
	applyPlan,
	planRows,
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
} from "./incomingModel";

export function ContextPublicationPreview({
	plan,
	files,
	releases,
	artists,
	inventoryVersion,
	confirmDowngrade,
	onPlan,
	onReleases,
	onBack,
}: {
	plan: IncomingPlan | null;
	files: IncomingFiles;
	releases: IncomingReleases;
	artists: CatalogArtist[];
	inventoryVersion: number;
	confirmDowngrade: boolean;
	onPlan: (plan: IncomingPlan | null, confirmDowngrade?: boolean) => void;
	onReleases: (releases: OriginalRelease[]) => void;
	onBack: () => void;
}) {
	const [error, setError] = useState("");
	if (!plan)
		return (
			<section>
				<h1>Предпросмотр публикации</h1>
				<p>Нет сохранённого плана публикации. Откройте издание или позицию.</p>
				<a href="#library">К медиатеке</a>
			</section>
		);

	const rows = planRows(plan, files, releases, undefined, artists);
	const active = rows.filter((row) => row.assignment?.included && row.file);
	const changed = active.filter((row) => row.action !== "Без изменений");
	const downgrades = changed.filter((row) => row.downgrade);
	const stale = plan.inventoryVersion !== inventoryVersion;
	const canConfirm =
		!stale && changed.length > 0 && (!downgrades.length || confirmDowngrade);
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
		applyPlan(plan, files, next, undefined, false, artists);
		onReleases(next);
		onPlan(null, false);
	};

	return (
		<section aria-labelledby="context-publication-title">
			<header className="demo-object-header">
				<div>
					<h1 id="context-publication-title">
						Предпросмотр: {plan.target.title}
					</h1>
					<p>
						Контекстный предпросмотр операции. Это не отдельный список
						публикаций.
					</p>
				</div>
				<button type="button" onClick={onBack}>
					Вернуться к объекту
				</button>
			</header>
			{(stale || error) && (
				<p role="alert">
					{error ||
						"Инвентарь источников изменился. Создайте новый предпросмотр перед подтверждением."}
				</p>
			)}
			<p className="demo-preview-counts">
				<span>
					{active.length} / {plan.target.tracks.length} выбрано
				</span>
				<span>{changed.length} изменений</span>
				<span>MKA · перепаковка без перекодирования</span>
			</p>
			<div className="demo-preview-table">
				<table>
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
						{rows.map((row) => (
							<tr key={row.targetTrack.id}>
								<td>
									<input
										type="checkbox"
										aria-label={`Включить ${row.targetTrack.title}`}
										checked={Boolean(row.assignment?.included && row.file)}
										disabled={!row.file}
										onChange={(event) =>
											updateIncluded(
												String(row.targetTrack.id),
												event.currentTarget.checked,
											)
										}
									/>
								</td>
								<td>
									{row.targetTrack.position} · {row.targetTrack.title}
								</td>
								<td>
									{row.file
										? `${row.file.name} · ${row.file.codec}`
										: "Нет выбранного источника"}
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
								</td>
								<td>
									{row.action}
									{row.downgrade && row.assignment?.included
										? " · Понижение качества"
										: ""}
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
				<label>
					<input
						type="checkbox"
						checked={confirmDowngrade}
						onChange={(event) => onPlan(plan, event.currentTarget.checked)}
					/>
					Подтверждаю отдельное понижение качества ({downgrades.length} поз.)
				</label>
			)}
			<div className="demo-object-actions">
				<button type="button" disabled={!canConfirm} onClick={confirm}>
					Подтвердить публикацию
				</button>
				<button type="button" onClick={onBack}>
					Отмена
				</button>
			</div>
		</section>
	);
}
