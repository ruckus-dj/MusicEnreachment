import { useQuery } from "@tanstack/react-query";
import { previewLibraryPublicationSelection } from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import {
  type LibrarySelectionPreviewState,
  LibrarySelectionPreviewView,
  maxLibraryPreviewPositions,
} from "../../ui/features/library/LibrarySelectionPreviewView";
import {
  libraryPositionKey,
  type SelectedLibraryPosition,
} from "./LibrarySelectionDock";

export function LibrarySelectionPreview({
  positions,
  onClose,
}: {
  positions: SelectedLibraryPosition[];
  onClose: () => void;
}) {
  const { locale } = useI18n();
  const pairs = positions.map(({ release_id, position_id }) => ({
    release_id,
    position_id,
  }));
  const oversized = pairs.length > maxLibraryPreviewPositions;
  const preview = useQuery({
    queryKey: ["library-publication-selection-preview", pairs],
    enabled: !oversized,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const response = await previewLibraryPublicationSelection(
        { positions: pairs },
        { signal },
      );
      if (response.status !== 200)
        throw new Error("The library selection preview request failed.");
      const results = response.data.items ?? [];
      if (
        results.length !== pairs.length ||
        results.some(
          (row, index) =>
            libraryPositionKey(row) !== libraryPositionKey(pairs[index]),
        )
      ) {
        throw new Error(
          "The library selection preview response did not match the selected positions.",
        );
      }
      return results;
    },
  });
  // The modal is unmounted on close, aborting the request and discarding its results.
  const rows =
    !preview.isFetching && !preview.isError ? preview.data : undefined;
  const state: LibrarySelectionPreviewState = oversized
    ? { status: "limit" }
    : preview.isError
      ? { status: "error", onRetry: () => void preview.refetch() }
      : rows
        ? { status: "ready", rows }
        : { status: "loading" };
  return (
    <LibrarySelectionPreviewView
      locale={locale}
      positions={positions}
      state={state}
      onClose={onClose}
    />
  );
}
