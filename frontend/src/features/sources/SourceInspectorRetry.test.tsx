import { act, fireEvent, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import {
  detail,
  detailPath,
  openInspector,
  responseFor,
  result,
} from "./sourceInspectorTestSupport";

describe("source inspector step controls", () => {
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
