import {
  defaultOperationsListQuery,
  isOperationsKindSelection,
  isOperationsSortColumn,
  isOperationsStatusFilter,
  type OperationsListQuery,
  type OperationsSortDirection,
} from "./operationsFilters";

/** Upper bound for a requested server offset, matching the portable list. */
const MAX_OPERATIONS_PAGE_OFFSET = 1_000_000;

/** Canonical, host-independent operations location. */
export type OperationsLocation = OperationsListQuery;

export const defaultOperationsLocation: OperationsLocation =
  defaultOperationsListQuery;

function isOperationsSortDirection(
  value: string,
): value is OperationsSortDirection {
  return value === "asc" || value === "desc";
}

/**
 * Reads `#/operations` using the product query names and defaults. Other
 * routes and malformed values fall back to the default location.
 */
export function parseOperationsLocation(hash: string): OperationsLocation {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/operations") return defaultOperationsLocation;
  const params = new URLSearchParams(search);
  const requestedStatus = params.get("status") ?? "";
  const requestedKind = params.get("kind") ?? "";
  const requestedSort = params.get("sort") ?? "";
  const requestedDirection = params.get("direction") ?? "";
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    status: isOperationsStatusFilter(requestedStatus)
      ? requestedStatus
      : defaultOperationsLocation.status,
    kind: isOperationsKindSelection(requestedKind)
      ? requestedKind
      : defaultOperationsLocation.kind,
    sort: isOperationsSortColumn(requestedSort)
      ? requestedSort
      : defaultOperationsLocation.sort,
    direction: isOperationsSortDirection(requestedDirection)
      ? requestedDirection
      : defaultOperationsLocation.direction,
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? Math.min(requestedOffset, MAX_OPERATIONS_PAGE_OFFSET)
        : defaultOperationsLocation.offset,
  };
}

/** Produces the canonical hash, omitting values that equal their defaults. */
export function operationsLocationHref(location: OperationsLocation) {
  const params = new URLSearchParams();
  if (location.status !== defaultOperationsLocation.status)
    params.set("status", location.status);
  if (location.kind !== defaultOperationsLocation.kind)
    params.set("kind", location.kind);
  if (location.sort !== defaultOperationsLocation.sort)
    params.set("sort", location.sort);
  if (location.direction !== defaultOperationsLocation.direction)
    params.set("direction", location.direction);
  if (location.offset > 0)
    params.set(
      "offset",
      String(Math.min(location.offset, MAX_OPERATIONS_PAGE_OFFSET)),
    );
  const search = params.toString();
  return search ? `#/operations?${search}` : "#/operations";
}
