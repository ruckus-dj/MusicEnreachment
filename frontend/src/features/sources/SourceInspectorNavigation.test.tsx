import { act, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";
import {
  detail,
  detailPath,
  observe,
  operation,
  responseFor,
  stream,
} from "./sourceInspectorTestSupport";

describe("inspector file navigation", () => {
  it("aborts the old detail and rejects its late response when navigating to another file", async () => {
    // Given
    let release: (response: Response) => void = () => {};
    let oldSignal: AbortSignal | undefined;
    const requested = new Promise<void>((resolve) => {
      server.use(
        http.get(detailPath, ({ request }) => {
          oldSignal = request.signal;
          return new Promise<Response>((done) => {
            release = done;
            resolve();
          });
        }),
      );
    });
    server.use(
      http.get("/api/sources/root-1/locations/file-2", () =>
        HttpResponse.json(
          detail({ location_id: "file-2", relative_path: "new.flac" }),
        ),
      ),
    );
    window.location.hash = "/sources/root-1/locations/file-1";
    render(<SourcesScreen />);
    await requested;
    // When
    await act(async () => {
      window.location.hash = "/sources/root-1/locations/file-2";
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await observe(() => !!screen.queryByText("new.flac"));
    await act(async () => {
      release(HttpResponse.json(detail({ relative_path: "late-old.flac" })));
    });
    // Then
    expect(oldSignal?.aborted).toBe(true);
    expect(screen.getByText("new.flac")).toBeVisible();
    expect(screen.queryByText("late-old.flac")).not.toBeInTheDocument();
    window.location.hash = "";
  });
  it("closes the old stream and aborts an in-flight operation reread when navigating", async () => {
    // Given
    let hold = false;
    let release: (response: Response) => void = () => {};
    let oldSignal: AbortSignal | undefined;
    let readStarted: () => void = () => {};
    const started = new Promise<void>((resolve) => {
      readStarted = resolve;
    });
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({ active_analysis_operation_id: "analysis-1" }),
        ),
      ),
      http.get("/api/operations/analysis-1", ({ request }) => {
        if (!hold)
          return HttpResponse.json(
            operation({ state: "running", stage: "probing" }),
          );
        oldSignal = request.signal;
        return new Promise<Response>((done) => {
          release = done;
          readStarted();
        });
      }),
      http.get("/api/sources/root-1/locations/file-2", () =>
        HttpResponse.json(
          detail({ location_id: "file-2", relative_path: "second.flac" }),
        ),
      ),
    );
    window.location.hash = "/sources/root-1/locations/file-1";
    render(<SourcesScreen />);
    await observe(
      () => !!screen.queryByText("Чтение технических данных ffprobe."),
    );
    const oldStream = stream();
    hold = true;
    oldStream.dispatchEvent(new Event("open"));
    await started;
    // When
    const next = responseFor("/api/sources/root-1/locations/file-2");
    await act(async () => {
      window.location.hash = "/sources/root-1/locations/file-2";
      window.dispatchEvent(new HashChangeEvent("hashchange"));
      await next;
    });
    await observe(() => !!screen.queryByText("second.flac"));
    await act(async () => {
      release(
        HttpResponse.json(
          operation({ state: "failed", safe_error: "old failure" }),
        ),
      );
    });
    // Then
    expect(oldSignal?.aborted).toBe(true);
    expect(oldStream.close).toHaveBeenCalledOnce();
    expect(screen.queryByText(/old failure/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Анализировать" })).toBeEnabled();
    window.location.hash = "";
  });
});
