// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DashboardScreen } from "./DashboardScreen";

afterEach(cleanup);

describe("DashboardScreen", () => {
  it("exposes all management actions from the main screen", () => {
    const onScan = vi.fn();
    const onReconcilePublications = vi.fn();
    render(
      <DashboardScreen
        scanning={false}
        reprocessing={false}
        refreshingMetadata={false}
        reconcilingPublications={false}
        onScan={onScan}
        onReprocessAll={vi.fn()}
        onRefreshMetadata={vi.fn()}
        onReconcilePublications={onReconcilePublications}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Обновить подтверждённые пары MusicBrainz" }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Переобработать всю медиатеку" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Сканировать новые и\u00a0изменённые" }));
    fireEvent.click(screen.getByRole("button", { name: "Проверить папку публикаций" }));

    expect(onScan).toHaveBeenCalledOnce();
    expect(onReconcilePublications).toHaveBeenCalledOnce();
  });

  it("disables every operation while publication reconciliation is active", () => {
    render(
      <DashboardScreen
        scanning={false}
        reprocessing={false}
        refreshingMetadata={false}
        reconcilingPublications
        onScan={vi.fn()}
        onReprocessAll={vi.fn()}
        onRefreshMetadata={vi.fn()}
        onReconcilePublications={vi.fn()}
      />,
    );

    expect(screen.getAllByRole("button").every((button) => button.hasAttribute("disabled"))).toBe(
      true,
    );
    expect(screen.getByRole("button", { name: "Проверяем публикации…" })).toBeTruthy();
  });
});
