import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import {
  SourceAnalysisSteps,
  type SourceAnalysisStepsProps,
} from "./SourceAnalysisSteps";

const probeResult: SourceTechnicalResultResponse = {
  analysis_policy_version: 1,
  applied_operation_id: "operation-1",
  container: {},
  ffprobe_version: "ffprobe version test",
  inspected_at: "2026-09-01T00:00:00Z",
  raw_json: {},
  streams: [],
  tags: {},
};

function props(
  overrides: Partial<SourceAnalysisStepsProps> = {},
): SourceAnalysisStepsProps {
  return {
    steps: {
      sha256: { state: "succeeded" },
      probe: { state: "succeeded" },
      fingerprint: { state: "succeeded" },
    },
    sha256: { value: "abc123" },
    probeResult,
    fingerprint: { value: "123456", version: "fpcalc 1.2" },
    retryAvailable: { sha256: true, probe: true, fingerprint: true },
    onRetry: vi.fn(),
    fingerprintRerunAvailable: true,
    onRerunFingerprint: vi.fn(),
    activeFingerprintVersion: "fpcalc 1.2",
    matching: { eligible: true },
    ...overrides,
  };
}

describe("SourceAnalysisSteps", () => {
  it("keeps independent SHA and fingerprint successes visible when probe fails", () => {
    const onRetry = vi.fn();
    render(
      <SourceAnalysisSteps
        {...props({
          steps: {
            sha256: { state: "succeeded" },
            probe: { state: "failed", safeError: "Ошибка безопасной проверки" },
            fingerprint: { state: "succeeded" },
          },
          onRetry,
        })}
      />,
    );

    expect(screen.getByText("abc123").parentElement).toHaveTextContent(
      "SHA-256: abc123",
    );
    expect(screen.getByText("123456").parentElement).toHaveTextContent(
      "Отпечаток: 123456",
    );
    expect(screen.getAllByRole("alert")).toHaveLength(2);
    expect(screen.getAllByRole("alert")[0]).toHaveTextContent(
      "Ошибка этапа «Технический анализ ffprobe»: Ошибка безопасной проверки",
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Повторить этап «Технический анализ ffprobe»",
      }),
    );
    expect(onRetry).toHaveBeenCalledExactlyOnceWith("probe");
  });

  it("retains the previous fingerprint version after a failed rerun", () => {
    render(
      <SourceAnalysisSteps
        {...props({
          steps: {
            sha256: { state: "succeeded" },
            probe: { state: "succeeded" },
            fingerprint: {
              state: "failed",
              safeError: "fpcalc завершился с ошибкой",
            },
          },
          fingerprint: { value: "old-fingerprint", version: "fpcalc 1.1" },
          activeFingerprintVersion: "fpcalc 1.2",
        })}
      />,
    );

    expect(screen.getByText("old-fingerprint").parentElement).toHaveTextContent(
      "Отпечаток: old-fingerprint",
    );
    expect(
      screen.getByText(/Версия fpcalc результата: fpcalc 1.1/),
    ).toHaveTextContent("Активная версия fpcalc: fpcalc 1.2");
    const alerts = screen.getAllByRole("alert");
    expect(alerts).toHaveLength(2);
    for (const alert of alerts) {
      expect(alert).toHaveTextContent("fpcalc завершился с ошибкой");
    }
  });

  it("summarizes the first error and keeps every step error visible", () => {
    render(
      <SourceAnalysisSteps
        {...props({
          steps: {
            sha256: { state: "failed", safeError: "Ошибка SHA" },
            probe: { state: "failed", safeError: "Ошибка ffprobe" },
            fingerprint: { state: "succeeded" },
          },
        })}
      />,
    );

    expect(screen.getAllByRole("alert")).toHaveLength(3);
    expect(screen.getAllByRole("alert")[0]).toHaveTextContent(
      "Ошибка этапа «SHA-256»: Ошибка SHA",
    );
    expect(screen.getAllByRole("alert")[2]).toHaveTextContent("Ошибка ffprobe");
  });

  it("does not offer a SHA retry when it is unavailable", () => {
    render(
      <SourceAnalysisSteps
        {...props({
          steps: {
            sha256: { state: "failed", safeError: "Ошибка SHA" },
            probe: { state: "succeeded" },
            fingerprint: { state: "succeeded" },
          },
          retryAvailable: { sha256: false, probe: true, fingerprint: true },
        })}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "Повторить этап «SHA-256»" }),
    ).not.toBeInTheDocument();
  });

  it("keeps successful execution data when matching is unsupported", () => {
    render(
      <SourceAnalysisSteps
        {...props({
          matching: {
            eligible: false,
            reason: "Недостаточно данных для сопоставления",
          },
        })}
      />,
    );

    expect(screen.getByText("abc123").parentElement).toHaveTextContent(
      "SHA-256: abc123",
    );
    expect(screen.getByText("123456").parentElement).toHaveTextContent(
      "Отпечаток: 123456",
    );
    expect(
      screen.getByText(/Недоступно · Недостаточно данных для сопоставления/),
    ).toBeInTheDocument();
  });

  it.each([
    "pending",
    "running",
  ] as const)("disables duplicate fingerprint work while %s", (state) => {
    const onRerunFingerprint = vi.fn();
    render(
      <SourceAnalysisSteps
        {...props({
          steps: {
            sha256: { state: "succeeded" },
            probe: { state: "succeeded" },
            fingerprint: { state },
          },
          onRerunFingerprint,
        })}
      />,
    );

    const button = screen.getByRole("button", {
      name: "Повторно вычислить отпечаток",
    });
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(onRerunFingerprint).not.toHaveBeenCalled();
  });

  it("shows the active version when it differs from the saved result", () => {
    render(
      <SourceAnalysisSteps
        {...props({
          fingerprint: { value: "123456", version: "fpcalc old" },
          activeFingerprintVersion: "fpcalc active",
        })}
      />,
    );

    expect(
      screen.getByText(/Версия fpcalc результата: fpcalc old/),
    ).toHaveTextContent("Активная версия fpcalc: fpcalc active");
  });
});
