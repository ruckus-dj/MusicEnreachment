import { type ReactNode, useEffect, useId, useMemo, useState } from "react";
import {
	Button as AriaButton,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
} from "react-aria-components";
import { MatchingDropZone, MatchingFileDragHandle } from "./MatchingDnD";
import { matchingBoardMessages } from "./matchingBoardMessages";
import {
	matchingBoardClass,
	matchingBoardClasses,
} from "./matchingBoardStyles";
import { readMatchingSourcePath } from "./matchingSourcePath";
import {
	matchingBoardAutomaticForRelease,
	matchingBoardPercent,
	matchingBoardSavedScore,
} from "./scoring";
import { compareSourceFiles, type SourceFileSort } from "./sourceFileOrder";
import type {
	MatchingWorkspaceAutomaticAssignments,
	MatchingWorkspaceLocale,
	MatchingWorkspacePositionContext,
	MatchingWorkspaceRelease,
	MatchingWorkspaceSavedReference,
	MatchingWorkspaceSavedScores,
	MatchingWorkspaceSource,
} from "./types";

export type MatchingBoardAutomaticAssignments =
	MatchingWorkspaceAutomaticAssignments;
export type MatchingBoardSavedScores = MatchingWorkspaceSavedScores;
export type MatchingBoardSource = MatchingWorkspaceSource;
export type MatchingBoardSavedReference = MatchingWorkspaceSavedReference;
export type MatchingBoardPositionContext = MatchingWorkspacePositionContext;

export type MatchingBoardProps = {
	groupId: string;
	release: MatchingWorkspaceRelease;
	members: readonly string[];
	/** Every group member, including already assigned files. */
	sources: readonly MatchingWorkspaceSource[];
	/** Manual draft assignments, member id to selected release track id. */
	assignments?: Readonly<Record<string, string>>;
	/**
	 * Optional additive track associations, track id to member ids. When given,
	 * occupants come from this map instead of `assignments`, so one member can
	 * appear on several positions. Removing always reports the owning track.
	 */
	associations?: Readonly<Record<string, readonly string[]>> | null;
	automaticAssignments?: MatchingWorkspaceAutomaticAssignments | null;
	savedScores?: MatchingWorkspaceSavedScores | null;
	disabled?: boolean;
	/** Close transient board popups while the host keeps the board mounted hidden. */
	hidden?: boolean;
	inspectedTrackId?: string;
	selectedSourceId?: string | null;
	defaultSelectedSourceId?: string | null;
	/** Explicit locale; the portable module never reads an app i18n provider. */
	locale: MatchingWorkspaceLocale;
	onSelectedSourceChange?: (memberId: string | null) => void;
	onAssign: (memberId: string, trackId: string) => void;
	onUnassign?: (memberId: string, trackId: string) => void;
	onInspectPosition?: (trackId: string) => void;
	onInspectSource?: (memberId: string) => void;
	/**
	 * Paired inspection of one file and one position. When provided it replaces
	 * the separate source/position callbacks so a single pair can be inspected
	 * without the two handlers racing each other.
	 */
	onInspectPair?: (memberId: string, trackId: string) => void;
	/** Per-position contextual content, for example a collapsed tag-override editor. */
	renderPositionMeta?: (context: MatchingWorkspacePositionContext) => ReactNode;
	renderSourceDetails?: (source: MatchingWorkspaceSource) => ReactNode;
	className?: string;
};

function sortedPositions(release: MatchingWorkspaceRelease) {
	return [...(release.positions ?? [])].sort(
		(left, right) =>
			left.medium - right.medium || left.position - right.position,
	);
}

function sourceOrderKey(source: MatchingWorkspaceSource) {
	return {
		name: source.fileName ?? source.label,
		discNumber: source.discNumber,
		trackNumber: source.trackNumber,
	};
}

export function MatchingBoard({
	groupId,
	release,
	members,
	sources,
	assignments,
	associations,
	automaticAssignments,
	savedScores,
	disabled = false,
	hidden,
	inspectedTrackId,
	selectedSourceId,
	defaultSelectedSourceId,
	locale,
	onSelectedSourceChange,
	onAssign,
	onUnassign,
	onInspectPosition,
	onInspectSource,
	onInspectPair,
	renderPositionMeta,
	renderSourceDetails,
	className,
}: MatchingBoardProps) {
	const copy = matchingBoardMessages[locale];
	const baseId = useId();
	const positionsTitleId = `${baseId}-positions`;
	const sourcesTitleId = `${baseId}-sources`;
	const [uncontrolledSelection, setUncontrolledSelection] = useState<
		string | null
	>(defaultSelectedSourceId ?? null);
	const [freeFileSort, setFreeFileSort] = useState<SourceFileSort>("track");
	const [sortOpen, setSortOpen] = useState(false);
	useEffect(() => {
		if (hidden) setSortOpen(false);
	}, [hidden]);
	const collator = useMemo(
		() => new Intl.Collator(locale, { numeric: true, sensitivity: "base" }),
		[locale],
	);
	const selectionControlled = selectedSourceId !== undefined;
	const selection = selectionControlled
		? (selectedSourceId ?? null)
		: uncontrolledSelection;

	function setSelection(next: string | null) {
		if (!selectionControlled) setUncontrolledSelection(next);
		onSelectedSourceChange?.(next);
	}

	const assignmentMap = assignments ?? {};
	const sourceById = new Map(sources.map((source) => [source.id, source]));
	const sourceFor = (memberId: string): MatchingWorkspaceSource =>
		sourceById.get(memberId) ?? { id: memberId, label: memberId };
	const positions = sortedPositions(release);
	const trackIds = new Set(positions.map((position) => position.track_id));
	const mediaCount = new Set(positions.map((position) => position.medium)).size;
	const automaticByMember = matchingBoardAutomaticForRelease(
		automaticAssignments,
		release,
	);
	const automaticByTrack = new Map<string, string[]>();
	for (const memberId of members) {
		const trackId = automaticByMember[memberId];
		if (!trackId || !trackIds.has(trackId)) continue;
		const current = automaticByTrack.get(trackId);
		if (current) current.push(memberId);
		else automaticByTrack.set(trackId, [memberId]);
	}

	function occupantsFor(trackId: string): readonly string[] {
		if (associations) return associations[trackId] ?? [];
		return members.filter((memberId) => assignmentMap[memberId] === trackId);
	}

	const associatedMembers = new Set<string>();
	if (associations) {
		for (const memberIds of Object.values(associations)) {
			for (const memberId of memberIds) associatedMembers.add(memberId);
		}
	}
	const freeMembers = members.filter((memberId) =>
		associations ? !associatedMembers.has(memberId) : !assignmentMap[memberId],
	);
	/*
	 * Sort a copy of the already resolved free rows. Associated/assigned rows,
	 * position order, ids, selection and drag handles are left untouched; the
	 * stable member ids keep focus on the same row while it moves.
	 */
	const freeMembersInOrder = [...freeMembers].sort((left, right) =>
		compareSourceFiles(
			sourceOrderKey(sourceFor(left)),
			sourceOrderKey(sourceFor(right)),
			freeFileSort,
			collator,
		),
	);
	const selectedSource = selection ? sourceById.get(selection) : undefined;
	const panelClass = matchingBoardClasses(matchingBoardClass.panel, className);

	function unassign(memberId: string, trackId: string) {
		if (onUnassign) onUnassign(memberId, trackId);
		else if (!associations) onAssign(memberId, "");
	}

	function inspectAt(memberId: string, trackId: string) {
		if (onInspectPair) {
			onInspectPair(memberId, trackId);
			return;
		}
		onInspectSource?.(memberId);
		onInspectPosition?.(trackId);
	}

	function scoreFor(memberId: string, trackId: string) {
		return matchingBoardSavedScore(savedScores, release, memberId, trackId);
	}

	return (
		<>
			<section
				data-matching-positions
				aria-labelledby={positionsTitleId}
				className={matchingBoardClasses(panelClass, "matching-board-positions")}
			>
				<h2 id={positionsTitleId} className={matchingBoardClass.heading}>
					{copy.positionsHeading(release.title)}
				</h2>
				<p className={matchingBoardClass.summary}>
					{release.tracklist_complete
						? copy.completeTrackList
						: copy.incompleteTrackList}{" "}
					· {copy.positionCount(positions.length)}
				</p>
				<p className={matchingBoardClass.instructions}>
					{copy.positionInstructions}
				</p>
				<ol className={matchingBoardClass.list}>
					{positions.map((position) => {
						const number =
							position.displayed_number || `${position.position + 1}`;
						const label = copy.positionLabel(
							position.medium + 1,
							number,
							position.title,
						);
						const occupants = occupantsFor(position.track_id);
						const automaticMembers =
							automaticByTrack.get(position.track_id) ?? [];
						const savedOnly = automaticMembers.filter(
							(memberId) => !occupants.includes(memberId),
						);
						const inspected = inspectedTrackId === position.track_id;
						const selectedAssignedHere =
							selection !== null && occupants.includes(selection);
						const selectedAvailable =
							selection !== null && selectedSource?.available !== false;
						const canAddSelected =
							Boolean(selection) &&
							selectedAvailable &&
							!selectedAssignedHere &&
							!disabled;
						const addLabel = !selection
							? copy.chooseSourceFirst
							: selectedAssignedHere
								? copy.alreadyAssignedHere(selectedSource?.label ?? "", label)
								: selectedSource
									? copy.assignSelectedTo(selectedSource.label, label)
									: copy.chooseSourceFirst;

						return (
							<li
								key={position.track_id}
								className={matchingBoardClasses(
									matchingBoardClass.listItem,
									matchingBoardClass.position,
								)}
								aria-label={label}
								data-inspected={inspected || undefined}
							>
								<MatchingDropZone
									groupId={groupId}
									trackId={position.track_id}
									trackName={label}
									dropHint={copy.dropHint}
									dropTargetLabel={copy.dropTarget(label)}
									disabled={disabled}
									onAssign={(fileId) => {
										onAssign(fileId, position.track_id);
										if (fileId === selection) setSelection(null);
										return true;
									}}
								>
									<div className={matchingBoardClass.positionHead}>
										{onInspectPosition ? (
											<button
												type="button"
												className={matchingBoardClasses(
													matchingBoardClass.positionTitle,
													matchingBoardClass.positionTitleButton,
													matchingBoardClass.focusRing,
												)}
												aria-pressed={inspected}
												aria-label={copy.inspectPosition(label)}
												title={position.title}
												onClick={() => onInspectPosition(position.track_id)}
											>
												<span className={matchingBoardClass.positionNumber}>
													{number}
												</span>
												<span className={matchingBoardClass.positionName}>
													{position.title}
												</span>
											</button>
										) : (
											<span
												className={matchingBoardClasses(
													matchingBoardClass.positionTitle,
													matchingBoardClass.positionTitleStatic,
												)}
											>
												<span className={matchingBoardClass.positionNumber}>
													{number}
												</span>
												<span className={matchingBoardClass.positionName}>
													{position.title}
												</span>
											</span>
										)}
										{mediaCount > 1 ? (
											<span className={matchingBoardClass.positionMedium}>
												{copy.disc(position.medium + 1)}
											</span>
										) : null}
										<button
											type="button"
											className={matchingBoardClasses(
												matchingBoardClass.addButton,
												matchingBoardClass.focusRing,
											)}
											disabled={!canAddSelected}
											aria-label={addLabel}
											title={addLabel}
											onClick={() => {
												if (!selection || !canAddSelected) return;
												onAssign(selection, position.track_id);
												setSelection(null);
											}}
										>
											{copy.addSelected}
										</button>
									</div>
									<div className={matchingBoardClass.assigned}>
										{occupants.map((memberId) => {
											const source = sourceFor(memberId);
											const confidence = scoreFor(memberId, position.track_id);
											const savedHere =
												automaticByMember[memberId] === position.track_id;
											return (
												<span
													key={memberId}
													className={matchingBoardClass.assignedFile}
												>
													<button
														type="button"
														className={matchingBoardClasses(
															matchingBoardClass.fileLabel,
															matchingBoardClass.fileLabelButton,
															matchingBoardClass.focusRing,
														)}
														title={source.path ?? source.label}
														aria-label={copy.inspectSource(source.label)}
														onClick={() =>
															inspectAt(memberId, position.track_id)
														}
													>
														{source.label}
													</button>
													{savedHere ? (
														<span className={matchingBoardClass.autoMarker}>
															{copy.savedAutomaticShort}
														</span>
													) : null}
													{confidence !== undefined ? (
														<span
															className={matchingBoardClass.score}
															title={copy.savedScoreLong(
																matchingBoardPercent(confidence),
															)}
														>
															{copy.savedScore(
																matchingBoardPercent(confidence),
															)}
														</span>
													) : null}
													<MatchingFileDragHandle
														groupId={groupId}
														fileId={memberId}
														name={source.label}
														available={!disabled && source.available !== false}
														label={copy.dragFile(source.label)}
													/>
													<button
														type="button"
														className={matchingBoardClasses(
															matchingBoardClass.removeButton,
															matchingBoardClass.focusRing,
														)}
														disabled={
															disabled || Boolean(associations && !onUnassign)
														}
														aria-label={copy.removeFile(source.label, label)}
														onClick={() =>
															unassign(memberId, position.track_id)
														}
													>
														×
													</button>
												</span>
											);
										})}
										{savedOnly.map((memberId) => {
											const source = sourceFor(memberId);
											const confidence = scoreFor(memberId, position.track_id);
											const content = (
												<>
													<span className={matchingBoardClass.autoMarker}>
														{copy.savedAutomatic}
													</span>
													<span className={matchingBoardClass.fileLabel}>
														{source.label}
													</span>
													{confidence !== undefined ? (
														<span
															className={matchingBoardClass.score}
															title={copy.savedScoreLong(
																matchingBoardPercent(confidence),
															)}
														>
															{copy.savedScore(
																matchingBoardPercent(confidence),
															)}
														</span>
													) : null}
												</>
											);
											return onInspectSource ||
												onInspectPosition ||
												onInspectPair ? (
												<button
													key={memberId}
													type="button"
													className={matchingBoardClasses(
														matchingBoardClass.savedFile,
														matchingBoardClass.savedFileButton,
														matchingBoardClass.focusRing,
													)}
													title={source.path ?? source.label}
													onClick={() => inspectAt(memberId, position.track_id)}
												>
													{content}
												</button>
											) : (
												<span
													key={memberId}
													className={matchingBoardClass.savedFile}
													title={source.path ?? source.label}
												>
													{content}
												</span>
											);
										})}
										{!occupants.length && !savedOnly.length ? (
											<span className={matchingBoardClass.empty}>
												{copy.noFilesAssigned}
											</span>
										) : null}
									</div>
									{renderPositionMeta?.({
										position,
										assigned: occupants.map(sourceFor),
										saved: automaticMembers.map((memberId) => ({
											memberId,
											trackId: position.track_id,
											confidence: scoreFor(memberId, position.track_id),
										})),
									})}
								</MatchingDropZone>
							</li>
						);
					})}
				</ol>
			</section>

			<section
				data-matching-sources
				aria-labelledby={sourcesTitleId}
				className={matchingBoardClasses(panelClass, "matching-board-sources")}
			>
				<h2 id={sourcesTitleId} className={matchingBoardClass.heading}>
					{copy.sourcesHeading(freeMembers.length)}
				</h2>
				<p className={matchingBoardClass.instructions}>
					{copy.sourcesInstructions}
				</p>
				<Select
					className="mt-1.5 flex min-w-0 flex-wrap items-center justify-between gap-x-2 gap-y-1"
					selectedKey={freeFileSort}
					isOpen={sortOpen && !hidden}
					onOpenChange={setSortOpen}
					isDisabled={freeMembers.length < 2}
					onSelectionChange={(key) => {
						if (key === "track" || key === "name") setFreeFileSort(key);
					}}
				>
					<Label className="text-xs uppercase leading-4 tracking-[0.03em] text-[var(--muted)]">
						{copy.sortLabel}
					</Label>
					<AriaButton
						className={matchingBoardClasses(
							"inline-flex min-h-[28px] min-w-[9rem] max-w-full cursor-pointer items-center justify-between gap-1 rounded border border-[var(--line)] bg-[var(--surface)] px-[7px] py-0 text-left text-xs font-[inherit] text-[var(--ink)] hover:border-[var(--control)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:min-h-10",
							matchingBoardClass.focusRing,
						)}
					>
						<SelectValue />
						<svg
							aria-hidden="true"
							viewBox="0 0 16 16"
							className="size-3 shrink-0"
							fill="none"
							stroke="currentColor"
							strokeWidth="1.5"
						>
							<path d="m4 6 4 4 4-4" />
						</svg>
					</AriaButton>
					<Popover className="z-30 max-h-[240px] min-w-[var(--trigger-width)] overflow-auto rounded border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]">
						<ListBox className="grid gap-0.5 text-xs outline-none">
							<ListBoxItem
								id="track"
								textValue={copy.sortByTrack}
								className="cursor-pointer rounded px-2 py-[5px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]"
							>
								{copy.sortByTrack}
							</ListBoxItem>
							<ListBoxItem
								id="name"
								textValue={copy.sortByName}
								className="cursor-pointer rounded px-2 py-[5px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]"
							>
								{copy.sortByName}
							</ListBoxItem>
						</ListBox>
					</Popover>
				</Select>
				<ul className={matchingBoardClass.list}>
					{freeMembersInOrder.map((memberId) => {
						const source = sourceFor(memberId);
						const pathParts = readMatchingSourcePath(source.path);
						const selected = selection === memberId;
						const available = !disabled && source.available !== false;
						return (
							<li key={memberId} className={matchingBoardClass.listItem}>
								<div
									className={matchingBoardClass.source}
									data-selected={selected || undefined}
								>
									<button
										type="button"
										className={matchingBoardClasses(
											matchingBoardClass.sourceSelect,
											matchingBoardClass.focusRing,
										)}
										aria-pressed={selected}
										aria-label={copy.selectSource(source.label)}
										disabled={!available}
										title={source.path ?? source.label}
										onClick={() => {
											if (selected) {
												setSelection(null);
												return;
											}
											setSelection(memberId);
											onInspectSource?.(memberId);
										}}
									>
										<span className="flex min-w-0 items-baseline text-xs leading-4">
											{pathParts?.parents.length ? (
												<span
													aria-hidden="true"
													dir="rtl"
													className="min-w-0 truncate text-[var(--muted)]"
												>
													<bdi dir="ltr">
														{pathParts.truncated ? "…/" : ""}
														{pathParts.parents.join("/")}/
													</bdi>
												</span>
											) : null}
											<strong className="min-w-0 max-w-full shrink-0 font-semibold [overflow-wrap:anywhere]">
												{pathParts?.fileName ?? source.fileName ?? source.label}
											</strong>
										</span>
										{source.detail ? (
											<span className={matchingBoardClass.sourceMeta}>
												{source.detail}
											</span>
										) : null}
										{source.available === false ? (
											<span className={matchingBoardClass.sourceMeta}>
												{copy.unavailable}
											</span>
										) : null}
									</button>
									<MatchingFileDragHandle
										groupId={groupId}
										fileId={memberId}
										name={source.label}
										available={available}
										label={copy.dragFile(source.label)}
									/>
								</div>
								{renderSourceDetails?.(source)}
							</li>
						);
					})}
				</ul>
				{freeMembers.length === 0 ? (
					<p className={matchingBoardClass.empty}>{copy.allAssigned}</p>
				) : null}
				<p
					role="status"
					className={matchingBoardClasses(
						"matching-board-source-status",
						matchingBoardClass.sourceStatus,
					)}
				>
					{selectedSource
						? copy.selectedSource(selectedSource.label)
						: copy.selectSourceHint}
				</p>
			</section>
		</>
	);
}
