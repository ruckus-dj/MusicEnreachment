import { Fragment, useEffect, useRef, useState } from "react";
import type { CatalogArtist } from "./catalogData";
import { useI18n } from "./i18n";
import {
	type IncomingMessage,
	incomingGroupsCount,
	incomingMessage,
} from "./incomingMessages";
import {
	artistName,
	createIncomingState,
	type IncomingFiles,
	type IncomingGroup,
	type IncomingReleases,
	type IncomingState,
	incomingFixtures,
	providerReleases,
	runAutomaticMatching,
} from "./incomingModel";
import { incomingOverviewRows } from "./incomingOverview";
import { Button, Link, PageHeader, Panel } from "./ui";
import { formatUiNotice } from "./uiNotice";

export type IncomingScreenProps = {
	destination?: import("./publicationOutput").PublicationDestination;
	files?: IncomingFiles;
	releases?: IncomingReleases;
	providers?: IncomingReleases;
	roots?: readonly { path: string }[];
	artists?: CatalogArtist[];
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onFiles?: (files: IncomingFiles) => void;
	onReleases?: (releases: IncomingReleases) => void;
};

export function IncomingScreen({
	destination,
	files: givenFiles,
	releases: givenReleases,
	providers: givenProviders,
	roots = [],
	artists = incomingFixtures.artists,
	state: givenState,
	onState,
	onReleases,
}: IncomingScreenProps) {
	const { locale } = useI18n();
	const msg = (
		key: IncomingMessage,
		values?: Record<string, string | number>,
	) => incomingMessage(locale, key, values);
	const renderReason = (reason: IncomingGroup["reason"]) => {
		if (typeof reason === "string") return reason;
		switch (reason.code) {
			case "incomplete":
				return msg("incompleteReason");
			case "confident":
				return msg("confidentReason", reason.params);
			case "compilation":
				return msg("compilationReason");
		}
	};
	const [localState, setLocalState] = useState(createIncomingState);
	const [localFiles] = useState<IncomingFiles>(() =>
		structuredClone(incomingFixtures.files),
	);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const state = givenState ?? localState;
	const [hash, setHash] = useState(() => window.location.hash);
	const drag = useRef<{ groupId: string; fileId: string } | null>(null);
	const [dropTarget, setDropTarget] = useState("");
	const [moveDestinations, setMoveDestinations] = useState<
		Record<string, string>
	>({});
	const expanded = new URLSearchParams(hash.split("?")[1]).getAll("group");
	useEffect(() => {
		const onHashChange = () => setHash(window.location.hash);
		window.addEventListener("hashchange", onHashChange);
		return () => window.removeEventListener("hashchange", onHashChange);
	}, []);
	const files = givenFiles ?? localFiles;
	const releases = givenReleases ?? localReleases;
	const providers = givenProviders ?? providerReleases;
	const updateState = () => {
		const next = structuredClone(state);
		givenState ? onState?.(next) : setLocalState(next);
	};
	function toggleGroup(id: string) {
		const params = new URLSearchParams();
		const ids = expanded.includes(id)
			? expanded.filter((openId) => openId !== id)
			: [...expanded, id];
		for (const groupId of ids) params.append("group", groupId);
		window.location.hash = params.size ? `#review?${params}` : "#review";
	}
	function moveFile(sourceId: string, fileId: string, targetId: string) {
		const source = state.groups.find((group) => group.id === sourceId);
		const target = state.groups.find((group) => group.id === targetId);
		if (!source?.fileIds.includes(fileId) || !target || source === target)
			return;
		const invalidate = (group: IncomingGroup, fileIds: string[]) => ({
			...group,
			fileIds,
			autoEligible: false,
			processed: false,
			drafts: {},
			reason: { code: "incomplete" as const },
		});
		const next = {
			...state,
			plan: null,
			groups: state.groups
				.map((group) =>
					group === source
						? invalidate(
								group,
								group.fileIds.filter((id) => id !== fileId),
							)
						: group === target
							? invalidate(group, [...new Set([...group.fileIds, fileId])])
							: group,
				)
				.filter((group) => group.fileIds.length),
		};
		givenState ? onState?.(next) : setLocalState(next);
		setMoveDestinations({});
	}
	const labels =
		locale === "ru"
			? {
					title: "Нет тега названия",
					move: "Переместить",
					target: "Куда переместить",
				}
			: { title: "No title tag", move: "Move", target: "Destination group" };
	const groups = state.groups;
	const rows = incomingOverviewRows(
		groups,
		files,
		locale,
		roots.map((root) => root.path),
	);
	const runAuto = () => {
		const nextReleases = structuredClone(releases);
		const count = runAutomaticMatching(
			state,
			files,
			nextReleases,
			providers,
			artists,
			destination,
		);
		state.notice = "";
		state.noticeDescriptor = null;
		state.uiNotice = count
			? { namespace: "incoming", id: "automaticPublished", params: { count } }
			: { namespace: "incoming", id: "nothingToPublish" };
		if (givenReleases) onReleases?.(nextReleases);
		else setLocalReleases(nextReleases);
		updateState();
	};
	return (
		<section className="min-w-0 w-full px-4 pt-[14px] pb-[18px] max-[600px]:p-3">
			<PageHeader className="mb-[10px] min-w-0">
				<p className="mb-[3px] text-xs text-[var(--muted)]">
					{msg("workflow")}
				</p>
				<div className="flex items-center justify-between gap-[14px] max-[600px]:items-start max-[600px]:flex-col">
					<div className="min-w-0">
						<h1 className="m-0 text-xl leading-[1.25] text-[var(--ink)] [text-wrap:balance]">
							{msg("title")}
						</h1>
						<p className="mt-[3px] mb-0 text-xs text-[var(--muted)]">
							{msg("introduction")}
						</p>
					</div>
					<Button
						className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90 max-[600px]:w-full"
						onPress={runAuto}
					>
						{msg("process")}
					</Button>
				</div>
			</PageHeader>
			<ol
				className="mb-[10px] grid list-none grid-cols-4 gap-0 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--raised)] p-0 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
				aria-label={msg("stages")}
			>
				<li className="flex min-w-0 items-center gap-2 px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						01
					</b>
					<span className="min-w-0">{msg("groupFiles")}</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t max-[600px]:border-[var(--line)]">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						02
					</b>
					<span className="min-w-0">{msg("match")}</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-l-0 max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						03
					</b>
					<span className="min-w-0">
						{msg("allConfidentAutomatic")}{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						{msg("automaticDirection")}
					</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						04
					</b>
					<span className="min-w-0">
						{msg("otherwiseManual")}{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						{msg("manualDirection")}
					</span>
				</li>
			</ol>
			{(state.uiNotice || state.notice || state.noticeDescriptor) && (
				<p
					role="status"
					className="mb-[10px] border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-[10px] py-[7px] text-xs text-[var(--ink)]"
				>
					{state.notice ||
						(state.uiNotice
							? formatUiNotice(locale, state.uiNotice)
							: state.noticeDescriptor?.code === "automaticPublished"
								? msg("automaticNotice", state.noticeDescriptor.params)
								: state.noticeDescriptor
									? msg("noAutomaticNotice")
									: "")}
				</p>
			)}
			<Panel className="mb-[10px]" aria-labelledby="incoming-groups-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<div>
						<h2
							className="m-0 text-[13px] font-semibold"
							id="incoming-groups-heading"
						>
							{msg("overviewTitle")}
						</h2>
						<span className="mt-0.5 block text-xs text-[var(--muted)]">
							{incomingGroupsCount(locale, rows.length)}
						</span>
					</div>
				</header>
				<div className="min-w-0 overflow-x-auto">
					<table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-xs [font-variant-numeric:tabular-nums] [&_tbody_tr:last-child_td]:border-b-0">
						<caption className="sr-only">{msg("overviewCaption")}</caption>
						<colgroup>
							<col className="w-[36%]" />
							<col className="w-[28%]" />
							<col className="w-[14%]" />
							<col className="w-[22%]" />
						</colgroup>
						<thead>
							<tr>
								<th
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 align-top text-[10px] font-semibold wrap-anywhere"
									scope="col"
								>
									{msg("group")}
								</th>
								<th
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 align-top text-[10px] font-semibold wrap-anywhere"
									scope="col"
								>
									{msg("uniqueFilesPaths")}
								</th>
								<th
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 align-top text-[10px] font-semibold wrap-anywhere"
									scope="col"
								>
									{msg("readyTotal")}
								</th>
								<th
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 align-top text-[10px] font-semibold wrap-anywhere"
									scope="col"
								>
									{msg("evaluationState")}
								</th>
							</tr>
						</thead>
						<tbody>
							{rows.length === 0 && (
								<tr>
									<td
										className="p-6 text-center text-[var(--muted)]"
										colSpan={4}
									>
										{msg("noGroups")}
									</td>
								</tr>
							)}
							{rows.map(
								({
									group,
									name,
									memberCount,
									pathCount,
									paths,
									readyCount,
								}) => (
									<Fragment key={group.id}>
										<tr
											className={`cursor-pointer ${dropTarget === group.id || expanded.includes(group.id) ? "bg-[var(--accent-soft)]" : ""}`}
											onClick={(event) => {
												if (
													event.target instanceof Element &&
													!event.target.closest("a,button")
												)
													toggleGroup(group.id);
											}}
											onDragOver={(event) => {
												if (!drag.current || drag.current.groupId === group.id)
													return;
												event.preventDefault();
												event.dataTransfer.dropEffect = "move";
												setDropTarget(group.id);
											}}
											onDragLeave={(event) => {
												if (
													event.relatedTarget instanceof Node &&
													event.currentTarget.contains(event.relatedTarget)
												)
													return;
												setDropTarget((current) =>
													current === group.id ? "" : current,
												);
											}}
											onDrop={(event) => {
												event.preventDefault();
												const source = drag.current;
												drag.current = null;
												setDropTarget("");
												if (
													source &&
													event.dataTransfer.getData(
														"application/x-melotrove-group-member",
													) === source.fileId
												)
													moveFile(source.groupId, source.fileId, group.id);
											}}
										>
											<td className="border-b border-[var(--line)] px-2 py-1.5 align-top">
												<button
													type="button"
													className="text-left font-semibold wrap-anywhere"
													aria-expanded={expanded.includes(group.id)}
													onClick={() => toggleGroup(group.id)}
												>
													{name}
												</button>
												<p className="m-0 mt-0.5 text-[11px] text-[var(--muted)] wrap-anywhere">
													{artistName(group.artistId, artists) ||
														msg("artistNotSpecified")}
												</p>
											</td>
											<td className="border-b border-[var(--line)] px-2 py-1.5 align-top">
												<span className="block whitespace-nowrap">
													{memberCount} / {pathCount}
												</span>
											</td>
											<td className="border-b border-[var(--line)] px-2 py-1.5 align-top">
												<span className="whitespace-nowrap">
													{readyCount} / {memberCount}
												</span>
											</td>
											<td className="border-b border-[var(--line)] px-2 py-1.5 align-top text-[11px] wrap-anywhere">
												<Link
													className="block font-semibold"
													href={`#match/${group.id}`}
												>
													{msg("openGroup", { name })}
												</Link>
												{renderReason(group.reason)}
											</td>
										</tr>
										{expanded.includes(group.id) && (
											<tr>
												<td
													colSpan={4}
													className="border-b border-[var(--line)] px-2 py-1.5"
												>
													<ul className="m-0 list-none p-0">
														{group.fileIds.map((id) => {
															const file = files[id];
															const tag = (key: string) =>
																file?.tags?.[key]?.join(" · ");
															return (
																<li
																	key={id}
																	draggable
																	className="grid cursor-grab grid-cols-[8%_minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)] gap-2 border-b border-[var(--line)] p-2 wrap-anywhere"
																	onDragStart={(event) => {
																		drag.current = {
																			groupId: group.id,
																			fileId: id,
																		};
																		event.dataTransfer.setData(
																			"application/x-melotrove-group-member",
																			id,
																		);
																		event.dataTransfer.effectAllowed = "move";
																	}}
																	onDragEnd={() => {
																		drag.current = null;
																		setDropTarget("");
																	}}
																>
																	<span>{tag("TRACKNUMBER") || "—"}</span>
																	<strong>
																		{tag("TITLE") || labels.title}
																	</strong>
																	<span>
																		{tag("ARTIST") || tag("ALBUMARTIST") || "—"}
																	</span>
																	<span>{tag("ALBUM") || "—"}</span>
																	<div className="grid gap-1">
																		<select
																			className="w-full min-w-0"
																			aria-label={`${labels.target}: ${tag("TITLE") || labels.title}`}
																			value={moveDestinations[id] ?? ""}
																			onChange={(event) =>
																				setMoveDestinations({
																					...moveDestinations,
																					[id]: event.target.value,
																				})
																			}
																		>
																			<option value="">{labels.target}</option>
																			{rows
																				.filter(
																					(row) => row.group.id !== group.id,
																				)
																				.map((row) => (
																					<option
																						key={row.group.id}
																						value={row.group.id}
																					>
																						{row.name}
																					</option>
																				))}
																		</select>
																		<Button
																			isDisabled={!moveDestinations[id]}
																			onPress={() =>
																				moveFile(
																					group.id,
																					id,
																					moveDestinations[id],
																				)
																			}
																		>
																			{labels.move}
																		</Button>
																	</div>
																</li>
															);
														})}
													</ul>
													<ul className="m-0 list-none p-2 text-[11px] text-[var(--muted)]">
														{paths.map((path) => (
															<li className="wrap-anywhere" key={path.key}>
																{path.label}
															</li>
														))}
													</ul>
												</td>
											</tr>
										)}
									</Fragment>
								),
							)}
						</tbody>
					</table>
				</div>
			</Panel>
		</section>
	);
}
