import { useState } from "react";
import {
	Button,
	Cell,
	Column,
	Link,
	PageHeader,
	Panel,
	Row,
	Table,
	TableBody,
	TableHeader,
} from "./ui";
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
import type { CatalogArtist } from "./catalogData";

export type IncomingScreenProps = {
	files?: IncomingFiles;
	releases?: IncomingReleases;
	providers?: IncomingReleases;
	artists?: CatalogArtist[];
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onFiles?: (files: IncomingFiles) => void;
	onReleases?: (releases: IncomingReleases) => void;
};

export function IncomingScreen({
	files: givenFiles,
	releases: givenReleases,
	providers: givenProviders,
	artists = incomingFixtures.artists,
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
	const providers = givenProviders ?? providerReleases;
	const updateState = () => {
		const next = structuredClone(state);
		givenState ? onState?.(next) : setLocalState(next);
	};
	const groups = state.groups;
	const rows = groups.map((group) => {
		const target = findTarget(group, releases, providers);
		const assignments = getDraft(group, files, providers, releases);
		const used = new Set(
			Object.values(assignments).flatMap((assignment) => assignment.sourceIds),
		);
		const published = releases.find((release) =>
			group.mode === "local"
				? release.id === group.releaseId
				: release.providerId === target?.id ||
					release.providerId === target?.providerId,
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
			providers,
			artists,
		);
		state.notice = count
			? `Демо: автоматически опубликовано ${count} позиций Small Hours. Неполные группы остались в ручном разборе. Файловые операции не выполнялись.`
			: "Нет новых полностью уверенных релизов. Автопубликация не повторяет и не понижает качество.";
		if (givenReleases) onReleases?.(nextReleases);
		else setLocalReleases(nextReleases);
		updateState();
	};
	return (
		<main className="demo-shell incoming-screen" id="content" tabIndex={-1}>
			<PageHeader className="incoming-page-header">
				<p className="demo-eyebrow">
					Входящие · исходные теги → сопоставление → публикация
				</p>
				<div className="incoming-page-title">
					<div>
						<h1>Входящие альбомы</h1>
						<p>
							Группы из исходных тегов → поиск издания → назначения →
							публикация. Неполные данные допустимы.
						</p>
					</div>
					<Button className="ui-button--primary" onPress={runAuto}>
						Запустить обработку · демо
					</Button>
				</div>
			</PageHeader>
			<ol className="incoming-flow" aria-label="Этапы обработки">
				<li>
					<b>01</b>
					<span>Сгруппировать файлы</span>
				</li>
				<li>
					<b>02</b>
					<span>Сопоставить</span>
				</li>
				<li>
					<b>03</b>
					<span>
						Все позиции уверенные <i aria-hidden="true">→</i> автоматически
					</span>
				</li>
				<li>
					<b>04</b>
					<span>
						Иначе <i aria-hidden="true">→</i> ручной разбор, в том числе
						частичный
					</span>
				</li>
			</ol>
			{state.notice && (
				<p role="status" className="demo-notice">
					{state.notice}
				</p>
			)}
			<Panel
				className="incoming-groups"
				aria-labelledby="incoming-groups-heading"
			>
				<header className="ui-panel-heading">
					<div>
						<h2 id="incoming-groups-heading">Текущие группы</h2>
						<span className="incoming-section-meta">
							{rows.length} группы · очередь источников
						</span>
					</div>
				</header>
				<div className="incoming-table-wrap">
					<Table
						aria-label="Текущие группы входящих альбомов"
						className="incoming-table"
					>
						<TableHeader>
							<Column id="group" isRowHeader>
								Альбом / папка
							</Column>
							<Column id="files" className="numeric">
								Входящие
							</Column>
							<Column id="assigned" className="numeric">
								Назначено
							</Column>
							<Column id="free" className="numeric">
								Свободно
							</Column>
							<Column id="mode">Режим</Column>
							<Column id="published" className="numeric">
								Опубликовано
							</Column>
							<Column id="reason">Причина</Column>
							<Column id="action" aria-label="Действие" />
						</TableHeader>
						<TableBody>
							{rows.length === 0 && (
								<Row id="empty">
									<Cell colSpan={8} className="incoming-empty">
										Входящих групп пока нет.
									</Cell>
								</Row>
							)}
							{rows.map(
								({ group, assignments, used, publishedCount, tracks }) => (
									<Row id={group.id} key={group.id}>
										<Cell className="incoming-entity">
											<span
												className={`incoming-initial incoming-initial--${group.id}`}
												aria-hidden="true"
											>
												{group.title.slice(0, 1).toLocaleUpperCase()}
											</span>
											<span className="incoming-entity-copy">
												<Link href={`#match/${group.id}`}>{group.title}</Link>
												<span className="incoming-sub">
													{artistName(group.artistId, artists)}
												</span>
												<code className="incoming-path">
													/mnt/inbox/{group.id}
												</code>
											</span>
										</Cell>
										<Cell className="incoming-numeric">
											<span className="incoming-count">
												{group.fileIds.length}
											</span>
											<span> файлов</span>
										</Cell>
										<Cell className="incoming-numeric">
											<span className="incoming-count">
												{
													Object.values(assignments).filter(
														(item) => item.sourceIds.length,
													).length
												}
											</span>
											<span> / {tracks.length}</span>
										</Cell>
										<Cell className="incoming-numeric">
											<span className="incoming-count">
												{
													group.fileIds.filter(
														(id) => files[id]?.available && !used.has(id),
													).length
												}
											</span>
										</Cell>
										<Cell>
											<span
												className={`incoming-status ${group.processed ? "is-success" : group.autoEligible ? "is-ready" : "is-manual"}`}
											>
												<i aria-hidden="true" />
												{group.processed
													? "Опубликовано автоматически"
													: group.autoEligible
														? "Готов к автоматике"
														: "Ручная обработка"}
											</span>
										</Cell>
										<Cell className="incoming-numeric">
											<span className="incoming-count">{publishedCount}</span>
											<span> / {tracks.length}</span>
										</Cell>
										<Cell className="incoming-reason">{group.reason}</Cell>
										<Cell>
											<Link
												className="incoming-open"
												href={`#match/${group.id}`}
											>
												Открыть
											</Link>
										</Cell>
									</Row>
								),
							)}
						</TableBody>
					</Table>
				</div>
			</Panel>
			<div className="incoming-bottom">
				<Panel className="incoming-info-panel">
					<h2>Как пройти ручной сценарий</h2>
					<ol>
						<li>Откройте Northern Lines: уверенные назначения заполнены.</li>
						<li>Назначьте 03 — Northbound (live?) на позицию 03.</li>
						<li>
							Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.
						</li>
						<li>Предпросмотр → подтвердить публикацию выбранных позиций.</li>
					</ol>
				</Panel>
				<Panel className="incoming-info-panel">
					<h2>Сборник, которого нет у провайдера</h2>
					<p>
						Road Notes сохраняет собственное название и порядок. Композиции
						можно найти в других изданиях; альбом провайдера не заменяет
						локальный сборник.
					</p>
					<Link href="#match/mix">Открыть локальный сборник</Link>
				</Panel>
			</div>
		</main>
	);
}
