import { useRef, useState, type ComponentRef } from "react";
import { DragPreview, useDrag } from "react-aria/useDrag";
import { useDrop } from "react-aria/useDrop";
import type { TextDropItem } from "react-aria/useDrop";
import { Button } from "./ui";
import { matchingMessage } from "./matchingMessages";
import type { Locale } from "./i18n/types";

export const MATCHING_FILE_DRAG_TYPE = "application/x-melotrove-matching-file";

export function decodeMatchingFilePayload(
	value: string,
	groupId: string,
): string | null {
	try {
		const payload: unknown = JSON.parse(value);
		if (
			typeof payload !== "object" ||
			payload === null ||
			!("groupId" in payload) ||
			!("fileId" in payload) ||
			payload.groupId !== groupId ||
			typeof payload.fileId !== "string" ||
			!payload.fileId
		) {
			return null;
		}
		return payload.fileId;
	} catch {
		return null;
	}
}

type MatchingFileChipProps = {
	groupId: string;
	fileId: string;
	name: string;
	codec: string;
	duration: string;
	path?: string;
	selected: boolean;
	available: boolean;
	compact?: boolean;
	locale?: Locale;
	onSelect: () => void;
};

export function MatchingFileChip({
	groupId,
	fileId,
	name,
	codec,
	duration,
	path,
	selected,
	available,
	compact = false,
	locale = "ru",
	onSelect,
}: MatchingFileChipProps) {
	const preview = useRef<ComponentRef<typeof DragPreview>>(null);
	const { dragProps, dragButtonProps, isDragging } = useDrag({
		hasDragButton: true,
		preview,
		isDisabled: !available,
		getAllowedDropOperations: () => ["copy"],
		getItems: () => [
			{
				[MATCHING_FILE_DRAG_TYPE]: JSON.stringify({ groupId, fileId }),
				"text/plain": name,
			},
		],
	});

	return (
		<>
			<div
				{...dragProps}
				className={`grid min-w-0 grid-cols-[minmax(0,1fr)_28px] items-center gap-1 ${isDragging ? "opacity-50" : ""}`}
			>
				<button
					type="button"
					className={`min-w-0 rounded border px-2 text-left ${compact ? "h-7" : "min-h-[44px] py-1.5"} ${selected ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--surface)] hover:border-[var(--accent)] hover:bg-[var(--accent-soft)]"} focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)]`}
					aria-pressed={selected}
					onClick={onSelect}
				>
					<strong className="block truncate text-[11px]">{name}</strong>
					{!compact && (
						<span className="block truncate text-[10px] text-[var(--muted)]">
							{codec} · {duration}
							{path ? ` · ${path}` : ""}
						</span>
					)}
				</button>
				<Button
					{...dragButtonProps}
					type="button"
					className="inline-flex h-7 w-7 items-center justify-center rounded border border-[var(--line)] bg-[var(--raised)] text-[11px] text-[var(--muted)] hover:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
					aria-label={matchingMessage(locale, "dragHandle", { name })}
					isDisabled={!available}
				>
					<svg
						aria-hidden="true"
						className="size-4"
						viewBox="0 0 16 16"
						fill="currentColor"
					>
						<circle cx="5" cy="4" r="1" />
						<circle cx="11" cy="4" r="1" />
						<circle cx="5" cy="8" r="1" />
						<circle cx="11" cy="8" r="1" />
						<circle cx="5" cy="12" r="1" />
						<circle cx="11" cy="12" r="1" />
					</svg>
				</Button>
			</div>
			<DragPreview ref={preview}>
				{(items) => (
					<div className="max-w-[min(320px,80vw)] rounded border border-[var(--accent)] bg-[var(--surface)] px-3 py-2 text-[12px] text-[var(--ink)] shadow-[0_8px_24px_#0003]">
						<strong className="block truncate">{name}</strong>
						<span className="block text-[10px] text-[var(--muted)]">
							{matchingMessage(locale, "previewLabel", {
								name: String(items[0]?.["text/plain"] ?? name),
							})}
						</span>
					</div>
				)}
			</DragPreview>
		</>
	);
}

export function MatchingDragHandle({
	groupId,
	fileId,
	name,
	available,
	locale = "ru",
}: {
	groupId: string;
	fileId: string;
	name: string;
	available: boolean;
	locale?: Locale;
}) {
	const preview = useRef<ComponentRef<typeof DragPreview>>(null);
	const { dragProps, isDragging } = useDrag({
		preview,
		isDisabled: !available,
		getAllowedDropOperations: () => ["copy"],
		getItems: () => [
			{
				[MATCHING_FILE_DRAG_TYPE]: JSON.stringify({ groupId, fileId }),
				"text/plain": name,
			},
		],
	});

	return (
		<>
			<button
				{...dragProps}
				type="button"
				className="inline-flex h-7 w-7 items-center justify-center rounded border border-[var(--line)] bg-[var(--raised)] text-[11px] text-[var(--muted)] hover:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
				aria-label={matchingMessage(locale, "dragHandle", { name })}
				disabled={!available}
				data-dragging={isDragging || undefined}
			>
				<svg
					aria-hidden="true"
					className="size-4"
					viewBox="0 0 16 16"
					fill="currentColor"
				>
					<circle cx="5" cy="4" r="1" />
					<circle cx="11" cy="4" r="1" />
					<circle cx="5" cy="8" r="1" />
					<circle cx="11" cy="8" r="1" />
					<circle cx="5" cy="12" r="1" />
					<circle cx="11" cy="12" r="1" />
				</svg>
			</button>
			<DragPreview ref={preview}>
				{(items) => (
					<div className="max-w-[min(320px,80vw)] rounded border border-[var(--accent)] bg-[var(--surface)] px-3 py-2 text-[12px] text-[var(--ink)] shadow-[0_8px_24px_#0003]">
						<strong className="block truncate">{name}</strong>
						<span className="block text-[10px] text-[var(--muted)]">
							{matchingMessage(locale, "previewLabel", {
								name: String(items[0]?.["text/plain"] ?? name),
							})}
						</span>
					</div>
				)}
			</DragPreview>
		</>
	);
}

type MatchingDropZoneProps = {
	groupId: string;
	trackId: string;
	trackName: string;
	selectedFileId: string | null;
	selectedFileName: string | null;
	locale?: Locale;
	onAssign: (fileId: string) => boolean;
};

export function MatchingDropZone({
	groupId,
	trackId,
	trackName,
	selectedFileId,
	selectedFileName,
	locale = "ru",
	onAssign,
}: MatchingDropZoneProps) {
	// DragManager focuses the registered element during keyboard navigation.
	const targetRef = useRef<HTMLButtonElement>(null);
	const [accepted, setAccepted] = useState(false);
	const { dropProps, dropButtonProps, isDropTarget } = useDrop({
		ref: targetRef,
		hasDropButton: true,
		getDropOperation: (types, allowedOperations) =>
			types.has(MATCHING_FILE_DRAG_TYPE) && allowedOperations.includes("copy")
				? "copy"
				: "cancel",
		async onDrop(event) {
			const item = event.items.find(
				(candidate): candidate is TextDropItem =>
					candidate.kind === "text" &&
					candidate.types.has(MATCHING_FILE_DRAG_TYPE),
			);
			if (!item) return;
			const fileId = decodeMatchingFilePayload(
				await item.getText(MATCHING_FILE_DRAG_TYPE),
				groupId,
			);
			if (!fileId || !onAssign(fileId)) return;
			setAccepted(true);
			window.setTimeout(() => setAccepted(false), 450);
		},
	});
	const label = selectedFileName ?? matchingMessage(locale, "dropHint");

	return (
		<div
			{...dropProps}
			data-track-id={trackId}
			className={`flex h-[34px] min-w-0 items-center gap-1 rounded border border-dashed px-1.5 ${isDropTarget ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--raised)]"} motion-safe:transition-colors motion-safe:duration-150 ${accepted ? "motion-safe:animate-pulse [animation-duration:420ms] [animation-iteration-count:1] motion-reduce:animate-none" : ""}`}
		>
			<Button
				{...dropButtonProps}
				ref={targetRef}
				type="button"
				className="min-h-0 h-full min-w-0 flex-1 truncate rounded-sm px-1 text-left text-[10px] text-[var(--muted)] pointer-coarse:min-h-0 hover:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--accent)]"
				aria-label={matchingMessage(locale, "ariaDropTarget", {
					track: trackName,
				})}
			>
				{label}
			</Button>
			<Button
				type="button"
				className="h-full min-h-0 px-2 py-0 text-[10px] pointer-coarse:min-h-0"
				aria-label={`${matchingMessage(locale, "assignSelected")}${selectedFileName ? `: ${selectedFileName}` : ` · ${matchingMessage(locale, "chooseFile")}`}`}
				isDisabled={!selectedFileId}
				onPress={() => {
					if (selectedFileId) onAssign(selectedFileId);
				}}
			>
				{matchingMessage(locale, "assign")}
			</Button>
		</div>
	);
}
