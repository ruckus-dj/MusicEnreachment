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
		<main
			className="min-w-0 w-full px-4 pt-[14px] pb-[18px] max-[600px]:p-3"
			id="content"
			tabIndex={-1}
		>
			<PageHeader className="mb-[10px] min-w-0">
				<p className="mb-[3px] text-[10px] tracking-[.06em] text-[var(--muted)]">
					Входящие · исходные теги → сопоставление → публикация
				</p>
				<div className="flex items-center justify-between gap-[14px] max-[600px]:items-start max-[600px]:flex-col">
					<div className="min-w-0">
						<h1 className="m-0 text-xl leading-[1.25] text-[var(--ink)] [text-wrap:balance]">
							Входящие альбомы
						</h1>
						<p className="mt-[3px] mb-0 text-xs text-[var(--muted)]">
							Группы из исходных тегов → поиск издания → назначения →
							публикация. Неполные данные допустимы.
						</p>
					</div>
					<Button
						className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90 max-[600px]:w-full"
						onPress={runAuto}
					>
						Запустить обработку · демо
					</Button>
				</div>
			</PageHeader>
			<ol
				className="mb-[10px] grid list-none grid-cols-4 gap-0 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--raised)] p-0 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
				aria-label="Этапы обработки"
			>
				<li className="flex min-w-0 items-center gap-2 px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						01
					</b>
					<span className="min-w-0">Сгруппировать файлы</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t max-[600px]:border-[var(--line)]">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						02
					</b>
					<span className="min-w-0">Сопоставить</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-l-0 max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						03
					</b>
					<span className="min-w-0">
						Все позиции уверенные{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						автоматически
					</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						04
					</b>
					<span className="min-w-0">
						Иначе{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						ручной разбор, в том числе частичный
					</span>
				</li>
			</ol>
			{state.notice && (
				<p
					role="status"
					className="mb-[10px] border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-[10px] py-[7px] text-xs text-[var(--ink)]"
				>
					{state.notice}
				</p>
			)}
			<Panel className="mb-[10px]" aria-labelledby="incoming-groups-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<div>
						<h2
							className="m-0 text-[13px] font-semibold"
							id="incoming-groups-heading"
						>
							Текущие группы
						</h2>
						<span className="mt-0.5 block text-[10px] text-[var(--muted)]">
							{rows.length} группы · очередь источников
						</span>
					</div>
				</header>
				<div className="min-w-0 overflow-x-auto">
					<Table
						aria-label="Текущие группы входящих альбомов"
						className="w-full min-w-[1040px] border-collapse text-xs [font-variant-numeric:tabular-nums] [&_tbody_tr:last-child_td]:border-b-0 max-[900px]:min-w-[970px]"
					>
						<TableHeader>
							<Column
								id="group"
								isRowHeader
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Альбом / папка
							</Column>
							<Column
								id="files"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Входящие
							</Column>
							<Column
								id="assigned"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Назначено
							</Column>
							<Column
								id="free"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Свободно
							</Column>
							<Column
								id="mode"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Режим
							</Column>
							<Column
								id="published"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Опубликовано
							</Column>
							<Column
								id="reason"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
							>
								Причина
							</Column>
							<Column
								id="action"
								aria-label="Действие"
								className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
							/>
						</TableHeader>
						<TableBody>
							{rows.length === 0 && (
								<Row id="empty">
									<Cell
										colSpan={8}
										className="!p-6 text-center text-[var(--muted)]"
									>
										Входящих групп пока нет.
									</Cell>
								</Row>
							)}
							{rows.map(
								({ group, assignments, used, publishedCount, tracks }) => (
									<Row
										id={group.id}
										key={group.id}
										className="transition-colors hover:bg-[var(--raised)]"
									>
										<Cell className="min-w-[190px] border-b border-[var(--line)] px-2 py-1.5 text-left align-middle">
											<span
												className={`float-left mr-2 inline-grid h-8 w-8 place-items-center text-sm font-semibold text-white ${group.id === "north" ? "bg-[#486d59]" : group.id === "hours" ? "bg-[#676e95]" : group.id === "mix" ? "bg-[#4c717e]" : "bg-[#ac7059]"}`}
												aria-hidden="true"
											>
												{group.title.slice(0, 1).toLocaleUpperCase()}
											</span>
											<span className="grid min-w-0 gap-px">
												<Link
													className="font-semibold wrap-anywhere"
													href={`#match/${group.id}`}
												>
													{group.title}
												</Link>
												<span className="text-[10px] text-[var(--muted)]">
													{artistName(group.artistId, artists)}
												</span>
												<code className="font-mono text-[9px] leading-[1.4] text-[var(--muted)] wrap-anywhere">
													/mnt/inbox/{group.id}
												</code>
											</span>
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-right align-middle whitespace-nowrap [font-variant-numeric:tabular-nums]">
											<span className="inline-flex min-w-6 justify-center rounded-[3px] border border-[var(--line)] bg-[var(--raised)] px-[5px] py-px text-[11px] font-semibold text-[var(--ink)]">
												{group.fileIds.length}
											</span>
											<span className="text-[var(--muted)]"> файлов</span>
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-right align-middle whitespace-nowrap [font-variant-numeric:tabular-nums]">
											<span className="inline-flex min-w-6 justify-center rounded-[3px] border border-[var(--line)] bg-[var(--raised)] px-[5px] py-px text-[11px] font-semibold text-[var(--ink)]">
												{
													Object.values(assignments).filter(
														(item) => item.sourceIds.length,
													).length
												}
											</span>
											<span className="text-[var(--muted)]">
												{" "}
												/ {tracks.length}
											</span>
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-right align-middle whitespace-nowrap [font-variant-numeric:tabular-nums]">
											<span className="inline-flex min-w-6 justify-center rounded-[3px] border border-[var(--line)] bg-[var(--raised)] px-[5px] py-px text-[11px] font-semibold text-[var(--ink)]">
												{
													group.fileIds.filter(
														(id) => files[id]?.available && !used.has(id),
													).length
												}
											</span>
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle">
											<span
												className={`inline-flex max-w-[170px] items-center gap-[5px] text-[10px] leading-[1.3] ${group.processed ? "text-[var(--success)]" : group.autoEligible ? "text-[var(--info)]" : "text-[var(--warning)]"}`}
											>
												<i
													className={`h-1.5 w-1.5 flex-[0_0_6px] rounded-full ${group.processed ? "bg-[var(--success)]" : group.autoEligible ? "bg-[var(--info)]" : "bg-[var(--warning)]"}`}
													aria-hidden="true"
												/>
												{group.processed
													? "Опубликовано автоматически"
													: group.autoEligible
														? "Готов к автоматике"
														: "Ручная обработка"}
											</span>
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-right align-middle whitespace-nowrap [font-variant-numeric:tabular-nums]">
											<span className="inline-flex min-w-6 justify-center rounded-[3px] border border-[var(--line)] bg-[var(--raised)] px-[5px] py-px text-[11px] font-semibold text-[var(--ink)]">
												{publishedCount}
											</span>
											<span className="text-[var(--muted)]">
												{" "}
												/ {tracks.length}
											</span>
										</Cell>
										<Cell className="min-w-40 max-w-[270px] border-b border-[var(--line)] px-2 py-1.5 text-left align-middle text-[10px] text-[var(--muted)] wrap-anywhere">
											{group.reason}
										</Cell>
										<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle">
											<Link
												className="inline-flex min-h-7 items-center whitespace-nowrap rounded-[3px] border border-[var(--line)] px-[7px] py-[3px] hover:border-[var(--accent)] hover:bg-[var(--accent-soft)] hover:no-underline max-[600px]:min-h-10"
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
			<div className="grid grid-cols-2 gap-[10px] max-[600px]:grid-cols-1">
				<Panel className="m-0 p-[10px]">
					<h2 className="mb-1.5 mt-0 text-[13px]">
						Как пройти ручной сценарий
					</h2>
					<ol className="m-0 list-decimal pl-5 text-[11px] text-[var(--muted)] [&>li+li]:mt-[3px]">
						<li>Откройте Northern Lines: уверенные назначения заполнены.</li>
						<li>Назначьте 03 — Northbound (live?) на позицию 03.</li>
						<li>
							Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.
						</li>
						<li>Предпросмотр → подтвердить публикацию выбранных позиций.</li>
					</ol>
				</Panel>
				<Panel className="m-0 p-[10px]">
					<h2 className="mb-1.5 mt-0 text-[13px]">
						Сборник, которого нет у провайдера
					</h2>
					<p className="mb-[7px] mt-0 text-[11px] text-[var(--muted)]">
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
