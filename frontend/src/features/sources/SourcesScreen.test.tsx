import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { selectDirectoryPath } from "../../test/selectDirectoryPath";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";

function root(overrides: Partial<SourceRootResponse> = {}): SourceRootResponse {
  return {
    id: "root-1",
    display_name: "Входящие",
    configured_path: "/srv/inbox",
    processing_mode: "in_place",
    enabled: true,
    status: "available",
    stale: false,
    scan_generation: 4,
    location_count: 12,
    inventory_path: "/srv/inbox",
    last_successful_scan_at: "2026-09-26T10:20:00Z",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T10:20:00Z",
    ...overrides,
  };
}

const json = (roots: SourceRootResponse[]) =>
  HttpResponse.json({ sources: roots });

// The detail page lists the published inventory below the root metadata, so
// every detail fixture answers the locations request as well.
function locationsHandler(published: SourceLocationResponse[]) {
  return http.get("/api/sources/:sourceId/locations", () =>
    HttpResponse.json({ locations: published }),
  );
}

beforeEach(() => {
  window.location.hash = "";
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
  );
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
      this.querySelector<HTMLElement>(
        "button:not(:disabled), input:not(:disabled)",
      )?.focus();
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
});
afterEach(() => {
  cleanup();
  window.location.hash = "";
  vi.unstubAllGlobals();
});

describe("source root creation", () => {
  it("registers a server path and reloads the list", async () => {
    vi.stubGlobal(
      "EventSource",
      class extends EventTarget {
        close() {}
      },
    );
    const registered = root({
      id: "root-disk",
      display_name: "Диск",
      configured_path: "/srv/disk",
      processing_mode: "staged",
      status: "unknown",
      scan_generation: 0,
      location_count: 0,
      inventory_path: undefined,
      last_successful_scan_at: undefined,
    });
    const created: SourceRootResponse[] = [];
    const scan = {
      id: "scan-new-root",
      kind: "scan_source",
      state: "queued",
      stage: "queued",
      target_source_root_id: "root-disk",
      bytes_completed: 0,
      created_at: "2026-09-30T00:00:00Z",
      updated_at: "2026-09-30T00:00:00Z",
    };
    let scanRequests = 0;
    server.use(
      http.get("/api/sources", () => json(created)),
      http.get("/api/sources/root-disk", () => HttpResponse.json(registered)),
      locationsHandler([]),
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: scanRequests ? [scan] : [] }),
      ),
      http.get("/api/operations/scan-new-root", () => HttpResponse.json(scan)),
      http.post("/api/sources/root-disk/scan", () => {
        scanRequests += 1;
        return HttpResponse.json(scan);
      }),
      http.post("/api/sources", async ({ request }) => {
        expect(await request.json()).toEqual({
          display_name: "Диск",
          configured_path: "/srv/disk",
          processing_mode: "staged",
        });
        created.push(registered);
        return HttpResponse.json(registered);
      }),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    expect(
      await screen.findByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Добавить каталог" }));
    fireEvent.change(screen.getByLabelText("Имя каталога"), {
      target: { value: "Диск" },
    });
    await selectDirectoryPath("Путь на сервере", "/srv/disk");
    expect(screen.getByLabelText("Режим обработки")).toHaveValue("");
    fireEvent.change(screen.getByLabelText("Режим обработки"), {
      target: { value: "staged" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Зарегистрировать каталог" }),
    );

    expect(
      await screen.findByRole("heading", { level: 1, name: "Диск" }),
    ).toBeVisible();
    expect(
      await screen.findByText("Сканирование поставлено в очередь."),
    ).toBeVisible();
    expect(scanRequests).toBe(1);
    expect(window.location.hash).toBe("#/sources/root-disk");
    fireEvent.click(screen.getByRole("button", { name: "К списку каталогов" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toHaveFocus();
    fireEvent.click(
      await screen.findByRole("button", { name: "Добавить каталог" }),
    );
    expect(screen.getByLabelText("Режим обработки")).toHaveValue("");
  });
});
