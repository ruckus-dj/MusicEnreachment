import type { Ref } from "react";
import type { SortDescriptor } from "react-aria-components";
import {
  MatchingRecheckAbandonDialog,
  type MatchingRecheckAbandonPosition,
} from "./MatchingRecheckAbandonDialog";
import {
  type OperationsActionError,
  OperationsList,
  type OperationsListFilterOption,
  type OperationsListRow,
  type OperationsListStatus,
} from "./OperationsList";
import {
  isOperationsKindSelection,
  isOperationsSortColumn,
  isOperationsStatusFilter,
  type OperationsKindSelection,
  type OperationsSortColumn,
  type OperationsSortDirection,
  type OperationsStatusFilter,
  operationsKindFilters,
  operationsStatusFilters,
} from "./operationsFilters";
import {
  type OperationsScreenCopy,
  operationsScreenCopy,
} from "./operationsScreenCopy";

/**
 * Portable operations screen composition.
 *
 * The list, details dialog and row actions already come from the shared
 * `OperationsList`. This module adds the remaining screen-level presentation
 * both hosts render identically: status/kind filter options, sortable headers,
 * the pagination range label and the matching recheck abandonment modal.
 *
 * Hosts keep their own typed adapters. The application keeps server scope
 * (`status`, `kind`, `q`, `sort`, `direction`, `locale`, `offset`) and server
 * rows/total; the demo keeps its in-memory rows, local filtering and demo
 * settlement. Neither adapter passes JSX into this component.
 */

export interface OperationsScreenFilters {
  query: string;
  onQueryChange: (query: string) => void;
  status: OperationsStatusFilter;
  onStatusChange: (status: OperationsStatusFilter) => void;
  kind: OperationsKindSelection;
  onKindChange: (kind: OperationsKindSelection) => void;
  /** Resets query, status and kind in one host update. */
  onResetFilters?: () => void;
}

export interface OperationsScreenPagination {
  /** First row number on the page, one-based. */
  from: number;
  /** Last row number on the page, one-based and inclusive. */
  to: number;
  total: number;
  onPrevious?: () => void;
  onNext?: () => void;
}

/** Snapshot and actions for abandoning one failed matching recheck member. */
export interface OperationsScreenMatchingRecheckAbandon {
  positions: MatchingRecheckAbandonPosition[];
  busy: boolean;
  error?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export interface OperationsScreenViewProps {
  locale: "en" | "ru";
  rows: OperationsListRow[];
  status?: OperationsListStatus;
  /** Safe failure text for the error state. */
  error?: string;
  /** Feedback for the latest completed action. */
  notice?: string;
  /** Feedback for a failed action. */
  actionError?: OperationsActionError;
  headingRef?: Ref<HTMLHeadingElement>;
  onRefresh?: () => void;
  isRefreshing?: boolean;
  filters: OperationsScreenFilters;
  sortColumn: OperationsSortColumn;
  sortDirection: OperationsSortDirection;
  onSortChange: (
    column: OperationsSortColumn,
    direction: OperationsSortDirection,
  ) => void;
  pagination?: OperationsScreenPagination;
  matchingRecheckAbandon?: OperationsScreenMatchingRecheckAbandon;
}

function paginationLabel(
  copy: OperationsScreenCopy,
  locale: "en" | "ru",
  pagination: OperationsScreenPagination,
) {
  const format = new Intl.NumberFormat(locale);
  return copy.paginationRange
    .replace("{from}", format.format(pagination.from))
    .replace("{to}", format.format(pagination.to))
    .replace("{total}", format.format(pagination.total));
}

export function OperationsScreenView({
  locale,
  rows,
  status,
  error,
  notice,
  actionError,
  headingRef,
  onRefresh,
  isRefreshing,
  filters,
  sortColumn,
  sortDirection,
  onSortChange,
  pagination,
  matchingRecheckAbandon,
}: OperationsScreenViewProps) {
  const copy = operationsScreenCopy[locale];
  const statusOptions: OperationsListFilterOption[] =
    operationsStatusFilters.map((value) => ({
      value,
      label:
        value === "attention"
          ? copy.attention
          : value === "all"
            ? copy.allStatuses
            : copy[value],
    }));
  const kindOptions: OperationsListFilterOption[] = [
    { value: "all", label: copy.allKinds },
    ...operationsKindFilters.map((value) => ({
      value,
      label:
        value === "publication"
          ? copy.kindPublication
          : value === "analysis"
            ? copy.kindAnalysis
            : value === "removal"
              ? copy.kindRemoval
              : copy.kindMatching,
    })),
  ];
  const sortDescriptor: SortDescriptor = {
    column: sortColumn,
    direction: sortDirection === "asc" ? "ascending" : "descending",
  };

  return (
    <>
      <OperationsList
        copy={copy}
        locale={locale}
        rows={rows}
        status={status}
        error={error}
        notice={notice}
        actionError={actionError}
        headingRef={headingRef}
        onRefresh={onRefresh}
        isRefreshing={isRefreshing}
        sortDescriptor={sortDescriptor}
        onSortChange={({ column, direction }) => {
          if (typeof column !== "string" || !isOperationsSortColumn(column))
            return;
          onSortChange(column, direction === "ascending" ? "asc" : "desc");
        }}
        filters={{
          query: filters.query,
          onQueryChange: filters.onQueryChange,
          status: filters.status,
          statusOptions,
          onStatusChange: (value) => {
            if (isOperationsStatusFilter(value)) filters.onStatusChange(value);
          },
          kind: filters.kind,
          kindOptions,
          onKindChange: (value) => {
            if (isOperationsKindSelection(value)) filters.onKindChange(value);
          },
          onResetFilters: filters.onResetFilters,
        }}
        pagination={
          pagination && pagination.total > 0 && pagination.from > 0
            ? {
                label: paginationLabel(copy, locale, pagination),
                onPrevious: pagination.onPrevious,
                onNext: pagination.onNext,
              }
            : undefined
        }
      />
      {matchingRecheckAbandon && (
        <MatchingRecheckAbandonDialog
          locale={locale}
          positions={matchingRecheckAbandon.positions}
          busy={matchingRecheckAbandon.busy}
          error={matchingRecheckAbandon.error}
          onConfirm={matchingRecheckAbandon.onConfirm}
          onCancel={matchingRecheckAbandon.onCancel}
        />
      )}
    </>
  );
}
