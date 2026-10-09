import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
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

const neverScanned = root({
  id: "root-new",
  display_name: "Новый",
  configured_path: "/srv/new",
  status: "unknown",
  scan_generation: 0,
  location_count: 0,
  inventory_path: undefined,
  last_successful_scan_at: undefined,
});
const scannedEmpty = root({
  id: "root-empty",
  display_name: "Пустой",
  configured_path: "/srv/empty",
  scan_generation: 2,
  location_count: 0,
});
const staleRoot = root({
  id: "root-stale",
  display_name: "Переехавший",
  configured_path: "/srv/new-home",
  inventory_path: "/srv/old-home",
  stale: true,
});
const unavailableRoot = root({
  id: "root-offline",
  display_name: "Архив",
  configured_path: "/srv/archive",
  status: "unavailable",
  safe_error: "source directory is not readable",
});
const disabledRoot = root({
  id: "root-spare",
  display_name: "Запасной",
  configured_path: "/srv/spare",
  enabled: false,
});

const json = (roots: SourceRootResponse[]) =>
  HttpResponse.json({ sources: roots });
const conflict = () =>
  HttpResponse.json(
    { detail: "source root has an active scan" },
    { status: 409 },
  );

function location(
  overrides: Partial<SourceLocationResponse> = {},
): SourceLocationResponse {
  return {
    id: "loc-1",
    relative_path: "Альбом/01 Открытие.flac",
    size_bytes: 1536,
    mtime: "2026-09-26T10:20:00Z",
    probe_status: "audio",
    has_result: false,
    ...overrides,
  };
}

// The detail page lists the published inventory below the root metadata, so
// every detail fixture answers the locations request as well.
function locationsHandler(published: SourceLocationResponse[] = [location()]) {
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

async function renderDetail(
  fixture: SourceRootResponse,
  published: SourceLocationResponse[] = [location()],
) {
  server.use(
    http.get("/api/sources/:sourceId", () => HttpResponse.json(fixture)),
    locationsHandler(published),
  );
  window.location.hash = `/sources/${fixture.id}`;
  render(<SourcesScreen />);
  await screen.findByRole("heading", {
    level: 1,
    name: fixture.display_name,
  });
}

describe("source root list", () => {
  it("renders every root with its availability, inventory state and last scan", async () => {
    server.use(
      http.get("/api/sources", () =>
        json([
          root(),
          neverScanned,
          scannedEmpty,
          staleRoot,
          unavailableRoot,
          disabledRoot,
        ]),
      ),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);
    expect(screen.getByRole("status")).toHaveTextContent("Загрузка каталогов");

    expect(await screen.findByText("Входящие")).toBeVisible();

    const inbox = screen.getByRole("row", { name: /Входящие/ });
    expect(within(inbox).getByText("/srv/inbox")).toBeVisible();
    expect(within(inbox).getByText("Доступен")).toBeVisible();
    expect(within(inbox).getByText("Включён")).toBeVisible();
    expect(
      within(inbox).getByText(
        new Date("2026-09-26T10:20:00Z").toLocaleString(),
      ),
    ).toBeVisible();
    expect(within(inbox).getByText("12 файлов")).toBeVisible();

    const fresh = screen.getByRole("row", { name: /Новый/ });
    expect(within(fresh).getByText("Не проверен")).toBeVisible();
    expect(within(fresh).getByText("Не сканировался")).toBeVisible();
    expect(within(fresh).getByText("Не выполнялось")).toBeVisible();

    const empty = screen.getByRole("row", { name: /Пустой/ });
    expect(within(empty).getByText("0 файлов")).toBeVisible();
    expect(
      within(empty).getByText(
        "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
      ),
    ).toBeVisible();

    const moved = screen.getByRole("row", { name: /Переехавший/ });
    expect(
      within(moved).getByText(
        "Инвентарь относится к прежнему пути /srv/old-home.",
      ),
    ).toBeVisible();
    expect(within(moved).getByText("/srv/new-home")).toBeVisible();

    const archive = screen.getByRole("row", { name: /Архив/ });
    expect(within(archive).getByText("Недоступен")).toBeVisible();
    expect(
      within(archive).getByText(
        "Каталог недоступен: показан инвентарь последнего успешного сканирования.",
      ),
    ).toBeVisible();
    expect(
      within(archive).getByText("source directory is not readable"),
    ).toBeVisible();

    const spare = screen.getByRole("row", { name: /Запасной/ });
    expect(within(spare).getByText("Выключен")).toBeVisible();
  });

  it("states that no root is registered when the list is empty", async () => {
    server.use(http.get("/api/sources", () => json([])));
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    expect(
      await screen.findByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("reports a load failure on the alert, then loads after a retry", async () => {
    let failing = true;
    server.use(
      http.get("/api/sources", () =>
        failing
          ? HttpResponse.json(
              { detail: "database unavailable" },
              { status: 503 },
            )
          : json([root()]),
      ),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("database unavailable");
    await waitFor(() => expect(alert).toHaveFocus());

    failing = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(await screen.findByText("Входящие")).toBeVisible();
  });

  it("opens the selected root at its own address", async () => {
    server.use(
      http.get("/api/sources", () => json([root()])),
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Открыть Входящие" }),
    );
    expect(
      await screen.findByRole("heading", { level: 1, name: "Входящие" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/sources/root-1");
  });
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
    fireEvent.change(screen.getByLabelText("Путь на сервере"), {
      target: { value: "/srv/disk" },
    });
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

  it("keeps the registered root after scan admission fails and offers manual scan", async () => {
    const registered = root({
      id: "root-retained",
      display_name: "Сохранённый",
      configured_path: "/srv/retained",
      status: "unknown",
      scan_generation: 0,
      location_count: 0,
      inventory_path: undefined,
      last_successful_scan_at: undefined,
    });
    const scans = [] as string[];
    server.use(
      http.get("/api/sources", () => json([registered])),
      http.post("/api/sources", () => HttpResponse.json(registered)),
      http.post("/api/sources/root-retained/scan", () => {
        scans.push("attempt");
        return HttpResponse.json(
          { detail: "source root has an active scan" },
          { status: 409 },
        );
      }),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Добавить каталог" }),
    );
    fireEvent.change(screen.getByLabelText("Имя каталога"), {
      target: { value: "Сохранённый" },
    });
    fireEvent.change(screen.getByLabelText("Путь на сервере"), {
      target: { value: "/srv/retained" },
    });
    fireEvent.change(screen.getByLabelText("Режим обработки"), {
      target: { value: "in_place" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Зарегистрировать каталог" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "зарегистрирован, но запуск сканирования не принят",
    );
    expect(screen.getByRole("row", { name: /Сохранённый/ })).toBeVisible();
    expect(scans).toHaveLength(1);
    expect(
      screen.getByRole("button", {
        name: "Открыть каталог для ручного сканирования",
      }),
    ).toBeVisible();
  });

  it("keeps the create form and explains a rejected path", async () => {
    server.use(
      http.get("/api/sources", () => json([])),
      http.post("/api/sources", () =>
        HttpResponse.json(
          { detail: "source root could not be registered" },
          { status: 400 },
        ),
      ),
    );
    window.location.hash = "/sources";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Добавить каталог" }),
    );
    fireEvent.change(screen.getByLabelText("Имя каталога"), {
      target: { value: "Диск" },
    });
    fireEvent.change(screen.getByLabelText("Путь на сервере"), {
      target: { value: "/srv/missing" },
    });
    fireEvent.change(screen.getByLabelText("Режим обработки"), {
      target: { value: "in_place" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Зарегистрировать каталог" }),
    );

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Сервер отклонил каталог");
    expect(screen.getByLabelText("Путь на сервере")).toHaveValue(
      "/srv/missing",
    );
    expect(
      screen.getByRole("button", { name: "Зарегистрировать каталог" }),
    ).toBeVisible();
  });
});

describe("source root detail", () => {
  it("shows the server path, the inventory and the old path of a changed root", async () => {
    await renderDetail(staleRoot);

    expect(screen.getByText("/srv/new-home")).toBeVisible();
    expect(screen.getByText("12 файлов")).toBeVisible();
    expect(
      screen.getByText(
        "Это прежний путь: инвентарь заменится после успешного сканирования текущего пути.",
      ),
    ).toBeVisible();
    // The published listing names the old path as well: its own scope note
    // states which path the shown locations belong to.
    expect(
      await screen.findByText(
        "Инвентарь относится к прежнему пути /srv/old-home.",
      ),
    ).toBeVisible();
    expect(screen.getAllByText("/srv/old-home")).toHaveLength(2);
    await screen.findByRole("row", { name: /01 Открытие/ });
    expect(
      screen.getByRole("button", { name: "Изменить каталог" }),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Удалить каталог" }),
    ).toBeVisible();
  });

  it("reports a root that was never scanned", async () => {
    await renderDetail(neverScanned, []);

    expect(screen.getByText(/Инвентаря нет/)).toHaveTextContent(
      "Инвентаря нет: каталог ещё ни разу не сканировался успешно. Список файлов появится после успешного сканирования.",
    );
    expect(screen.getByText("Не выполнялось")).toBeVisible();
  });

  it("reports an empty successful scan", async () => {
    await renderDetail(scannedEmpty, []);

    expect(
      await screen.findByText(
        "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
      ),
    ).toBeVisible();
    expect(screen.getByText("0 файлов")).toBeVisible();
  });

  it("reports an unavailable root with its previous inventory and safe error", async () => {
    await renderDetail(unavailableRoot);

    expect(screen.getByText("Недоступен")).toBeVisible();
    expect(screen.getByText("source directory is not readable")).toBeVisible();
    expect(
      screen.getByText(
        "Каталог недоступен: показан инвентарь последнего успешного сканирования.",
      ),
    ).toBeVisible();
  });

  it("reports a disabled root that refuses new scans", async () => {
    await renderDetail(disabledRoot);

    expect(
      screen.getByText("Нет: новые сканирования запрещены."),
    ).toBeVisible();
  });

  it("reports a missing root and returns to the list", async () => {
    server.use(
      http.get("/api/sources/:sourceId", () =>
        HttpResponse.json({ detail: "source root not found" }, { status: 404 }),
      ),
      http.get("/api/sources", () => json([])),
    );
    window.location.hash = "/sources/gone";
    render(<SourcesScreen />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Каталог не найден");

    fireEvent.click(screen.getByRole("button", { name: "К списку каталогов" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/sources");
  });

  it("saves a changed path and explains that the inventory still belongs to the old one", async () => {
    const saved = root({
      id: "root-1",
      display_name: "Входящие 2",
      configured_path: "/srv/inbox2",
      processing_mode: "staged",
      inventory_path: "/srv/inbox",
      stale: true,
    });
    let patched: unknown;
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
      http.patch("/api/sources/:sourceId", async ({ request }) => {
        patched = await request.json();
        return HttpResponse.json(saved);
      }),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Изменить каталог" }),
    );
    fireEvent.change(screen.getByLabelText("Имя каталога"), {
      target: { value: "Входящие 2" },
    });
    fireEvent.change(screen.getByLabelText("Путь на сервере"), {
      target: { value: "/srv/inbox2" },
    });
    fireEvent.change(screen.getByLabelText("Режим обработки"), {
      target: { value: "staged" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить каталог" }));

    expect(
      await screen.findByText(
        "Каталог сохранён. Инвентарь относится к прежнему пути /srv/inbox и заменится после успешного сканирования нового пути.",
      ),
    ).toBeVisible();
    expect(patched).toEqual({
      display_name: "Входящие 2",
      configured_path: "/srv/inbox2",
      processing_mode: "staged",
    });
    expect(
      screen.getByRole("heading", { level: 1, name: "Входящие 2" }),
    ).toBeVisible();
  });

  it("hides the legacy enable control and preserves a stored disabled flag on edit", async () => {
    const saved = root({ id: "root-1", enabled: false });
    let patched: unknown;
    server.use(
      http.get("/api/sources/:sourceId", () =>
        HttpResponse.json(root({ enabled: false })),
      ),
      locationsHandler(),
      http.patch("/api/sources/:sourceId", async ({ request }) => {
        patched = await request.json();
        return HttpResponse.json(saved);
      }),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Изменить каталог" }),
    );
    expect(screen.queryByLabelText("Каталог включён")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить каталог" }));

    expect(
      await screen.findByText("Нет: новые сканирования запрещены."),
    ).toBeVisible();
    expect(patched).toEqual({
      display_name: "Входящие",
      configured_path: "/srv/inbox",
      processing_mode: "in_place",
    });
  });

  it("refuses an edit of a root that is being scanned", async () => {
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
      http.patch("/api/sources/:sourceId", () => conflict()),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Изменить каталог" }),
    );
    fireEvent.change(screen.getByLabelText("Имя каталога"), {
      target: { value: "Входящие" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить каталог" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("активном сканировании");
    expect(
      screen.getByRole("button", { name: "Сохранить каталог" }),
    ).toBeVisible();
  });
});

describe("source root deletion", () => {
  it("closes the dialog on cancel without a request and returns focus to its trigger", async () => {
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    const trigger = await screen.findByRole("button", {
      name: "Удалить каталог",
    });
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", {
      name: "Удалить инвентарь каталога «Входящие»?",
    });
    expect(within(dialog).getByText("/srv/inbox")).toBeVisible();
    expect(within(dialog).getByText(/12 файлов/)).toBeVisible();
    expect(
      within(dialog).getByText(
        "Исходные файлы в каталоге источника и медиатека в output останутся на месте: MeloTrove удаляет только свои записи.",
      ),
    ).toBeVisible();
    expect(
      within(dialog).getByRole("button", { name: "Отмена" }),
    ).toHaveFocus();

    fireEvent.click(within(dialog).getByRole("button", { name: "Отмена" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes the dialog on Escape and keeps the root", async () => {
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Удалить каталог" }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/sources/root-1");
  });

  it("confirms the deletion with the exact path and count, then reloads the list", async () => {
    let sources = [root(), neverScanned];
    const confirmed: unknown[] = [];
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
      http.get("/api/sources", () => json(sources)),
      http.delete("/api/sources/:sourceId", async ({ request, params }) => {
        expect(params.sourceId).toBe("root-1");
        confirmed.push(await request.json());
        sources = [neverScanned];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Удалить каталог" }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
    );

    const heading = await screen.findByRole("heading", {
      level: 1,
      name: "Источники",
    });
    await waitFor(() => expect(heading).toHaveFocus());
    expect(screen.getByText("Новый")).toBeVisible();
    expect(screen.queryByText("Входящие")).not.toBeInTheDocument();
    expect(confirmed).toEqual([
      { confirmed_path: "/srv/inbox", confirmed_location_count: 12 },
    ]);
    expect(window.location.hash).toBe("#/sources");
  });

  it("keeps the dialog open when the server refuses the deletion", async () => {
    server.use(
      http.get("/api/sources/:sourceId", () => HttpResponse.json(root())),
      locationsHandler(),
      http.delete("/api/sources/:sourceId", () => conflict()),
    );
    window.location.hash = "/sources/root-1";
    render(<SourcesScreen />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Удалить каталог" }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
    );

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("активном сканировании");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(window.location.hash).toBe("#/sources/root-1");
  });
});
