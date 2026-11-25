import { useRef, useState } from "react";
import { useI18n } from "../../app/i18n/host";
import { LibrarySelectionDockView } from "../../ui/features/library/LibrarySelectionDockView";
import { LibrarySelectionReviewView } from "../../ui/features/library/LibrarySelectionReviewView";

export interface SelectedLibraryPosition {
  release_id: string;
  position_id: string;
  title: string;
  release_title: string;
}

export const libraryPositionKey = (
  position: Pick<SelectedLibraryPosition, "release_id" | "position_id">,
) => `${position.release_id}:${position.position_id}`;

export function LibrarySelectionDock({
  positions,
  hiddenCount,
  onDeselect,
  onClear,
  onPreview,
}: {
  positions: readonly SelectedLibraryPosition[];
  hiddenCount: number | undefined;
  onDeselect: (position: SelectedLibraryPosition) => void;
  onClear: () => void;
  onPreview: (positions: SelectedLibraryPosition[]) => void;
}) {
  const { locale } = useI18n();
  const [reviewOpen, setReviewOpen] = useState(false);
  const reviewTrigger = useRef<HTMLButtonElement>(null);
  return (
    <>
      {positions.length > 0 && (
        <LibrarySelectionDockView
          locale={locale}
          count={positions.length}
          hiddenCount={hiddenCount}
          review={{
            triggerRef: reviewTrigger,
            onOpen: () => setReviewOpen(true),
          }}
          onPreview={() => onPreview([...positions])}
          onClear={onClear}
        />
      )}
      <LibrarySelectionReviewView
        isOpen={reviewOpen}
        triggerRef={reviewTrigger}
        rows={positions.map((position) => ({
          id: libraryPositionKey(position),
          title: position.title,
          releaseTitle: position.release_title,
        }))}
        onClose={() => setReviewOpen(false)}
        onDeselect={(row) => {
          const position = positions.find(
            (item) => libraryPositionKey(item) === row.id,
          );
          if (position) onDeselect(position);
        }}
      />
    </>
  );
}
