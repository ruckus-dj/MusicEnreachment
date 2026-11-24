import { useEffect, useRef, useState } from "react";
import { Button, PageHeader } from "../../components/ui";
import { useI18n } from "../../i18n";
import { formatUiNotice } from "../../infrastructure/uiNotice";
import type { CatalogArtist } from "../library/catalogData";
import {
	compareSourceFiles,
	readSourceTrackOrder,
} from "../matching/sourceFileOrder";
import {
	IncomingGroupsOverviewView,
	type IncomingGroupViewRow,
} from "./IncomingGroupsOverviewView";
import {
	IncomingGroupsPanel,
	incomingGroupsEmpty,
} from "./IncomingGroupsPanel";
import { type IncomingMessage, incomingMessage } from "./incomingMessages";
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
	usableFile,
} from "./incomingModel";
import { incomingOverviewRows } from "./incomingOverview";
import { canSplitGroup, splitFileIntoNewGroup } from "./incomingSplitModel";

export type IncomingScreenProps = {
	destination?: import("../publication/publicationOutput").PublicationDestination;
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
	const groupDisclosureRefs = useRef(new Map<string, HTMLButtonElement>());
	const pendingGroupFocus = useRef<{
		groupId: string;
		preventScroll: boolean;
	} | null>(null);
	const expanded = new URLSearchParams(hash.split("?")[1]).getAll("group");
	useEffect(() => {
		const onHashChange = () => setHash(window.location.hash);
		window.addEventListener("hashchange", onHashChange);
		return () => window.removeEventListener("hashchange", onHashChange);
	}, []);
	const files = givenFiles ?? localFiles;
	const fileNames = new Intl.Collator(locale, {
		numeric: true,
		sensitivity: "base",
	});
	function sortedMembers(fileIds: readonly string[]) {
		return fileIds
			.map((id) => {
				const file = files[id];
				return {
					id,
					name: file?.name ?? "",
					...readSourceTrackOrder(file?.tags),
				};
			})
			.sort((a, b) => compareSourceFiles(a, b, "track", fileNames));
	}
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
			return false;
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
		return true;
	}
	function splitFile(sourceId: string, fileId: string) {
		const next = splitFileIntoNewGroup(state, files, sourceId, fileId);
		if (!next) return false;
		givenState ? onState?.(next) : setLocalState(next);
		return true;
	}
	function commitMove(sourceId: string, fileId: string, targetId: string) {
		const sourceRemains = Boolean(
			state.groups
				.find((group) => group.id === sourceId)
				?.fileIds.some((id) => id !== fileId),
		);
		queueGroupFocus(sourceRemains ? sourceId : targetId);
		if (!moveFile(sourceId, fileId, targetId)) pendingGroupFocus.current = null;
	}
	function commitSplit(sourceId: string, fileId: string) {
		queueGroupFocus(sourceId);
		if (!splitFile(sourceId, fileId)) pendingGroupFocus.current = null;
	}
	function queueGroupFocus(groupId: string) {
		pendingGroupFocus.current = {
			groupId,
			preventScroll: !document.activeElement?.matches(":focus-visible"),
		};
	}
	const groups = state.groups;
	useEffect(() => {
		const pending = pendingGroupFocus.current;
		if (!pending) return;
		pendingGroupFocus.current = null;
		groupDisclosureRefs.current
			.get(pending.groupId)
			?.focus({ preventScroll: pending.preventScroll });
	}, [groups]);
	const rows = incomingOverviewRows(
		groups,
		files,
		locale,
		roots.map((root) => root.path),
	);
	const overviewLabels = {
		caption: msg("overviewCaption"),
		group: msg("group"),
		filesPaths: msg("uniqueFilesPaths"),
		ready: msg("readyTotal"),
		status: msg("evaluationState"),
		empty: incomingGroupsEmpty(locale),
		number: msg("columnNumber"),
		title: msg("columnTitle"),
		artist: msg("columnArtist"),
		album: msg("columnAlbum"),
		actions: msg("columnActions"),
		noTitle: msg("missingFileTitle"),
		noValue: msg("noValue"),
		expand: (name: string) => msg("filesIn", { name }),
		openGroup: (name: string) => msg("openGroup", { name }),
	};
	const viewRows = rows.map(
		({
			group,
			name,
			memberCount,
			pathCount,
			readyCount,
			paths,
		}): IncomingGroupViewRow => {
			const opened = expanded.includes(group.id);
			const splitEnabled = canSplitGroup(group, files);
			const moveDestinations = rows
				.filter((destinationRow) => destinationRow.group.id !== group.id)
				.map((destinationRow) => ({
					id: destinationRow.group.id,
					name: destinationRow.name,
					paths: destinationRow.folderPaths,
				}));
			const fileRows = opened
				? sortedMembers(group.fileIds).map(({ id, trackNumber }) => {
						const file = files[id];
						const tag = (key: string) => file?.tags?.[key]?.join(" · ");
						return {
							id,
							trackNumber: trackNumber ?? tag("TRACKNUMBER"),
							title: tag("TITLE") || msg("missingFileTitle"),
							path: file?.path,
							artist: tag("ARTIST") || tag("ALBUMARTIST") || msg("noValue"),
							album: tag("ALBUM") || msg("noValue"),
							canMove: moveDestinations.length > 0,
							canCreateGroup: splitEnabled && usableFile(file),
						};
					})
				: [];
			return {
				id: group.id,
				name,
				secondary:
					artistName(group.artistId, artists) || msg("artistNotSpecified"),
				status: renderReason(group.reason),
				members: memberCount,
				paths: pathCount,
				ready: readyCount,
				matchingHref: `#match/${group.id}`,
				opened,
				files: fileRows,
				pathEvidence: paths,
				destinations: moveDestinations,
			};
		},
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
			<IncomingGroupsPanel locale={locale} count={viewRows.length}>
				<IncomingGroupsOverviewView
					labels={overviewLabels}
					rows={viewRows}
					onToggle={toggleGroup}
					onMove={(groupId, memberId, targetGroupId, focus) => {
						if (focus) commitMove(groupId, memberId, targetGroupId);
						else moveFile(groupId, memberId, targetGroupId);
					}}
					onSplit={(groupId, memberId) => commitSplit(groupId, memberId)}
					disclosureRef={(groupId, node) => {
						if (node) groupDisclosureRefs.current.set(groupId, node);
						else groupDisclosureRefs.current.delete(groupId);
					}}
				/>
			</IncomingGroupsPanel>
		</section>
	);
}
