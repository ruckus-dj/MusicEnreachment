import { fireEvent, render, screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { SourceAnalysisStepResponse } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";
import {
  detail,
  detailPath,
  observe,
  openInspector,
  result,
  stamp,
} from "./sourceInspectorTestSupport";

function fingerprintStep(
  overrides: Partial<SourceAnalysisStepResponse> = {},
): SourceAnalysisStepResponse {
  return {
    name: "fingerprint",
    state: "succeeded",
    attempt: 1,
    fingerprint: {
      value: "old-fingerprint",
      version: "fpcalc 1.1",
      algorithm_id: 1,
      algorithm_namespace: "chromaprint",
      applied_operation_id: "previous-operation",
      calculated_at: stamp,
      duration: 12.5,
      parser_contract_version: 1,
      version_banner: "fpcalc version 1.1",
    },
    ...overrides,
  };
}

describe("source inspector", () => {
  it("opens a nested address and reads the source location", async () => {
    window.location.hash = "/sources/root-1/locations/file-1";
    render(<SourcesScreen />);
    await observe(() => !!screen.queryByText("album/01.flac"));
    expect(
      screen.getByRole("heading", { name: "Инспектор файла" }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Каталог источника" }),
    ).toHaveAttribute("href", "#/sources/root-1");
    window.location.hash = "";
  });

  it("shows a missing file and allows the read to be retried", async () => {
    server.use(
      http.get(detailPath, () => HttpResponse.json({}, { status: 404 })),
    );
    await openInspector();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Файл или каталог не найден",
    );
    expect(
      screen.getByRole("button", { name: "Повторить загрузку" }),
    ).toBeEnabled();
  });

  it("renders saved technical data and keeps the raw JSON accessible", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result: {
              ...result,
              ffprobe_version: "ffprobe 8.0\nbuilt with clang",
            },
            steps: [{ name: "probe", state: "succeeded", attempt: 1 }],
          }),
        ),
      ),
    );
    await openInspector();
    expect(screen.getByText(/Анализ от/)).toHaveTextContent(
      new Date(stamp).toLocaleString(),
    );
    expect(screen.getByText("First / artist; preserved")).toBeVisible();
    const trigger = screen.getByRole("button", {
      name: "Исходный JSON ffprobe",
    });
    fireEvent.click(trigger);
    expect(
      within(
        screen.getByRole("region", { name: "Исходный JSON ffprobe" }),
      ).getByText(/codec_type/),
    ).toBeVisible();
  });

  it("does not offer an all-in-one analyze action and gates retries on root readiness", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            root: {
              enabled: false,
              stale: false,
              status: "available",
              inventory_path: "/srv/inbox",
            },
            steps: [
              {
                name: "sha256",
                state: "failed",
                attempt: 1,
                safe_error: "hash failed",
              },
            ],
          }),
        ),
      ),
    );
    await openInspector();
    expect(
      screen.queryByRole("button", { name: "Анализировать" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Повторить этап «SHA-256»" }),
    ).not.toBeInTheDocument();
  });

  it("does not fabricate successful state for steps absent from the persisted response", async () => {
    await openInspector();
    const steps = screen.getAllByRole("listitem");
    expect(steps).toHaveLength(3);
    for (const step of steps) {
      expect(step).toHaveTextContent("Состояние: Загрузка");
    }
    expect(screen.queryByText("Завершено")).not.toBeInTheDocument();
  });

  it("offers a fingerprint rerun for a succeeded result saved under an older version", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.2",
            steps: [fingerprintStep()],
          }),
        ),
      ),
    );
    await openInspector();
    expect(screen.getByText("old-fingerprint").parentElement).toHaveTextContent(
      "Отпечаток: old-fingerprint",
    );
    expect(
      screen.getByText(/Версия fpcalc результата: fpcalc 1.1/),
    ).toHaveTextContent("Активная версия fpcalc: fpcalc 1.2");
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeEnabled();
  });

  it("keeps a retained fingerprint after a failure but does not offer a rerun", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.2",
            steps: [
              fingerprintStep({
                state: "failed",
                safe_error: "fpcalc завершился с ошибкой",
              }),
            ],
          }),
        ),
      ),
    );
    await openInspector();
    expect(screen.getByText("old-fingerprint").parentElement).toHaveTextContent(
      "Отпечаток: old-fingerprint",
    );
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Повторить этап «Акустический отпечаток»",
      }),
    ).toBeEnabled();
  });

  it("does not offer a rerun when the saved result already uses the active version", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_fpcalc_version: "fpcalc 1.1",
            steps: [fingerprintStep()],
          }),
        ),
      ),
    );
    await openInspector();
    expect(
      screen.queryByText(/Активная версия fpcalc: fpcalc/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeDisabled();
  });

  it("does not treat an unknown active version as a version change", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(detail({ steps: [fingerprintStep()] })),
      ),
    );
    await openInspector();
    expect(
      screen.queryByText(/Активная версия fpcalc: fpcalc/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Активная версия fpcalc недоступна."),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Повторно вычислить отпечаток" }),
    ).toBeDisabled();
  });
});
