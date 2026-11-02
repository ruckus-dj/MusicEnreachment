import { useState } from "react";
import { Button } from "react-aria-components";
import {
	runAutomaticMatching,
	artistName,
	createIncomingState,
	incomingFixtures,
	getDraft,
	findTarget,
	providerReleases,
	type IncomingFiles,
	type IncomingReleases,
	type IncomingState,
} from "./incomingModel";

export type IncomingScreenProps = {
	files?: IncomingFiles;
	releases?: IncomingReleases;
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onFiles?: (files: IncomingFiles) => void;
	onReleases?: (releases: IncomingReleases) => void;
};

export function IncomingScreen({
	files: givenFiles,
	releases: givenReleases,
	state: givenState,
	onState,
	onFiles,
	onReleases,
}: IncomingScreenProps) {
	const [localState, setLocalState] = useState(createIncomingState);
	const [localFiles, setLocalFiles] = useState<IncomingFiles>(() =>
		structuredClone(incomingFixtures.files),
	);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const state = givenState ?? localState;
	const files = givenFiles ?? localFiles;
	const releases = givenReleases ?? localReleases;
	const updateState = () => {
		const next = structuredClone(state);
		givenState ? onState?.(next) : setLocalState(next);
	};
	const groups = state.groups;
	const rows = groups.map((group) => {
		const target = findTarget(group, releases);
		const assignments = getDraft(group, files, providerReleases, releases);
		const used = new Set(
			Object.values(assignments).flatMap((assignment) => assignment.sourceIds),
		);
		const published = releases.find((release) =>
			group.mode === "local"
				? release.id === group.releaseId
				: release.providerId === target?.id,
		);
		const publishedCount =
			published?.tracks.filter((track) => Boolean(track.published)).length ?? 0;
		return {
			group,
			target,
			assignments,
			used,
			publishedCount,
			tracks: target?.tracks ?? [],
		};
	});
	const runAuto = () => {
		const nextReleases = structuredClone(releases);
		const count = runAutomaticMatching(
			state,
			files,
			nextReleases,
			providerReleases,
		);
		state.notice = count
			? `Демо: автоматически опубликовано ${count} позиций Small Hours. Неполные группы остались в ручном разборе. Файловые операции не выполнялись.`
			: "Нет новых полностью уверенных релизов. Автопубликация не повторяет и не понижает качество.";
		if (givenReleases) onReleases?.(nextReleases);
		else setLocalReleases(nextReleases);
		updateState();
	};
	return (
		<main className="demo-shell incoming-screen">
			<header>
				<p className="demo-eyebrow">
					Входящие · исходные теги → сопоставление → публикация
				</p>
				<h1>Входящие альбомы</h1>
				<p>
					Группы из исходных тегов → поиск издания → назначения → публикация.
					Неполные данные допустимы.
				</p>
				<Button className="demo-button" onPress={runAuto}>
					Запустить обработку · демо
				</Button>
			</header>
			<div className="incoming-flow">
				<span>1 · Сгруппировать файлы</span>
				<span>2 · Сопоставить</span>
				<span>3 · Все позиции уверенные → автоматически</span>
				<span>4 · Иначе → ручной разбор, в том числе частичный</span>
			</div>
			{state.notice && (
				<p role="status" className="demo-notice">
					{state.notice}
				</p>
			)}
			<section aria-labelledby="incoming-groups-heading">
				<h2 id="incoming-groups-heading">Текущие группы</h2>
				<div className="incoming-table-wrap">
					<table>
						<thead>
							<tr>
								<th>Альбом / папка</th>
								<th>Входящие</th>
								<th>Назначено</th>
								<th>Свободно</th>
								<th>Режим</th>
								<th>Опубликовано</th>
								<th>Причина</th>
								<th />
							</tr>
						</thead>
						<tbody>
							{rows.map(
								({ group, assignments, used, publishedCount, tracks }) => (
									<tr key={group.id}>
										<td>
											<a href={`#match/${group.id}`}>{group.title}</a>
											<span className="incoming-sub">
												{artistName(group.artistId)} · /mnt/inbox/{group.id}
											</span>
										</td>
										<td>{group.fileIds.length} файлов</td>
										<td>
											{
												Object.values(assignments).filter(
													(item) => item.sourceIds.length,
												).length
											}{" "}
											/ {tracks.length}
										</td>
										<td>
											{
												group.fileIds.filter(
													(id) => files[id]?.available && !used.has(id),
												).length
											}
										</td>
										<td>
											{group.processed
												? "Опубликовано автоматически"
												: group.autoEligible
													? "Готов к автоматике"
													: "Ручная обработка"}
										</td>
										<td>
											{publishedCount} / {tracks.length}
										</td>
										<td>{group.reason}</td>
										<td>
											<a className="demo-button" href={`#match/${group.id}`}>
												Открыть
											</a>
										</td>
									</tr>
								),
							)}
						</tbody>
					</table>
				</div>
			</section>
			<div className="incoming-bottom">
				<section>
					<h2>Как пройти ручной сценарий</h2>
					<ol>
						<li>Откройте Northern Lines: уверенные назначения заполнены.</li>
						<li>Назначьте 03 — Northbound (live?) на позицию 03.</li>
						<li>
							Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.
						</li>
						<li>Предпросмотр → подтвердить публикацию выбранных позиций.</li>
					</ol>
				</section>
				<section>
					<h2>Сборник, которого нет у провайдера</h2>
					<p>
						Road Notes сохраняет собственное название и порядок. Композиции
						можно найти в других изданиях; альбом провайдера не заменяет
						локальный сборник.
					</p>
					<a href="#match/mix">Открыть локальный сборник</a>
				</section>
			</div>
		</main>
	);
}
