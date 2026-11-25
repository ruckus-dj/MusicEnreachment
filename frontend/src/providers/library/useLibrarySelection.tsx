import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { resolveLibrarySelection } from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import type { ArtistCardSelectionControl } from "../../ui/features/library/LibraryArtistCards";
import {
  libraryPositionKey,
  type SelectedLibraryPosition,
} from "./LibrarySelectionDock";

const noPositions: SelectedLibraryPosition[] = [];

export type LibrarySelectionScope =
  | { kind: "page" }
  | { kind: "artist"; id: string }
  | { kind: "release"; id: string }
  | { kind: "position"; position: SelectedLibraryPosition };

export function useLibrarySelection({
  artistIDs = [],
  releaseIDs = [],
  trackPositions,
  pageReady,
}: {
  artistIDs?: string[];
  releaseIDs?: string[];
  trackPositions?: SelectedLibraryPosition[];
  pageReady: boolean;
}) {
  const { locale } = useI18n();
  const [selected, setSelected] = useState<
    Map<string, SelectedLibraryPosition>
  >(() => new Map());
  const [frozen, setFrozen] = useState<SelectedLibraryPosition[]>();
  const hasParents = artistIDs.length + releaseIDs.length > 0;
  const membership = useQuery({
    queryKey: ["library-selection-membership", artistIDs, releaseIDs],
    enabled: pageReady && hasParents,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await resolveLibrarySelection(
        { artist_ids: artistIDs, release_ids: releaseIDs },
        { signal },
      );
      if (response.status !== 200) {
        throw new Error(String(response.status));
      }
      return response.data.positions;
    },
  });
  const pagePositions = trackPositions ?? membership.data ?? noPositions;
  const pageKeys = new Set(pagePositions.map(libraryPositionKey));
  const known =
    pageReady && (!hasParents || (!!membership.data && !membership.isError));
  const positions = [...selected.values()];

  function select(items: readonly SelectedLibraryPosition[], value: boolean) {
    setSelected((current) => {
      const next = new Map(current);
      for (const item of items) {
        const key = libraryPositionKey(item);
        if (value) next.set(key, item);
        else next.delete(key);
      }
      return next;
    });
  }

  function itemsFor(
    scope: LibrarySelectionScope,
  ): readonly SelectedLibraryPosition[] {
    switch (scope.kind) {
      case "page":
        return pagePositions;
      case "artist":
        return (
          membership.data?.filter((row) => row.artist_ids.includes(scope.id)) ??
          noPositions
        );
      case "release":
        return pagePositions.filter((row) => row.release_id === scope.id);
      case "position":
        return [scope.position];
    }
  }

  function selectionControl(
    scope: LibrarySelectionScope,
  ): ArtistCardSelectionControl {
    const items = itemsFor(scope);
    const count = items.filter((item) =>
      selected.has(libraryPositionKey(item)),
    ).length;
    return {
      isSelected: count > 0 && count === items.length,
      isIndeterminate: count > 0 && count < items.length,
      isDisabled: items.length === 0 || (scope.kind !== "position" && !known),
      onChange: (value) => select(items, value),
    };
  }

  const membershipError =
    pageReady && hasParents && membership.isError
      ? membership.error.message === "409"
        ? locale === "ru"
          ? "На странице слишком много позиций для общего выбора. Уточните поиск или выберите отдельные треки."
          : "This page has too many positions to select together. Refine the search or select individual tracks."
        : locale === "ru"
          ? "Не удалось определить позиции на странице."
          : "Could not determine this page’s positions."
      : undefined;

  return {
    selectionControl,
    positions,
    hiddenCount: known
      ? positions.filter(
          (position) => !pageKeys.has(libraryPositionKey(position)),
        ).length
      : undefined,
    deselect: (position: SelectedLibraryPosition) => select([position], false),
    clear: () => setSelected(new Map()),
    frozen,
    setFrozen,
    membershipError,
    retryMembership: () => void membership.refetch(),
  };
}
