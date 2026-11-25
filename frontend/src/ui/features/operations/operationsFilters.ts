import type { OperationsListSortColumn } from "./OperationsList";

export const operationsStatusFilters = [
  "attention",
  "all",
  "failed",
  "running",
  "queued",
] as const;

export const operationsKindFilters = [
  "publication",
  "analysis",
  "removal",
  "matching",
] as const;

const sortColumns: readonly OperationsListSortColumn[] = [
  "target",
  "operation",
  "state",
  "created",
];

export type OperationsStatusFilter = (typeof operationsStatusFilters)[number];
export type OperationsKindFilter = (typeof operationsKindFilters)[number];
export type OperationsKindSelection = OperationsKindFilter | "all";
export type OperationsSortColumn = OperationsListSortColumn;
export type OperationsSortDirection = "asc" | "desc";

export function isOperationsStatusFilter(
  value: string,
): value is OperationsStatusFilter {
  return operationsStatusFilters.some((candidate) => candidate === value);
}

export function isOperationsKindSelection(
  value: string,
): value is OperationsKindSelection {
  return (
    value === "all" ||
    operationsKindFilters.some((candidate) => candidate === value)
  );
}

export function isOperationsSortColumn(
  value: string,
): value is OperationsSortColumn {
  return sortColumns.some((candidate) => candidate === value);
}
