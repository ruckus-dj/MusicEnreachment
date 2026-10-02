import { fireEvent, screen } from "@testing-library/react";
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
} from "./sourceInspectorTestSupport";

describe("inspector logical operation retry", () => {
  it("retries the failed logical operation when its snapshot still applies", async () => {
    // Given: the operation becomes terminal between detail discovery and its read.
    let reads = 0;
    let state = "failed";
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            analysis_state: "analyzed",
            ...(reads++ === 0
              ? { active_analysis_operation_id: "analysis-1" }
              : {}),
          }),
        ),
      ),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(
          operation({
            state,
            stage: "probing",
            safe_error: "probe failed",
          }),
        ),
      ),
      http.post("/api/operations/analysis-1/retry", () => {
        state = "running";
        return HttpResponse.json(operation({ state, stage: "probing" }));
      }),
    );
    const terminalDetail = responseFor(detailPath, "GET", 2);
    await openInspector();
    await terminalDetail;
    await observe(
      () =>
        !!screen.queryByRole("button", {
          name: "Повторить попытку операции",
        }) && !screen.queryByText(/Загрузка файла/),
    );
    const retried = responseFor("/api/operations/analysis-1/retry", "POST");
    // When
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить попытку операции" }),
    );
    await retried;
    await observe(
      () => !!screen.queryByText("Чтение технических данных ffprobe."),
    );
    // Then
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Повторить анализ" }),
    ).toBeDisabled();
    expect(screen.getByText("matroska")).toBeVisible();
  });
  it("preserves the failed operation and explains a fresh analysis when retry conflicts", async () => {
    // Given
    let reads = 0;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            analysis_state: "analyzed",
            ...(reads++ === 0
              ? { active_analysis_operation_id: "analysis-1" }
              : {}),
          }),
        ),
      ),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(
          operation({ state: "failed", safe_error: "previous probe failure" }),
        ),
      ),
      http.post("/api/operations/analysis-1/retry", () =>
        HttpResponse.json({ detail: "snapshot changed" }, { status: 409 }),
      ),
    );
    const terminalDetail = responseFor(detailPath, "GET", 2);
    await openInspector();
    await terminalDetail;
    await observe(
      () =>
        !!screen.queryByRole("button", {
          name: "Повторить попытку операции",
        }) && !screen.queryByText(/Загрузка файла/),
    );
    const refreshed = responseFor(detailPath);
    // When
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить попытку операции" }),
    );
    await refreshed;
    await observe(
      () =>
        !!screen.queryByText(/snapshot changed/) &&
        !screen.queryByText(/Загрузка файла/),
    );
    // Then
    expect(screen.getByText(/snapshot changed/)).toHaveTextContent(
      "новый анализ текущего файла",
    );
    expect(screen.getByText(/previous probe failure/)).toBeVisible();
    expect(screen.getByText("matroska")).toBeVisible();
  });
});
