import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { resolveLibrarySelection } from "../../api/generated/client";
import { useI18n } from "../../i18n/I18nProvider";
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

  function renderCheckbox(scope: LibrarySelectionScope, label: string) {
    let items: readonly SelectedLibraryPosition[];
    switch (scope.kind) {
      case "page":
        items = pagePositions;
        break;
      case "artist":
        items =
          membership.data?.filter((row) => row.artist_ids.includes(scope.id)) ??
          noPositions;
        break;
      case "release":
        items = pagePositions.filter((row) => row.release_id === scope.id);
        break;
      case "position":
        items = [scope.position];
        break;
    }
    const count = items.filter((item) =>
      selected.has(libraryPositionKey(item)),
    ).length;
    return (
      <Checkbox
        slot={null}
        aria-label={label}
        isSelected={count > 0 && count === items.length}
        isIndeterminate={count > 0 && count < items.length}
        isDisabled={items.length === 0 || (scope.kind !== "position" && !known)}
        onChange={(value) => select(items, value)}
        className="group inline-flex size-6 shrink-0 cursor-pointer items-center justify-center data-[disabled]:cursor-default data-[disabled]:opacity-45"
      >
        <span
          aria-hidden="true"
          className="grid size-4 place-items-center rounded-sm border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-offset-2 group-data-[focus-visible]:outline-[var(--focus)] group-data-[indeterminate]:bg-[var(--button)] group-data-[selected]:bg-[var(--button)]"
        >
          <svg
            aria-hidden="true"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            className="size-3.5"
          >
            <path
              d="M3 8l3 3 7-7"
              className="hidden group-data-[selected]:block"
            />
            <path
              d="M3 8h10"
              className="hidden group-data-[indeterminate]:block"
            />
          </svg>
        </span>
      </Checkbox>
    );
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
    renderCheckbox,
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
