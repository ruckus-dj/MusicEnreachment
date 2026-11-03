import { useRef, useState, type ComponentRef, type ReactNode } from "react";
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
	locale?: Locale;
	onAssign: (fileId: string) => boolean;
	children: ReactNode;
};

export function MatchingDropZone({
	groupId,
	trackId,
	trackName,
	locale = "ru",
	onAssign,
	children,
}: MatchingDropZoneProps) {
	// DragManager focuses this registered target during keyboard DnD; it is not
	// an ordinary tab stop.
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
	return (
		<div
			{...dropProps}
			data-track-id={trackId}
			className={`relative flex h-full min-w-0 flex-col gap-1 motion-safe:transition-colors motion-safe:duration-150 motion-reduce:transition-none ${accepted ? "motion-safe:animate-pulse [animation-duration:420ms] [animation-iteration-count:1] motion-reduce:animate-none" : ""}`}
		>
			{children}
			<Button
				{...dropButtonProps}
				ref={targetRef}
				type="button"
				excludeFromTabOrder
				className="pointer-events-none absolute inset-0 z-20 h-full min-h-0 w-full rounded border-0 bg-transparent p-0 text-transparent opacity-0 focus-visible:outline-none"
				aria-label={matchingMessage(locale, "ariaDropTarget", {
					track: trackName,
				})}
			/>
			{isDropTarget && (
				<span
					aria-hidden="true"
					className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded border border-dashed border-[var(--accent)] bg-[var(--accent-soft)] px-2 text-center text-[10px] font-semibold text-[var(--accent)]"
				>
					{matchingMessage(locale, "dropHint")}
				</span>
			)}
		</div>
	);
}
