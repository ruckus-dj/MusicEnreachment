import { useState, useSyncExternalStore } from "react";
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
	type IncomingGroup,
	type IncomingReleases,
	type IncomingState,
} from "./incomingModel";
import type { CatalogArtist } from "./catalogData";
import { useI18n } from "./i18n";
import {
	incomingFilesCount,
	incomingGroupsCount,
	incomingMessage,
	type IncomingMessage,
} from "./incomingMessages";
import { formatUiNotice } from "./uiNotice";

const mobileQuery = "(max-width: 599px)";

function subscribeToViewport(listener: () => void) {
	const media = window.matchMedia?.(mobileQuery);
	if (!media) return () => {};
	media.addEventListener("change", listener);
	return () => media.removeEventListener("change", listener);
}

function useIsMobile() {
	return useSyncExternalStore(
		subscribeToViewport,
		() => window.matchMedia?.(mobileQuery).matches ?? false,
		() => false,
	);
}

export type IncomingScreenProps = {
	destination?: import("./publicationOutput").PublicationDestination;
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
	destination,
	files: givenFiles,
	releases: givenReleases,
	providers: givenProviders,
	artists = incomingFixtures.artists,
	state: givenState,
	onState,
	onFiles,
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
	const isMobile = useIsMobile();
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
		<main
			className="min-w-0 w-full px-4 pt-[14px] pb-[18px] max-[600px]:p-3"
			id="content"
			tabIndex={-1}
		>
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
							{msg("currentGroups")}
						</h2>
						<span className="mt-0.5 block text-xs text-[var(--muted)]">
							{incomingGroupsCount(locale, rows.length)}
						</span>
					</div>
				</header>
				{isMobile ? (
					<ul
						aria-label={msg("groupList")}
						className="m-0 grid list-none gap-2 p-2"
					>
						{rows.length === 0 ? (
							<li className="py-6 text-center text-[var(--muted)]">
								{msg("noGroups")}
							</li>
						) : (
							rows.map(
								({ group, assignments, used, publishedCount, tracks }) => {
									const assignedCount = Object.values(assignments).filter(
										(item) => item.sourceIds.length,
									).length;
									const freeCount = group.fileIds.filter(
										(id) => files[id]?.available && !used.has(id),
									).length;
									return (
										<li
											key={group.id}
											className="grid min-w-0 gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
										>
											<div className="flex min-w-0 items-start justify-between gap-3">
												<div className="min-w-0">
													<Link
														className="font-semibold wrap-anywhere"
														href={`#match/${group.id}`}
													>
														{group.title}
													</Link>
													<p className="m-0 mt-0.5 text-xs text-[var(--muted)]">
														{artistName(group.artistId, artists) ||
															msg("artistNotSpecified")}
													</p>
												</div>
												<Link
													className="inline-flex min-h-10 shrink-0 items-center rounded-[3px] border border-[var(--line)] px-3 hover:border-[var(--accent)] hover:bg-[var(--accent-soft)] hover:no-underline"
													href={`#match/${group.id}`}
												>
													{msg("open")}
												</Link>
											</div>
											<dl className="m-0 grid grid-cols-2 gap-x-3 gap-y-1 text-xs [font-variant-numeric:tabular-nums]">
												<div>
													<dt className="inline text-[var(--muted)]">
														{msg("files")}{" "}
													</dt>
													<dd className="inline m-0 font-semibold">
														{group.fileIds.length}
													</dd>
												</div>
												<div>
													<dt className="inline text-[var(--muted)]">
														{msg("assigned")}{" "}
													</dt>
													<dd className="inline m-0 font-semibold">
														{assignedCount} / {tracks.length}
													</dd>
												</div>
												<div>
													<dt className="inline text-[var(--muted)]">
														{msg("free")}{" "}
													</dt>
													<dd className="inline m-0 font-semibold">
														{freeCount}
													</dd>
												</div>
												<div>
													<dt className="inline text-[var(--muted)]">
														{msg("published")}{" "}
													</dt>
													<dd className="inline m-0 font-semibold">
														{publishedCount} / {tracks.length}
													</dd>
												</div>
											</dl>
											<p className="m-0 text-xs text-[var(--muted)]">
												{renderReason(group.reason)}
											</p>
										</li>
									);
								},
							)
						)}
					</ul>
				) : (
					<div className="min-w-0 overflow-x-auto">
						<Table
							aria-label={msg("groupList")}
							className="w-full min-w-[1040px] border-collapse text-xs [font-variant-numeric:tabular-nums] [&_tbody_tr:last-child_td]:border-b-0 max-[900px]:min-w-[970px]"
						>
							<TableHeader>
								<Column
									id="group"
									isRowHeader
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("albumFolder")}
								</Column>
								<Column
									id="files"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("incoming")}
								</Column>
								<Column
									id="assigned"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("assignedHeading")}
								</Column>
								<Column
									id="free"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("freeHeading")}
								</Column>
								<Column
									id="mode"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("mode")}
								</Column>
								<Column
									id="published"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-right align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("publishedHeading")}
								</Column>
								<Column
									id="reason"
									className="border-b border-[var(--line)] bg-[var(--raised)] px-2 py-1.5 text-left align-middle text-[10px] font-semibold whitespace-nowrap"
								>
									{msg("reason")}
								</Column>
								<Column
									id="action"
									aria-label={msg("action")}
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
											{msg("noGroups")}
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
														{artistName(group.artistId, artists) ||
															msg("artistNotSpecified")}
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
												<span className="text-[var(--muted)]">
													{incomingFilesCount(
														locale,
														group.fileIds.length,
													).replace(/^\d+\s*/, "")}
												</span>
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
														? msg("publishedAutomatically")
														: group.autoEligible
															? msg("readyForAutomatic")
															: msg("manualProcessing")}
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
												{renderReason(group.reason)}
											</Cell>
											<Cell className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle">
												<Link
													className="inline-flex min-h-7 items-center whitespace-nowrap rounded-[3px] border border-[var(--line)] px-[7px] py-[3px] hover:border-[var(--accent)] hover:bg-[var(--accent-soft)] hover:no-underline max-[600px]:min-h-10"
													href={`#match/${group.id}`}
												>
													{msg("open")}
												</Link>
											</Cell>
										</Row>
									),
								)}
							</TableBody>
						</Table>
					</div>
				)}
			</Panel>
			<div className="grid grid-cols-2 gap-[10px] max-[600px]:grid-cols-1">
				<Panel className="m-0 p-[10px]">
					<h2 className="mb-1.5 mt-0 text-[13px]">{msg("manualScenario")}</h2>
					<ol className="m-0 list-decimal pl-5 text-[11px] text-[var(--muted)] [&>li+li]:mt-[3px]">
						<li>{msg("manualStep1")}</li>
						<li>{msg("manualStep2")}</li>
						<li>{msg("manualStep3")}</li>
						<li>{msg("manualStep4")}</li>
					</ol>
				</Panel>
				<Panel className="m-0 p-[10px]">
					<h2 className="mb-1.5 mt-0 text-[13px]">
						{msg("missingProviderCompilation")}
					</h2>
					<p className="mb-[7px] mt-0 text-[11px] text-[var(--muted)]">
						{msg("compilationDescription")}
					</p>
					<Link href="#match/mix">{msg("openCompilation")}</Link>
				</Panel>
			</div>
		</main>
	);
}
