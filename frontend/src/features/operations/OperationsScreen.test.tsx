import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it } from "vitest";
import { server } from "../../test/server";
import { OperationsScreen } from "./OperationsScreen";

function renderScreen() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <OperationsScreen />
    </QueryClientProvider>,
  );
}

it("shows loading then current operations with real diagnostics and failed retry", async () => {
  let retried = false;
  server.use(
    http.get("/api/operations", () =>
      HttpResponse.json({
        operations: [
          {
            id: "failed-id",
            kind: "scan_source",
            state: "failed",
            stage: "scan_files",
            bytes_completed: 12,
            bytes_total: 30,
            target_identity: "/music",
            safe_error: "Permission denied",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
          {
            id: "done-id",
            kind: "old_success",
            state: "succeeded",
            stage: "done",
            bytes_completed: 1,
            created_at: "2025-01-01T00:00:00Z",
            updated_at: "2025-01-01T00:01:00Z",
          },
        ],
      }),
    ),
    http.post("/api/operations/failed-id/retry", () => {
      retried = true;
      return HttpResponse.json({
        id: "failed-id",
        kind: "scan_source",
        state: "queued",
        stage: "queued",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:02:00Z",
      });
    }),
  );

  renderScreen();
  expect(screen.getByRole("status")).toHaveTextContent("Загрузка");
  expect(await screen.findByText("Permission denied")).toBeInTheDocument();
  expect(screen.getByText("12 / 30 байт")).toBeInTheDocument();
  expect(screen.queryByText("old_success")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Повторить" }));
  await waitFor(() => expect(retried).toBe(true));
});

it.each([
  "cleanup_source_analysis_artifacts",
  "cleanup_publication_sources_v1",
  "retry_publication_sources_v1",
  "analyze_source",
])("does not offer generic retry for unsupported %s operations", async (kind) => {
  let retryCalls = 0;
  server.use(
    http.get("/api/operations", () =>
      HttpResponse.json({
        operations: [
          {
            id: "unsupported-id",
            kind,
            state: "failed",
            stage: "failed",
            bytes_completed: 0,
            safe_error: "Existing diagnostic is preserved",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
      }),
    ),
    http.post("/api/operations/:operationId/retry", () => {
      retryCalls += 1;
      return HttpResponse.json({ detail: "unsupported" }, { status: 409 });
    }),
  );

  renderScreen();
  expect(
    await screen.findByText("Existing diagnostic is preserved"),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Повторить" }),
  ).not.toBeInTheDocument();
  expect(retryCalls).toBe(0);
});

it("shows a useful empty state when there are no unfinished or failed operations", async () => {
  server.use(
    http.get("/api/operations", () =>
      HttpResponse.json({
        operations: [
          {
            id: "done-id",
            kind: "completed",
            state: "succeeded",
            stage: "done",
            bytes_completed: 0,
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
      }),
    ),
  );

  renderScreen();
  expect(await screen.findByText("Нет текущих операций")).toBeInTheDocument();
});

it("offers a retry when loading fails", async () => {
  server.use(
    http.get("/api/operations", () =>
      HttpResponse.json({ detail: "Database unavailable" }, { status: 503 }),
    ),
  );

  renderScreen();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Database unavailable",
  );
  expect(
    screen.getByRole("button", { name: "Повторить загрузку" }),
  ).toBeInTheDocument();
});
