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
            kind: "install",
            state: "succeeded",
            stage: "done",
            bytes_completed: 1,
            target_identity: "ffmpeg:test:4.2.3:linux:amd64",
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
  expect(screen.getByText("12 / 30 bytes")).toBeInTheDocument();
  expect(screen.queryByText("FFmpeg, version 4.2.3")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Повторить" }));
  await waitFor(() => expect(retried).toBe(true));
});
