import { act, fireEvent, screen } from "@testing-library/react";
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
  stamp,
  stream,
} from "./sourceInspectorTestSupport";

describe("inspector analysis operations", () => {
  it("posts the observed identity and refreshes detail when the operation succeeds", async () => {
    // Given
    let analyzed = false;
    let body: unknown;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail(analyzed ? { result, analysis_state: "analyzed" } : {}),
        ),
      ),
      http.post(`${detailPath}/analyze`, async ({ request }) => {
        body = await request.json();
        analyzed = true;
        return HttpResponse.json(operation(), { status: 202 });
      }),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(operation({ state: "succeeded" })),
      ),
    );
    await openInspector();
    const refreshed = responseFor(detailPath);
    // When
    fireEvent.click(screen.getByRole("button", { name: "Анализировать" }));
    await refreshed;
    await observe(() => !!screen.queryByText("matroska"));
    // Then
    expect(body).toEqual({
      expected_size_bytes: 1048576,
      expected_mtime: stamp,
    });
    expect(
      screen.getByRole("button", { name: "Повторить анализ" }),
    ).toBeEnabled();
    expect(stream().close).toHaveBeenCalled();
  });
  it("rereads identity and offers a fresh load when start returns conflict", async () => {
    // Given
    server.use(
      http.post(`${detailPath}/analyze`, () =>
        HttpResponse.json({ detail: "identity changed" }, { status: 409 }),
      ),
    );
    await openInspector();
    const refreshed = responseFor(detailPath);
    // When
    fireEvent.click(screen.getByRole("button", { name: "Анализировать" }));
    await refreshed;
    await observe(() => !!screen.queryByRole("alert"));
    // Then
    expect(screen.getByRole("alert")).toHaveTextContent("identity changed");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "новый анализ текущего файла",
    );
    expect(
      screen.getByRole("button", { name: "Повторить загрузку" }),
    ).toBeEnabled();
  });
  it("keeps previous result and its date when a repeat fails", async () => {
    // Given
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(detail({ result, analysis_state: "analyzed" })),
      ),
      http.post(`${detailPath}/analyze`, () =>
        HttpResponse.json(operation(), { status: 202 }),
      ),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(
          operation({
            state: "failed",
            stage: "probing",
            safe_error: "probe read failed",
          }),
        ),
      ),
    );
    await openInspector();
    // When
    fireEvent.click(screen.getByRole("button", { name: "Повторить анализ" }));
    await observe(
      () =>
        !!screen.queryByRole("button", {
          name: "Повторить попытку операции",
        }) && !screen.queryByText(/Загрузка файла/),
    );
    // Then
    expect(screen.getByRole("alert")).toHaveTextContent("probe read failed");
    expect(screen.getByText("matroska")).toBeVisible();
    expect(screen.getByText(/Анализ от/)).toHaveTextContent(
      new Date(stamp).toLocaleString(),
    );
  });
  it("adopts active analysis, treats SSE as wake only, and refreshes failure detail on reconnect", async () => {
    // Given
    let state = "running";
    let reads = 0;
    server.use(
      http.get(detailPath, () => {
        reads += 1;
        return HttpResponse.json(
          detail({
            result,
            analysis_state: "analyzed",
            ...(state === "running"
              ? { active_analysis_operation_id: "analysis-1" }
              : {}),
          }),
        );
      }),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(
          operation({
            state,
            stage: "probing",
            safe_error: "reconnected failure",
          }),
        ),
      ),
    );
    // When
    await openInspector();
    await observe(
      () => !!screen.queryByText("Чтение технических данных ffprobe."),
    );
    // Then
    expect(
      screen.getByRole("button", { name: "Повторить анализ" }),
    ).toBeDisabled();
    const events = stream();
    const disconnectedRead = responseFor("/api/operations/analysis-1");
    await act(async () => {
      events.dispatchEvent(new Event("error"));
      await disconnectedRead;
    });
    expect(screen.getByText(/Поток событий прерван/)).toBeVisible();
    state = "failed";
    const refreshed = responseFor(detailPath);
    act(() => {
      events.dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify(operation({ state: "succeeded" })),
        }),
      );
    });
    await refreshed;
    await observe(
      () =>
        !!screen.queryByRole("alert") && !screen.queryByText(/Загрузка файла/),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("reconnected failure");
    expect(reads).toBe(2);
    expect(screen.getByText("matroska")).toBeVisible();
    expect(events.close).toHaveBeenCalled();
  });
  it("dismisses a failed operation without erasing the saved result", async () => {
    // Given
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(detail({ result, analysis_state: "analyzed" })),
      ),
      http.post(`${detailPath}/analyze`, () =>
        HttpResponse.json(operation(), { status: 202 }),
      ),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(
          operation({ state: "failed", safe_error: "probe failed" }),
        ),
      ),
      http.delete(
        "/api/operations/analysis-1",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    await openInspector();
    fireEvent.click(screen.getByRole("button", { name: "Повторить анализ" }));
    await observe(
      () =>
        !!screen.queryByRole("button", { name: "Скрыть операцию" }) &&
        !screen.queryByText(/Загрузка файла/),
    );
    const dismissed = responseFor("/api/operations/analysis-1", "DELETE");
    // When
    fireEvent.click(screen.getByRole("button", { name: "Скрыть операцию" }));
    await act(async () => {
      await dismissed;
    });
    // Then
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("matroska")).toBeVisible();
  });
});
