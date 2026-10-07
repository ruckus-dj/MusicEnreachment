import { act, fireEvent, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import {
  detail,
  detailPath,
  openInspector,
  operation,
  responseFor,
  result,
  stream,
} from "./sourceInspectorTestSupport";

describe("source inspector step controls", () => {
  it.each([
    { label: "zero", streams: [], probeStatus: "no_audio" as const },
    {
      label: "multiple",
      streams: result.streams,
      probeStatus: "audio" as const,
    },
  ])("shows neutral matching eligibility for $label audio streams", async ({
    streams,
    probeStatus,
  }) => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result: { ...result, streams },
            probe_status: probeStatus,
            steps: [
              { name: "sha256", state: "not_requested", attempt: 0 },
              { name: "probe", state: "succeeded", attempt: 1 },
              {
                name: "fingerprint",
                state: "succeeded",
                attempt: 1,
                fingerprint: {
                  value: "preserved-fingerprint",
                  version: "fpcalc 1.6",
                  version_banner: "fpcalc 1.6",
                  algorithm_namespace: "chromaprint",
                  algorithm_id: 1,
                  duration: 42,
                  calculated_at: "2026-10-01T10:20:00Z",
                  applied_operation_id: "analysis-1",
                  parser_contract_version: 1,
                },
              },
            ],
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByText("Состояние: Не запрошено")).toBeVisible();
    expect(screen.queryByText("Состояние: Загрузка")).not.toBeInTheDocument();
    expect(screen.getByText("preserved-fingerprint")).toBeVisible();
    expect(screen.queryByText(/Анализ недоступен/)).not.toBeInTheDocument();
    expect(screen.getByText(/Не поддерживается для файла/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /Повторить этап.*Сопоставление/ }),
    ).not.toBeInTheDocument();
  });

  it("shows a shared digest and partial ffprobe result without fabricating a digest", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result: { ...result, streams: null },
            probe_status: "probe_error",
            steps: [
              {
                name: "sha256",
                state: "succeeded",
                attempt: 1,
                reuse_origin: "shared",
                sha256: { value: "shared-digest" },
              },
              {
                name: "probe",
                state: "failed",
                attempt: 1,
                safe_error: "partial ffprobe failure",
              },
            ],
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByText("shared-digest")).toBeVisible();
    expect(screen.getByText(/· shared$/)).toBeVisible();
    expect(screen.getByText("matroska")).toBeVisible();
    expect(screen.getByText("partial ffprobe failure")).toBeVisible();
    expect(screen.queryByText(/Анализ недоступен/)).not.toBeInTheDocument();
    expect(screen.getByText("Состояние: Не запрошено")).toBeVisible();
    expect(
      screen.queryByText("Успешный результат этапа отсутствует."),
    ).not.toBeInTheDocument();
  });

  it("retries only the failed step with the observed file identity", async () => {
    let body: unknown;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            steps: [
              {
                name: "sha256",
                state: "failed",
                attempt: 1,
                safe_error: "digest failed",
              },
            ],
          }),
        ),
      ),
      http.post(`${detailPath}/retry`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          {
            id: "retry-1",
            state: "queued",
            kind: "retry-source-location-step",
            stage: "queued",
            bytes_completed: 0,
            created_at: "",
            updated_at: "",
          },
          { status: 202 },
        );
      }),
    );
    await openInspector();
    expect(screen.getByText("digest failed")).toBeVisible();
    const admitted = responseFor(`${detailPath}/retry`, "POST");
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить этап «SHA-256»" }),
    );
    await admitted;
    expect(body).toEqual({
      step: "sha256",
      expected_size_bytes: 1048576,
      expected_mtime: "2026-10-01T10:20:00.123456Z",
    });
  });

  it("reruns a stored fingerprint without requiring a current audio probe", async () => {
    let body: unknown;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.6",
            probe_status: "probe_error",
            steps: [
              {
                name: "fingerprint",
                state: "succeeded",
                attempt: 1,
                fingerprint: {
                  value: "12345",
                  version: "fpcalc 1.5",
                  version_banner: "fpcalc 1.5",
                  algorithm_namespace: "chromaprint",
                  algorithm_id: 1,
                  duration: 42,
                  calculated_at: "2026-10-01T10:20:00Z",
                  applied_operation_id: "old",
                  parser_contract_version: 1,
                },
              },
            ],
          }),
        ),
      ),
      http.post(`${detailPath}/fingerprint/rerun`, async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(
          {
            id: "rerun-1",
            state: "queued",
            kind: "rerun-source-location-fingerprint",
            stage: "queued",
            bytes_completed: 0,
            created_at: "",
            updated_at: "",
          },
          { status: 202 },
        );
      }),
    );
    await openInspector();
    const admitted = responseFor(`${detailPath}/fingerprint/rerun`, "POST");
    fireEvent.click(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    );
    await admitted;
    expect(body).toEqual({
      expected_size_bytes: 1048576,
      expected_mtime: "2026-10-01T10:20:00.123456Z",
    });
    expect(
      screen.getByText(/Версия fpcalc результата: fpcalc 1\.5/),
    ).toBeVisible();
  });

  it("keeps the prior fingerprint visible while rerun is running and after failure", async () => {
    let phase: "initial" | "running" | "failed" = "initial";
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.6",
            ...(phase === "initial"
              ? {
                  steps: [
                    {
                      name: "fingerprint",
                      state: "succeeded",
                      attempt: 1,
                      fingerprint: {
                        value: "old-fingerprint",
                        version: "fpcalc 1.5",
                        version_banner: "fpcalc 1.5",
                        algorithm_namespace: "chromaprint",
                        algorithm_id: 1,
                        duration: 42,
                        calculated_at: "2026-10-01T10:20:00Z",
                        applied_operation_id: "old",
                        parser_contract_version: 1,
                      },
                    },
                  ],
                }
              : phase === "running"
                ? {
                    active_analysis_operation_id: "rerun-1",
                    steps: [
                      {
                        name: "fingerprint",
                        state: "running",
                        attempt: 2,
                        fingerprint: {
                          value: "old-fingerprint",
                          version: "fpcalc 1.5",
                          version_banner: "fpcalc 1.5",
                          algorithm_namespace: "chromaprint",
                          algorithm_id: 1,
                          duration: 42,
                          calculated_at: "2026-10-01T10:20:00Z",
                          applied_operation_id: "old",
                          parser_contract_version: 1,
                        },
                      },
                    ],
                  }
                : {
                    steps: [
                      {
                        name: "fingerprint",
                        state: "failed",
                        attempt: 2,
                        safe_error: "fpcalc rerun failed",
                        fingerprint: {
                          value: "old-fingerprint",
                          version: "fpcalc 1.5",
                          version_banner: "fpcalc 1.5",
                          algorithm_namespace: "chromaprint",
                          algorithm_id: 1,
                          duration: 42,
                          calculated_at: "2026-10-01T10:20:00Z",
                          applied_operation_id: "old",
                          parser_contract_version: 1,
                        },
                      },
                    ],
                  }),
          }),
        ),
      ),
      http.get("/api/operations/rerun-1", () =>
        HttpResponse.json({
          id: "rerun-1",
          kind: "rerun-source-location-fingerprint",
          state: phase === "failed" ? "failed" : "running",
          stage: phase === "failed" ? "failed" : "fingerprinting",
          bytes_completed: 0,
          created_at: "",
          updated_at: "",
        }),
      ),
      http.post(`${detailPath}/fingerprint/rerun`, () => {
        phase = "running";
        return HttpResponse.json(
          {
            id: "rerun-1",
            state: "queued",
            kind: "rerun-source-location-fingerprint",
            stage: "queued",
            bytes_completed: 0,
            created_at: "",
            updated_at: "",
          },
          { status: 202 },
        );
      }),
    );
    await openInspector();
    fireEvent.click(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    );
    await screen.findByText("Состояние: Выполняется");
    expect(screen.getByText("old-fingerprint")).toBeVisible();

    const failedDetail = responseFor(detailPath);
    phase = "failed";
    await act(async () => {
      stream().dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify({
            id: "rerun-1",
            state: "failed",
            kind: "rerun-source-location-fingerprint",
            stage: "failed",
            bytes_completed: 0,
            created_at: "",
            updated_at: "",
          }),
        }),
      );
      await failedDetail;
    });
    expect(await screen.findByText("old-fingerprint")).toBeVisible();
    expect(screen.getByText("Состояние: Ошибка")).toBeVisible();
    expect(
      screen.getByRole("button", {
        name: "Повторить этап «Акустический отпечаток»",
      }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeDisabled();
    expect(
      screen.queryByText(/Сохранение результата этапа анализа/),
    ).toBeNull();
    expect(stream().close).toHaveBeenCalled();
  });

  it("retries only a failed step through queued, running, and success while preserving siblings", async () => {
    let phase: "failed" | "queued" | "running" | "succeeded" = "failed";
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            active_analysis_operation_id:
              phase === "failed" ? undefined : "retry-sha",
            steps: [
              {
                name: "sha256",
                state:
                  phase === "failed"
                    ? "failed"
                    : phase === "queued"
                      ? "queued"
                      : phase,
                attempt: phase === "failed" ? 1 : 2,
                ...(phase === "failed" ? { safe_error: "digest failed" } : {}),
                ...(phase === "succeeded"
                  ? { sha256: { value: "new-digest" } }
                  : {}),
              },
              { name: "probe", state: "succeeded", attempt: 1 },
              {
                name: "fingerprint",
                state: "succeeded",
                attempt: 1,
                fingerprint: {
                  value: "sibling-fingerprint",
                  version: "fpcalc 1.6",
                  version_banner: "fpcalc 1.6",
                  algorithm_namespace: "chromaprint",
                  algorithm_id: 1,
                  duration: 42,
                  calculated_at: "2026-10-01T10:20:00Z",
                  applied_operation_id: "sibling-op",
                  parser_contract_version: 1,
                },
              },
            ],
          }),
        ),
      ),
      http.post(`${detailPath}/retry`, () => {
        phase = "queued";
        return HttpResponse.json(
          operation({
            id: "retry-sha",
            kind: "retry-source-location-step",
            state: "queued",
          }),
          { status: 202 },
        );
      }),
      http.get("/api/operations/retry-sha", () =>
        HttpResponse.json(
          operation({
            id: "retry-sha",
            kind: "retry-source-location-step",
            state: phase === "queued" ? "queued" : "running",
            stage: phase === "queued" ? "queued" : "hashing",
          }),
        ),
      ),
    );
    await openInspector();
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить этап «SHA-256»" }),
    );

    await screen.findByText("Состояние: В очереди");
    expect(
      screen.getByRole("listitem", { name: "Акустический отпечаток" }),
    ).toHaveTextContent("Состояние: Завершено");
    expect(screen.getByText("sibling-fingerprint")).toBeVisible();

    phase = "running";
    const runningDetail = responseFor(detailPath);
    await act(async () => {
      stream().dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify(
            operation({
              id: "retry-sha",
              state: "running",
              stage: "hashing",
            }),
          ),
        }),
      );
      await runningDetail;
    });
    expect(screen.getByText("Состояние: Выполняется")).toBeVisible();
    expect(screen.getByText("sibling-fingerprint")).toBeVisible();

    phase = "succeeded";
    const succeededDetail = responseFor(detailPath);
    await act(async () => {
      stream().dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify(
            operation({
              id: "retry-sha",
              state: "succeeded",
              stage: "succeeded",
            }),
          ),
        }),
      );
      await succeededDetail;
    });
    expect(screen.getByText("new-digest")).toBeVisible();
    expect(screen.getByText("sibling-fingerprint")).toBeVisible();
    expect(screen.getByRole("listitem", { name: "SHA-256" })).toHaveTextContent(
      "Состояние: Завершено",
    );
  });

  it("replaces the retained fingerprint after a successful rerun and closes the terminal subscription", async () => {
    let phase: "initial" | "running" | "succeeded" = "initial";
    const fingerprint = (value: string, version: string) => ({
      value,
      version,
      version_banner: version,
      algorithm_namespace: "chromaprint",
      algorithm_id: 1,
      duration: 42,
      calculated_at: "2026-10-01T10:20:00Z",
      applied_operation_id: phase === "initial" ? "old" : "rerun-success",
      parser_contract_version: 1,
    });
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.6",
            ...(phase === "running"
              ? { active_analysis_operation_id: "rerun-success" }
              : {}),
            steps: [
              {
                name: "fingerprint",
                state: phase === "running" ? "running" : "succeeded",
                attempt: phase === "initial" ? 1 : 2,
                fingerprint:
                  phase !== "succeeded"
                    ? fingerprint("old-fingerprint", "fpcalc 1.5")
                    : fingerprint("new-fingerprint", "fpcalc 1.6"),
              },
            ],
          }),
        ),
      ),
      http.post(`${detailPath}/fingerprint/rerun`, () => {
        phase = "running";
        return HttpResponse.json(
          operation({
            id: "rerun-success",
            kind: "rerun-source-location-fingerprint",
            state: "queued",
          }),
          { status: 202 },
        );
      }),
      http.get("/api/operations/rerun-success", () =>
        HttpResponse.json(
          operation({
            id: "rerun-success",
            kind: "rerun-source-location-fingerprint",
            state: "running",
            stage: "fingerprinting",
          }),
        ),
      ),
    );
    await openInspector();
    fireEvent.click(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    );
    await screen.findByText("Состояние: Выполняется");
    const subscription = stream();

    phase = "succeeded";
    const refreshed = responseFor(detailPath);
    await act(async () => {
      subscription.dispatchEvent(
        new MessageEvent("operation-changed", {
          data: JSON.stringify(
            operation({
              id: "rerun-success",
              kind: "rerun-source-location-fingerprint",
              state: "succeeded",
              stage: "succeeded",
            }),
          ),
        }),
      );
      await refreshed;
    });

    expect(screen.getByText("new-fingerprint")).toBeVisible();
    expect(
      screen.getByText(/Версия fpcalc результата: fpcalc 1\.6/),
    ).toBeVisible();
    expect(screen.queryByText("old-fingerprint")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeDisabled();
    expect(screen.queryByText(/Этап анализа поставлен в очередь/)).toBeNull();
    expect(subscription.close).toHaveBeenCalled();
  });

  it("shows all independent step errors and preserves successful technical data", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            probe_status: "probe_error",
            steps: [
              {
                name: "sha256",
                state: "failed",
                attempt: 2,
                safe_error: "hash failed",
              },
              { name: "probe", state: "succeeded", attempt: 1 },
              {
                name: "fingerprint",
                state: "failed",
                attempt: 1,
                safe_error: "fingerprint failed",
              },
            ],
          }),
        ),
      ),
    );
    await openInspector();
    expect(screen.getByText("hash failed")).toBeVisible();
    expect(screen.getByText("fingerprint failed")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Повторить этап «SHA-256»" }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", {
        name: "Повторить этап «Акустический отпечаток»",
      }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "Анализировать" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Повторить попытку операции" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("matroska")).toBeVisible();
  });

  it("refreshes file identity when step admission is stale", async () => {
    let reads = 0;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            ...(reads++ === 0
              ? {
                  steps: [
                    {
                      name: "sha256",
                      state: "failed",
                      attempt: 1,
                      safe_error: "old failure",
                    },
                  ],
                }
              : {
                  size_bytes: 2048,
                  steps: [
                    {
                      name: "sha256",
                      state: "failed",
                      attempt: 1,
                      safe_error: "old failure",
                    },
                  ],
                }),
          }),
        ),
      ),
      http.post(`${detailPath}/retry`, () =>
        HttpResponse.json({ detail: "identity changed" }, { status: 409 }),
      ),
    );
    await openInspector();
    const refreshed = responseFor(detailPath);
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить этап «SHA-256»" }),
    );
    await act(async () => {
      await refreshed;
    });
    expect(screen.getByText(/identity changed/)).toBeVisible();
    expect(screen.getByText("old failure")).toBeVisible();
    expect(screen.getByText(/2\s048/).parentElement).toHaveTextContent(
      /2\s048\s*Б/,
    );
  });

  it("disables retries for unavailable roots and active-operation contention", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_analysis_operation_id: "active-1",
            steps: [
              {
                name: "sha256",
                state: "failed",
                attempt: 1,
                safe_error: "failed",
              },
            ],
          }),
        ),
      ),
      http.get("/api/operations/active-1", () =>
        HttpResponse.json({
          id: "active-1",
          state: "running",
          kind: "retry-source-location-step",
          stage: "hashing",
          bytes_completed: 0,
          created_at: "",
          updated_at: "",
        }),
      ),
    );
    await openInspector();
    expect(
      screen.queryByRole("button", { name: "Повторить этап «SHA-256»" }),
    ).not.toBeInTheDocument();
  });
});
