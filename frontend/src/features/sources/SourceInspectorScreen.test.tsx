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
  operation,
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
  it.each([
    ["unknown", "Неизвестно"],
    ["preparation", "Подготовка"],
    ["acquiring", "Получение артефакта"],
    ["ready", "Готов"],
    ["retained", "Сохранён"],
    ["cleanup_eligible", "Ожидает очистки"],
    ["cleanup_failed", "Ошибка очистки"],
  ] as const)("projects staged artifact state %s as %s", async (state, label) => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state,
              requested_steps: [],
              requested_steps_known: false,
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(
      screen.getByRole("region", { name: "Промежуточный артефакт анализа" }),
    ).toHaveTextContent(`Состояние: ${label}`);
    expect(
      screen.getByText(/История запрошенных этапов неизвестна/),
    ).toBeVisible();
  });

  it("explains known requested steps and only reports reuse across different operations", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state: "retained",
              requested_steps: ["sha256", "probe"],
              requested_steps_known: true,
              creator_operation_id: "creator-op",
              borrower_operation_id: "borrower-op",
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByText("Запрошенные этапы: sha256, probe")).toBeVisible();
    expect(screen.getByText(/создан другой операцией/)).toBeVisible();
  });

  it("keeps successful analysis visible when staged artifact cleanup fails", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            steps: [{ name: "probe", state: "succeeded", attempt: 1 }],
            staged_artifact: {
              state: "cleanup_failed",
              requested_steps: [],
              requested_steps_known: false,
              safe_error: "Удаление недоступно",
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Очистить промежуточный артефакт не удалось: Удаление недоступно",
    );
    expect(screen.getByText("First / artist; preserved")).toBeVisible();
    expect(screen.getByText(/Анализ от/)).toBeVisible();
  });

  it("does not claim a ready artifact exists on disk or invent progress", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state: "ready",
              requested_steps: ["sha256"],
              requested_steps_known: true,
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(
      screen.getByText(/не подтверждает наличие файла на диске/),
    ).toBeVisible();
    expect(screen.queryByText(/%|байт из/)).not.toBeInTheDocument();
  });

  it("does not describe artifact reuse when creator and borrower are the same", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state: "retained",
              requested_steps: [],
              requested_steps_known: true,
              creator_operation_id: "same-op",
              borrower_operation_id: "same-op",
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(
      screen.queryByText(/создан другой операцией/),
    ).not.toBeInTheDocument();
  });

  it("treats a legacy payload without a staged artifact projection as explicitly unknown", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result,
            steps: [{ name: "probe", state: "succeeded", attempt: 1 }],
            // A legacy server never sent the projection; JSON drops the key.
            staged_artifact: undefined,
          }),
        ),
      ),
    );
    await openInspector();

    const region = screen.getByRole("region", {
      name: "Промежуточный артефакт анализа",
    });
    expect(region).toHaveTextContent("Состояние: Неизвестно");
    expect(region).toHaveTextContent(
      "Сведения о промежуточном артефакте неизвестны.",
    );
    expect(
      within(region).getByText(/История запрошенных этапов неизвестна/),
    ).toBeVisible();
    // The successful analysis stays visible beside the unknown projection.
    expect(screen.getByText("First / artist; preserved")).toBeVisible();
  });

  it("never reports completion for a known but empty requested step list", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state: "retained",
              requested_steps: [],
              requested_steps_known: true,
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByText("Запрошенные этапы: Нет")).toBeVisible();
    expect(
      screen.getByText(
        /Пустой список запрошенных этапов не означает, что этапы завершены/,
      ),
    ).toBeVisible();
  });

  it("reads a null requested step list safely instead of crashing", async () => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            staged_artifact: {
              state: "retained",
              requested_steps: null,
              requested_steps_known: true,
            },
          }),
        ),
      ),
    );
    await openInspector();

    expect(screen.getByText("Запрошенные этапы: Нет")).toBeVisible();
  });

  it.each([
    "probing",
    "future-running-stage",
  ])("describes a fingerprint-only running operation neutrally at stage %s", async (stage) => {
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_analysis_operation_id: "analysis-1",
            steps: [fingerprintStep({ state: "running" })],
          }),
        ),
      ),
      http.get("/api/operations/analysis-1", () =>
        HttpResponse.json(operation({ state: "running", stage })),
      ),
    );
    await openInspector();
    expect(
      await screen.findByText("Выполняется этап анализа файла."),
    ).toBeVisible();
    expect(screen.getByText("old-fingerprint")).toBeVisible();
    expect(
      screen.queryByText("Чтение технических данных ffprobe."),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Этап анализа поставлен в очередь."),
    ).not.toBeInTheDocument();
  });

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
    expect(steps).toHaveLength(4);
    for (const step of steps) {
      expect(step).toHaveTextContent("Состояние: Не запрошено");
    }
    expect(
      screen.getByRole("listitem", { name: "Метаданные тегов" }),
    ).toHaveTextContent("Состояние: Не запрошено");
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
