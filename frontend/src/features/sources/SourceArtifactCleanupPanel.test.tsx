import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  OperationResponse,
  SourceAnalysisArtifactCleanupCandidate,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourceArtifactCleanupPanel } from "./SourceArtifactCleanupPanel";
import { useSourceArtifactCleanup } from "./useSourceArtifactCleanup";

const api = "/api/source-analysis/artifacts/cleanup";
const artifact = (
  id: string,
  relative_path = `work/${id}.wav`,
): SourceAnalysisArtifactCleanupCandidate => ({
  artifact_id: id,
  relative_path,
  state: "cleanup_eligible",
});

function operation(
  overrides: Partial<OperationResponse> = {},
): OperationResponse {
  return {
    id: "cleanup-op",
    kind: "cleanup_source_analysis_artifacts",
    state: "queued",
    stage: "queued",
    bytes_completed: 0,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

class CleanupEventSource extends EventTarget {
  static instances: CleanupEventSource[] = [];
  readonly url: string;
  close = vi.fn();
  opened = false;

  constructor(url: string) {
    super();
    this.url = url;
    CleanupEventSource.instances.push(this);
  }
}

beforeEach(() => {
  CleanupEventSource.instances = [];
  vi.stubGlobal("EventSource", CleanupEventSource);
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function candidates(
  items: SourceAnalysisArtifactCleanupCandidate[],
  count = items.length,
) {
  return HttpResponse.json({ candidates: items, count });
}

describe("SourceArtifactCleanupPanel", () => {
  it("loads a bounded returned batch and posts only the exact IDs in the focused confirmation; cancel and Escape do not start cleanup", async () => {
    let posts = 0;
    server.use(
      http.get(api, () => candidates([artifact("a"), artifact("b")], 2)),
      http.post(api, async ({ request }) => {
        posts += 1;
        expect(await request.json()).toEqual({ artifact_ids: ["a"] });
        return HttpResponse.json(operation(), { status: 202 });
      }),
      http.get("/api/operations/cleanup-op", () =>
        HttpResponse.json(operation({ state: "running", stage: "deleting" })),
      ),
    );
    render(<SourceArtifactCleanupPanel />);

    expect(await screen.findByText(/текущей подборке: 2/i)).toHaveTextContent(
      "не общее число копий или лимит удаления",
    );
    expect(
      screen.getByRole("checkbox", { name: /work\/a.wav/ }),
    ).not.toBeChecked();
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );

    const dialog = screen.getByRole("dialog");
    const cancelButton = screen.getByRole("button", { name: "Отмена" });
    const confirmButton = screen.getByRole("button", {
      name: "Подтвердить очистку",
    });
    expect(cancelButton).toHaveFocus();
    expect(dialog).toHaveTextContent("a");

    const backwardsTab = new KeyboardEvent("keydown", {
      key: "Tab",
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    });
    cancelButton.dispatchEvent(backwardsTab);
    expect(backwardsTab.defaultPrevented).toBe(true);
    expect(confirmButton).toHaveFocus();

    const forwardsTab = new KeyboardEvent("keydown", {
      key: "Tab",
      bubbles: true,
      cancelable: true,
    });
    confirmButton.dispatchEvent(forwardsTab);
    expect(forwardsTab.defaultPrevented).toBe(true);
    expect(cancelButton).toHaveFocus();

    fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    ).toHaveFocus();
    expect(posts).toBe(0);

    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(posts).toBe(0);

    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );
    await waitFor(() => expect(posts).toBe(1));
    await waitFor(() => expect(CleanupEventSource.instances).toHaveLength(1));
  });

  it("claims a cleanup synchronously so same-tick starts post once and pending operations reject another start", async () => {
    let posts = 0;
    server.use(
      http.get(api, () => candidates([artifact("a")])),
      http.post(api, () => {
        posts += 1;
        return HttpResponse.json(operation(), { status: 202 });
      }),
      http.get("/api/operations/cleanup-op", () =>
        HttpResponse.json(operation()),
      ),
    );
    const { result } = renderHook(() => useSourceArtifactCleanup());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      void result.current.start(["a"]);
      void result.current.start(["a"]);
    });
    await waitFor(() => expect(posts).toBe(1));
    await waitFor(() => expect(result.current.isPending).toBe(true));
    await act(async () => {
      await result.current.start(["a"]);
    });
    expect(posts).toBe(1);
  });

  it("reads an accepted operation immediately even if SSE never opens and refreshes candidates after terminal mixed results", async () => {
    let candidateReads = 0;
    const terminal = operation({
      state: "failed",
      stage: "complete",
      cleanup_results: [
        { artifact_id: "a", state: "deleted" },
        {
          artifact_id: "b",
          state: "failed",
          safe_error: "Не удалось удалить копию.",
        },
      ],
    });
    server.use(
      http.get(api, () => {
        candidateReads += 1;
        return candidates(
          candidateReads === 1
            ? [artifact("a"), artifact("b")]
            : [artifact("b")],
        );
      }),
      http.post(api, () => HttpResponse.json(operation(), { status: 202 })),
      http.get("/api/operations/cleanup-op", () => HttpResponse.json(terminal)),
    );
    render(<SourceArtifactCleanupPanel />);
    await screen.findByText(/текущей подборке: 2/i);
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );

    expect(
      await screen.findByText(/Операция завершена: failed/i),
    ).toBeInTheDocument();
    const results = within(
      screen.getByRole("list", { name: "Результаты очистки" }),
    ).getAllByRole("listitem");
    expect(results[0]).toHaveTextContent("a: Удалён");
    expect(results[1]).toHaveTextContent(
      "b: Не удалось удалить — Не удалось удалить копию.",
    );
    await waitFor(() => expect(candidateReads).toBe(2));
    expect(
      screen.getByRole("checkbox", { name: /work\/b.wav/ }),
    ).toBeInTheDocument();
    expect(CleanupEventSource.instances.every((stream) => !stream.opened)).toBe(
      true,
    );
  });

  it("aborts the immediate REST snapshot when unmounted", async () => {
    let snapshotSignal: AbortSignal | undefined;
    server.use(
      http.get(api, () => candidates([artifact("a")])),
      http.post(api, () => HttpResponse.json(operation(), { status: 202 })),
      http.get("/api/operations/cleanup-op", async ({ request }) => {
        snapshotSignal = request.signal;
        await new Promise<void>((resolve) => {
          request.signal.addEventListener("abort", () => resolve(), {
            once: true,
          });
        });
        return HttpResponse.json(
          operation({ state: "succeeded", stage: "complete" }),
        );
      }),
    );
    const view = render(<SourceArtifactCleanupPanel />);
    await screen.findByText(/текущей подборке: 1/i);
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );
    await waitFor(() => expect(snapshotSignal).toBeDefined());

    view.unmount();
    expect(snapshotSignal?.aborted).toBe(true);
  });

  it("does not imply deletion when a terminal snapshot has no per-item results", async () => {
    server.use(
      http.get(api, () => candidates([artifact("a")])),
      http.post(api, () => HttpResponse.json(operation(), { status: 202 })),
      http.get("/api/operations/cleanup-op", () =>
        HttpResponse.json(operation({ state: "succeeded", stage: "complete" })),
      ),
    );
    render(<SourceArtifactCleanupPanel />);
    await screen.findByText(/текущей подборке: 1/i);
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );

    expect(
      await screen.findByText(/состояние удаления неизвестно/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/a: Удалён/)).not.toBeInTheDocument();
  });

  it("shows mixed durable per-item results and refreshes the eligible batch after settlement", async () => {
    let candidateReads = 0;
    let operationRead = operation({
      state: "running",
      stage: "deleting",
    });
    server.use(
      http.get(api, () => {
        candidateReads += 1;
        return candidates(
          candidateReads < 2 ? [artifact("a"), artifact("b")] : [artifact("b")],
        );
      }),
      http.post(api, () => HttpResponse.json(operationRead, { status: 202 })),
      http.get("/api/operations/cleanup-op", () =>
        HttpResponse.json(operationRead),
      ),
    );
    render(<SourceArtifactCleanupPanel />);
    await screen.findByText(/текущей подборке: 2/i);
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );
    expect(await screen.findByText(/Операция running/i)).toBeInTheDocument();
    await waitFor(() => expect(CleanupEventSource.instances).toHaveLength(1));

    operationRead = operation({
      state: "failed",
      stage: "complete",
      cleanup_results: [
        { artifact_id: "a", state: "deleted" },
        {
          artifact_id: "b",
          state: "failed",
          safe_error: "Не удалось удалить копию.",
        },
      ],
    });
    await act(async () => {
      CleanupEventSource.instances[0].dispatchEvent(
        new Event("operation-changed"),
      );
    });

    expect(
      await screen.findByText(/Операция завершена: failed/i),
    ).toBeInTheDocument();
    const results = within(
      screen.getByRole("list", { name: "Результаты очистки" }),
    ).getAllByRole("listitem");
    expect(results[0]).toHaveTextContent("a: Удалён");
    expect(results[1]).toHaveTextContent(
      "b: Не удалось удалить — Не удалось удалить копию.",
    );
    await waitFor(() => expect(candidateReads).toBe(2));
    expect(
      screen.getByRole("checkbox", { name: /work\/b.wav/ }),
    ).toBeInTheDocument();
  });

  it("keeps the selected IDs unchanged when server eligibility is stale and refreshes candidates", async () => {
    let candidateReads = 0;
    let submitted: unknown;
    server.use(
      http.get(api, () => {
        candidateReads += 1;
        return candidates(
          candidateReads === 1 ? [artifact("old-id")] : [artifact("new-id")],
        );
      }),
      http.post(api, async ({ request }) => {
        submitted = await request.json();
        return HttpResponse.json(
          { detail: "Eligibility changed." },
          { status: 409 },
        );
      }),
    );
    render(<SourceArtifactCleanupPanel />);
    expect(await screen.findByText(/текущей подборке: 1/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/old-id.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    expect(screen.getByText("old-id")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Eligibility changed.",
    );
    await waitFor(() => expect(candidateReads).toBe(2));
    expect(submitted).toEqual({ artifact_ids: ["old-id"] });
    expect(
      screen.getByRole("checkbox", { name: /work\/new-id.wav/ }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: /очистить выбранные \(1\)/i }),
    ).toBeInTheDocument();
  });

  it("closes the operation stream and ignores callbacks after unmount", async () => {
    server.use(
      http.get(api, () => candidates([artifact("a")])),
      http.post(api, () => HttpResponse.json(operation(), { status: 202 })),
      http.get("/api/operations/cleanup-op", () =>
        HttpResponse.json(operation()),
      ),
    );
    const view = render(<SourceArtifactCleanupPanel />);
    await screen.findByText(/текущей подборке: 1/i);
    fireEvent.click(screen.getByRole("checkbox", { name: /work\/a.wav/ }));
    fireEvent.click(
      screen.getByRole("button", { name: /очистить выбранные/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить очистку" }),
    );
    await waitFor(() => expect(CleanupEventSource.instances).toHaveLength(1));
    const stream = CleanupEventSource.instances[0];
    view.unmount();
    expect(stream.close).toHaveBeenCalledOnce();
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
    });
  });
});
