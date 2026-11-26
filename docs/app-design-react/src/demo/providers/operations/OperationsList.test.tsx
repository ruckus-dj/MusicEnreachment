import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import {
  OperationsList,
  type OperationsListCopy,
  type OperationsListRow,
} from "../../../ui/features/operations/OperationsList";

const copy: OperationsListCopy = {
  title: "Current operations",
  description: "Work in progress and errors that need attention.",
  refresh: "Refresh",
  refreshing: "Refreshing…",
  loading: "Loading operations…",
  loadError: "Could not load operations.",
  retryLoad: "Retry loading",
  emptyTitle: "No current operations",
  emptyDescription: "Work in progress will appear here.",
  target: "Target",
  operation: "Operation",
  stateStage: "Status / stage",
  progress: "Progress",
  created: "Created",
  error: "Error",
  actions: "Actions",
  footnote: "Completed operations are hidden.",
  search: "Search by target",
  statusFilter: "Status",
  kindFilter: "Operation type",
  details: "Details",
  close: "Close",
  previous: "Previous",
  next: "Next",
};

const row: OperationsListRow = {
  id: "publication-1",
  target: "Northern Lines · position 01",
  targetHref: "#/library/releases/north/positions/north-t1",
  operation: "Publish position",
  state: "Running",
  stateTone: "info",
  stage: "Preparing result",
  progress: "42%",
  created: "12:04",
  error: "EACCES: no write access to the temporary directory",
  actions: [{ id: "dismiss", label: "Dismiss", onPress: vi.fn() }],
};

it("renders an operation row and forwards row actions and refresh", async () => {
  const onRefresh = vi.fn();
  render(<OperationsList copy={copy} rows={[row]} onRefresh={onRefresh} />);
  expect(
    screen.getByRole("link", { name: "Northern Lines · position 01" }),
  ).toHaveAttribute("href", "#/library/releases/north/positions/north-t1");
  expect(screen.getByText("Preparing result")).toBeVisible();
  expect(screen.queryByText("42%")).not.toBeInTheDocument();
  expect(screen.queryByText(row.error ?? "")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("columnheader", { name: /progress|error/i }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("12:04")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: /Details/ }));
  const details = screen.getByRole("dialog");
  expect(details).toHaveTextContent("42%");
  expect(details).toHaveTextContent(
    "EACCES: no write access to the temporary directory",
  );
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  fireEvent.click(
    screen.getByRole("button", { name: /Actions: Northern Lines/ }),
  );
  fireEvent.click(await screen.findByRole("menuitem", { name: "Dismiss" }));
  expect(row.actions?.[0]?.onPress).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  expect(onRefresh).toHaveBeenCalledTimes(1);
});

it("shows only nondefault filters and restores the canonical defaults", () => {
  const statusOptions = [
    { value: "attention", label: "Needs attention" },
    { value: "all", label: "All" },
    { value: "queued", label: "Queued" },
  ];
  const kindOptions = [
    { value: "all", label: "All kinds" },
    { value: "publication", label: "Publication" },
  ];
  const onResetFilters = vi.fn();

  function FilterHarness() {
    const [query, setQuery] = useState("north");
    const [status, setStatus] = useState("all");
    const [kind, setKind] = useState("publication");
    const reset = () => {
      setQuery("");
      setStatus("attention");
      setKind("all");
      onResetFilters();
    };
    return (
      <OperationsList
        copy={copy}
        locale="en"
        rows={[]}
        filters={{
          query,
          onQueryChange: setQuery,
          status,
          statusOptions,
          onStatusChange: setStatus,
          kind,
          kindOptions,
          onKindChange: setKind,
          onResetFilters: reset,
        }}
      />
    );
  }

  render(<FilterHarness />);
  expect(screen.getByText("Search: north")).toBeVisible();
  expect(screen.getByText("Status: All")).toBeVisible();
  expect(screen.getByText("Kind: Publication")).toBeVisible();
  expect(screen.queryByText("Status: Needs attention")).toBeNull();
  expect(screen.queryByText("Kind: All kinds")).toBeNull();

  const statusTag = screen
    .getByText("Status: All")
    .closest<HTMLElement>('[role="row"]');
  if (!statusTag) throw new Error("Missing status filter tag");
  fireEvent.click(within(statusTag).getByRole("button"));
  expect(screen.queryByText("Status: All")).toBeNull();
  expect(screen.getByText("Search: north")).toBeVisible();
  expect(screen.getByText("Kind: Publication")).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "Reset all" }));
  expect(onResetFilters).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("Search: north")).toBeNull();
  expect(screen.queryByText("Kind: Publication")).toBeNull();
  expect(screen.queryByRole("button", { name: "Reset all" })).toBeNull();
  expect(
    screen.getByRole("searchbox", { name: "Search by target" }),
  ).toHaveFocus();
});

it("returns focus to the query input after the last chip is removed", () => {
  function LastChipHarness() {
    const [query, setQuery] = useState("north");
    return (
      <OperationsList
        copy={copy}
        locale="en"
        rows={[]}
        filters={{
          query,
          onQueryChange: setQuery,
          status: "attention",
          statusOptions: [{ value: "attention", label: "Needs attention" }],
          onStatusChange: vi.fn(),
          kind: "all",
          kindOptions: [{ value: "all", label: "All kinds" }],
          onKindChange: vi.fn(),
        }}
      />
    );
  }

  render(<LastChipHarness />);
  const input = screen.getByRole("searchbox", { name: "Search by target" });
  const tag = screen
    .getByText("Search: north")
    .closest<HTMLElement>('[role="row"]');
  if (!tag) throw new Error("Missing query filter tag");
  fireEvent.click(within(tag).getByRole("button"));

  expect(input).toHaveFocus();
  expect(screen.queryByText("Search: north")).toBeNull();
});

it("shows loading, error and empty states instead of rows", () => {
  const onRefresh = vi.fn();
  const { rerender } = render(
    <OperationsList copy={copy} rows={[]} status="loading" />,
  );
  expect(screen.getByText("Loading operations…")).toBeVisible();
  rerender(
    <OperationsList
      copy={copy}
      rows={[]}
      status="error"
      error="Operations API is unavailable."
      onRefresh={onRefresh}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Operations API is unavailable.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry loading" }));
  expect(onRefresh).toHaveBeenCalledTimes(1);
  rerender(
    <OperationsList
      copy={copy}
      rows={[row]}
      status="error"
      isRefreshing
      onRefresh={onRefresh}
      actionError={{
        rowId: row.id,
        message: "Could not dismiss the operation.",
      }}
    />,
  );
  expect(screen.getByText("Northern Lines · position 01")).toBeVisible();
  expect(
    screen.getByText(copy.loadError).closest('[role="alert"]'),
  ).not.toBeNull();
  expect(screen.getByText("Could not dismiss the operation.")).toHaveRole(
    "alert",
  );
  expect(screen.getByRole("button", { name: "Retry loading" })).toBeDisabled();
  rerender(<OperationsList copy={copy} rows={[]} />);
  expect(screen.getByText("No current operations")).toBeVisible();
  expect(screen.queryByRole("button", { name: "Retry loading" })).toBeNull();
});
