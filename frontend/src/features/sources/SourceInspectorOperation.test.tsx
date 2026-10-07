import { act, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import {
  detail,
  detailPath,
  observe,
  openInspector,
  operation,
  responseFor,
  result,
  stream,
} from "./sourceInspectorTestSupport";

describe("source inspector operation wake-ups", () => {
  it("rereads the inspector on intermediate wakes while preserving previous results", async () => {
    let phase = 0;
    let detailReads = 0;
    let operationReads = 0;
    server.use(
      http.get(detailPath, () => {
        detailReads += 1;
        return HttpResponse.json(
          detail({
            result,
            relative_path: phase > 0 ? "album/02.flac" : "album/01.flac",
            active_analysis_operation_id: "step-op",
            steps:
              phase > 0
                ? [
                    {
                      name: "sha256",
                      state: "succeeded",
                      attempt: 1,
                      sha256: { value: "digest-value" },
                    },
                  ]
                : [{ name: "sha256", state: "running", attempt: 1 }],
          }),
        );
      }),
      http.get("/api/operations/step-op", () => {
        operationReads += 1;
        return HttpResponse.json(
          operation({
            id: "step-op",
            kind: "retry-source-location-step",
            state: "running",
            stage: "hashing",
          }),
        );
      }),
    );
    const initialOperation = responseFor("/api/operations/step-op");
    await openInspector();
    await initialOperation;
    await observe(() => !!screen.queryByText(/Состояние: Выполняется/));
    const previousDetailReads = detailReads;
    const previousOperationReads = operationReads;
    phase = 1;
    const refreshed = responseFor(detailPath, "GET", 1);
    const operationRefreshed = responseFor("/api/operations/step-op", "GET", 1);
    await act(async () => {
      stream().dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify(
            operation({
              id: "step-op",
              state: "running",
              updated_at: "2026-10-01T11:00:00Z",
            }),
          ),
        }),
      );
      await Promise.all([refreshed, operationRefreshed]);
    });
    expect(detailReads).toBeGreaterThan(previousDetailReads);
    expect(operationReads).toBeGreaterThan(previousOperationReads);
    expect(screen.getByText("matroska")).toBeVisible();
  });

  it("clears a stale active operation after the server no longer reports its ID", async () => {
    let reads = 0;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            ...(reads++ === 0
              ? { active_analysis_operation_id: "stale-op" }
              : {}),
          }),
        ),
      ),
      http.get("/api/operations/stale-op", () =>
        HttpResponse.json(
          operation({
            id: "stale-op",
            state: "failed",
            safe_error: "old failure",
          }),
        ),
      ),
    );
    const reread = responseFor(detailPath, "GET", 2);
    await openInspector();
    await reread;
    await observe(
      () => !screen.queryByText(/Сохранение результата этапа анализа/),
    );
    expect(reads).toBeGreaterThanOrEqual(2);
  });
});
