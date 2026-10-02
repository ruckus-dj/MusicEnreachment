import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../../test/server";
import { SourceInspectorScreen } from "./SourceInspectorScreen";
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

describe("source inspector reads", () => {
  it("opens a direct nested address when the file exists", async () => {
    // Given
    window.location.hash = "/sources/root-1/locations/file-1";
    // When
    render(<SourcesScreen />);
    await observe(() => !!screen.queryByText("album/01.flac"));
    // Then
    expect(
      screen.getByRole("heading", { name: "Инспектор файла" }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Каталог источника" }),
    ).toHaveAttribute("href", "#/sources/root-1");
    window.location.hash = "";
  });
  it("prevents start while detail discovery has not answered", async () => {
    // Given
    let release: (response: Response) => void = () => {};
    const requested = new Promise<void>((resolve) => {
      server.use(
        http.get(
          detailPath,
          () =>
            new Promise<Response>((done) => {
              release = done;
              resolve();
            }),
        ),
      );
    });
    // When
    render(<SourceInspectorScreen sourceId="root-1" locationId="file-1" />);
    await requested;
    // Then
    expect(screen.getByRole("status")).toHaveTextContent("Загрузка файла");
    expect(
      screen.queryByRole("button", { name: "Анализировать" }),
    ).not.toBeInTheDocument();
    await act(async () => {
      release(HttpResponse.json(detail()));
    });
    await observe(
      () => !!screen.queryByRole("button", { name: "Анализировать" }),
    );
  });
  it("shows a missing file instead of a blank inspector when GET returns 404", async () => {
    // Given
    server.use(
      http.get(detailPath, () => HttpResponse.json({}, { status: 404 })),
    );
    // When
    await openInspector();
    // Then
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Файл или каталог не найден",
    );
    expect(
      screen.getByRole("button", { name: "Повторить загрузку" }),
    ).toBeEnabled();
  });
  it("keeps start disabled while an active operation snapshot is being discovered", async () => {
    // Given
    let release: (response: Response) => void = () => {};
    let reads = 0;
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            active_analysis_operation_id: "analysis-1",
          }),
        ),
      ),
    );
    const requested = new Promise<void>((resolve) => {
      server.use(
        http.get("/api/operations/analysis-1", () => {
          reads += 1;
          if (reads > 1)
            return HttpResponse.json(
              operation({ state: "running", stage: "probing" }),
            );
          return new Promise<Response>((done) => {
            release = done;
            resolve();
          });
        }),
      );
    });
    // When
    render(<SourceInspectorScreen sourceId="root-1" locationId="file-1" />);
    await requested;
    await observe(
      () => !!screen.queryByRole("button", { name: "Анализировать" }),
    );
    // Then
    expect(
      screen.getByRole("button", { name: "Анализировать" }),
    ).toBeDisabled();
    await act(async () => {
      release(
        HttpResponse.json(operation({ state: "running", stage: "probing" })),
      );
    });
    await observe(
      () => !!screen.queryByText("Чтение технических данных ffprobe."),
    );
  });
  it("offers explicit analysis when the file has no result", async () => {
    // Given / When
    await openInspector();
    // Then
    expect(screen.getByText("Файл ещё не анализировался.")).toBeVisible();
    expect(screen.getByRole("button", { name: "Анализировать" })).toBeEnabled();
    expect(
      screen.queryByText("Сохранённый технический результат"),
    ).not.toBeInTheDocument();
  });
  it("shows every stream, untouched tags and accessible raw JSON when analysis succeeded", async () => {
    // Given
    const fullVersion =
      "ffprobe 8.0\nbuilt with clang\nconfiguration: --enable-gpl";
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(
          detail({
            result: { ...result, ffprobe_version: fullVersion },
            analysis_state: "analyzed",
          }),
        ),
      ),
    );
    // When
    await openInspector();
    // Then
    expect(screen.getByText(/Анализ от/)).toHaveTextContent(
      `Анализ от ${new Date(stamp).toLocaleString()} · ffprobe 8.0 · Политика 1`,
    );
    expect(screen.getByText("ffprobe 8.0")).toHaveAttribute(
      "title",
      fullVersion,
    );
    expect(screen.queryByText(/configuration:/)).not.toBeInTheDocument();
    expect(screen.getByText("3:00:30")).toBeVisible();
    expect(
      within(screen.getByRole("region", { name: "Аудиопоток 0" })).getByText(
        "24 бит",
      ),
    ).toBeVisible();
    expect(
      within(screen.getByRole("region", { name: "Аудиопоток 3" })).getAllByText(
        "Неизвестно",
      ).length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("First / artist; preserved")).toBeVisible();
    const trigger = screen.getByRole("button", {
      name: "Исходный JSON ffprobe",
    });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(
      screen.getByRole("region", { name: "Исходный JSON ffprobe" }),
    ).toHaveTextContent('"codec_type": "video"');
  });
  it.each([
    {
      enabled: false,
      stale: false,
      status: "available" as const,
      note: "Каталог выключен",
    },
    {
      enabled: true,
      stale: true,
      status: "available" as const,
      note: "Инвентарь устарел",
    },
    {
      enabled: true,
      stale: false,
      status: "unavailable" as const,
      note: "Каталог недоступен",
    },
  ])("keeps the result separate and disables start when root is $note", async ({
    note,
    ...root
  }) => {
    // Given
    server.use(
      http.get(detailPath, () =>
        HttpResponse.json(detail({ root, result, analysis_state: "analyzed" })),
      ),
    );
    // When
    await openInspector();
    // Then
    expect(screen.getByText(new RegExp(note))).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Повторить анализ" }),
    ).toBeDisabled();
    expect(screen.getByText("matroska")).toBeVisible();
  });
});
