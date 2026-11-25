import { type ReactNode, useRef } from "react";
import type { TextDropItem } from "react-aria-components";
import { Button, useDrag, useDrop } from "react-aria-components";
import {
  matchingBoardClass,
  matchingBoardClasses,
} from "./matchingBoardStyles";

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

type MatchingFileDragHandleProps = {
  groupId: string;
  fileId: string;
  name: string;
  available: boolean;
  label: string;
};

/**
 * Keyboard and mouse drag affordance for one incoming file. Keyboard users
 * activate the handle, move through drop targets, and confirm with Enter.
 */
export function MatchingFileDragHandle({
  groupId,
  fileId,
  name,
  available,
  label,
}: MatchingFileDragHandleProps) {
  const { dragProps, isDragging } = useDrag({
    getItems: () => [
      {
        [MATCHING_FILE_DRAG_TYPE]: JSON.stringify({ groupId, fileId }),
        "text/plain": name,
      },
    ],
    getAllowedDropOperations: () => ["copy"],
    isDisabled: !available,
  });

  return (
    <button
      {...dragProps}
      type="button"
      className={matchingBoardClasses(
        matchingBoardClass.dragButton,
        matchingBoardClass.focusRing,
      )}
      aria-label={label}
      disabled={!available}
      data-dragging={isDragging || undefined}
    >
      <svg
        aria-hidden="true"
        className={matchingBoardClass.dragHandleIcon}
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
  );
}

type MatchingDropZoneProps = {
  groupId: string;
  trackId: string;
  trackName: string;
  dropHint: string;
  dropTargetLabel: string;
  onAssign: (fileId: string) => boolean;
  disabled?: boolean;
  children: ReactNode;
};

/**
 * Drop target wrapper. `useDrop` registers the supplied focusable button for
 * keyboard drag and drop; the layout wrapper keeps the row contents intact.
 */
export function MatchingDropZone({
  groupId,
  trackId,
  trackName,
  dropHint,
  dropTargetLabel,
  onAssign,
  disabled = false,
  children,
}: MatchingDropZoneProps) {
  const targetRef = useRef<HTMLButtonElement>(null);
  const { dropProps, dropButtonProps, isDropTarget } = useDrop({
    ref: targetRef,
    hasDropButton: true,
    isDisabled: disabled,
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
      if (fileId) onAssign(fileId);
    },
  });

  return (
    <div
      {...dropProps}
      data-track-id={trackId}
      data-drop-target={isDropTarget || undefined}
      className={matchingBoardClass.positionDrop}
    >
      {children}
      <Button
        {...dropButtonProps}
        ref={targetRef}
        type="button"
        excludeFromTabOrder
        className="pointer-events-none absolute inset-0 z-20 m-0 h-full min-h-0 w-full rounded border-0 bg-transparent p-0 text-transparent opacity-0 focus-visible:outline-none"
        aria-label={dropTargetLabel}
      />
      {isDropTarget ? (
        <span aria-hidden="true" className={matchingBoardClass.dropHint}>
          {dropHint} · {trackName}
        </span>
      ) : null}
    </div>
  );
}
