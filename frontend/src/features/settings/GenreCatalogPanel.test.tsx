import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { subscribeToOperation } from "../../api/client/operations";
import {
  getGenreCatalog,
  getOperation,
  refreshGenreCatalog,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { GenreCatalogPanel } from "./GenreCatalogPanel";

vi.mock("../../api/generated/client", () => ({
  getGenreCatalog: vi.fn(),
  getOperation: vi.fn(),
  refreshGenreCatalog: vi.fn(),
}));

vi.mock("../../api/client/operations", () => ({
  subscribeToOperation: vi.fn(),
}));

const operationId = "76092c63-0e0f-4dc1-af39-674dc1ce037c";
const status = {
  count: 128,
  items: [],
  last_attempt_at: "2026-11-01T12:00:00Z",
  last_error: null,
  refresh_status: "idle",
  active_operation_id: null,
  updated_at: "2026-10-31T12:00:00Z",
};
const queued: OperationResponse = {
  id: operationId,
  kind: "genre_catalog_refresh",
  state: "queued",
  stage: "queued",
  bytes_completed: 0,
  created_at: "2026-11-06T12:00:00Z",
  updated_at: "2026-11-06T12:00:00Z",
};
const terminal: OperationResponse = {
  ...queued,
  state: "succeeded",
  stage: "finished",
  updated_at: "2026-11-06T12:01:00Z",
  finished_at: "2026-11-06T12:01:00Z",
};

describe("GenreCatalogPanel", () => {
  let wake: ((id: string) => Promise<void>) | undefined;
  let reconnect: ((id: string) => Promise<void>) | undefined;

  beforeEach(() => {
    vi.clearAllMocks();
    wake = undefined;
    reconnect = undefined;
    vi.mocked(subscribeToOperation).mockImplementation((id, notify) => {
      wake = async (wakeId) => {
        if (wakeId !== id) return;
        const response = await getOperation(wakeId, { cache: "no-store" });
        if (response.status === 200) notify(response.data);
      };
      reconnect = async (reconnectId) => {
        if (reconnectId !== id) return;
        const response = await getOperation(reconnectId, {
          cache: "no-store",
        });
        if (response.status === 200) notify(response.data);
      };
      return vi.fn();
    });
    vi.mocked(getGenreCatalog).mockResolvedValue({
      status: 200,
      data: status,
      headers: new Headers(),
    } as never);
  });

  it("shows the current catalog status", async () => {
    render(<GenreCatalogPanel />);

    expect(await screen.findByText("128")).toBeInTheDocument();
    expect(screen.getByText("Last attempt")).toBeInTheDocument();
  });

  it("reports a status request error without discarding the catalog", async () => {
    vi.mocked(getGenreCatalog)
      .mockResolvedValueOnce({
        status: 200,
        data: status,
        headers: new Headers(),
      } as never)
      .mockRejectedValueOnce(new Error("network unavailable"));
    render(<GenreCatalogPanel />);
    expect(await screen.findByText("128")).toBeInTheDocument();
    // A terminal operation triggers a later status read; its failure must not
    // erase the last catalog snapshot.
    vi.mocked(refreshGenreCatalog).mockResolvedValue({
      status: 200,
      data: queued,
      headers: new Headers(),
    } as never);
    vi.mocked(getOperation).mockResolvedValue({
      status: 200,
      data: terminal,
      headers: new Headers(),
    } as never);
    fireEvent.click(screen.getByRole("button", { name: "Refresh catalog" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "network unavailable",
    );
    expect(screen.getByText("128")).toBeInTheDocument();
  });

  it("follows the operation returned by refresh and reloads on SSE wake and reconnect", async () => {
    vi.mocked(refreshGenreCatalog).mockResolvedValue({
      status: 200,
      data: queued,
      headers: new Headers(),
    } as never);
    vi.mocked(getOperation)
      .mockResolvedValueOnce({
        status: 200,
        data: queued,
        headers: new Headers(),
      } as never)
      .mockResolvedValueOnce({
        status: 200,
        data: queued,
        headers: new Headers(),
      } as never)
      .mockResolvedValueOnce({
        status: 200,
        data: terminal,
        headers: new Headers(),
      } as never);
    vi.mocked(getGenreCatalog)
      .mockResolvedValueOnce({
        status: 200,
        data: status,
        headers: new Headers(),
      } as never)
      .mockResolvedValueOnce({
        status: 200,
        data: { ...status, count: 140 },
        headers: new Headers(),
      } as never);

    render(<GenreCatalogPanel />);
    await screen.findByText("128");
    fireEvent.click(screen.getByRole("button", { name: "Refresh catalog" }));

    await waitFor(() =>
      expect(subscribeToOperation).toHaveBeenCalledWith(
        operationId,
        expect.any(Function),
        expect.any(Function),
      ),
    );
    await waitFor(() =>
      expect(getOperation).toHaveBeenCalledWith(operationId, expect.anything()),
    );
    expect(screen.getByText(/Catalog refresh: queued/)).toBeInTheDocument();

    expect(wake).toBeDefined();
    await wake?.(operationId);
    expect(reconnect).toBeDefined();
    await reconnect?.(operationId);
    await waitFor(() => expect(screen.getByText("140")).toBeInTheDocument());
    expect(getOperation).toHaveBeenCalledTimes(3);
  });

  it("attaches to an active operation from status after mount", async () => {
    vi.mocked(getGenreCatalog).mockResolvedValue({
      status: 200,
      data: { ...status, active_operation_id: operationId },
      headers: new Headers(),
    } as never);
    vi.mocked(getOperation).mockResolvedValue({
      status: 200,
      data: queued,
      headers: new Headers(),
    } as never);

    render(<GenreCatalogPanel />);
    expect(await screen.findByText("128")).toBeInTheDocument();
    await waitFor(() =>
      expect(subscribeToOperation).toHaveBeenCalledWith(
        operationId,
        expect.any(Function),
        expect.any(Function),
      ),
    );
    expect(
      screen.getByRole("button", { name: "Refresh catalog" }),
    ).toBeDisabled();
  });
});
