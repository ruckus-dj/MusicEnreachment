import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { selectDirectoryPath } from "../../test/selectDirectoryPath";
import { server } from "../../test/server";
import { SourcesScreen } from "./SourcesScreen";

function LocaleFixture({ children }: { children: ReactNode }) {
  const { setPreference } = useI18n();
  return (
    <>
      <button type="button" onClick={() => setPreference("en")}>
        Switch to English
      </button>
      {children}
    </>
  );
}

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
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
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

it("autosaves each source choice without submitting the name draft", async () => {
  localStorage.setItem("melotrove-language", "en");
  window.location.hash = "/sources/root-1";
  const path = "/srv/new inbox ";
  const newName = "Renamed source";
  const writes: unknown[] = [];
  let current = root();
  let rejectPath = true;
  let scans = 0;
  server.use(
    http.get("/api/sources/root-1", () => HttpResponse.json(current)),
    locationsHandler([]),
    http.patch("/api/sources/root-1", async ({ request }) => {
      const patch = (await request.json()) as Partial<SourceRootResponse>;
      writes.push(patch);
      if (patch.configured_path && rejectPath) {
        rejectPath = false;
        return HttpResponse.json({}, { status: 409 });
      }
      current = { ...current, ...patch };
      current.stale = current.inventory_path !== current.configured_path;
      return HttpResponse.json(current);
    }),
    http.post("/api/sources/root-1/scan", () => {
      scans += 1;
      return HttpResponse.json({}, { status: 409 });
    }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <SourcesScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );

  const settings = within(
    await screen.findByRole("region", { name: "Source settings" }),
  );
  const name = settings.getByRole("textbox", { name: "Source name" });
  const save = () => settings.getByRole("button", { name: "Save" });
  const directory = settings.getByRole("textbox", { name: "Server directory" });
  expect(
    settings.queryByRole("button", { name: "Save" }),
  ).not.toBeInTheDocument();
  fireEvent.change(name, { target: { value: ` ${newName} ` } });
  fireEvent.blur(name);
  expect(save()).toBeEnabled();
  fireEvent.click(settings.getByRole("radio", { name: "Using a work copy" }));
  const overview = within(screen.getByRole("region", { name: "Source" }));
  expect(await overview.findByText("Using a work copy")).toBeInTheDocument();
  expect(writes).toEqual([{ processing_mode: "staged" }]);

  await selectDirectoryPath("Server directory", path);
  expect(await settings.findByRole("alert")).toHaveTextContent(
    "The source is busy",
  );
  expect(directory).toHaveValue("/srv/inbox");
  expect(name).toHaveValue(` ${newName} `);
  await selectDirectoryPath("Server directory", path);
  expect(
    await overview.findByText(/This is the previous path/),
  ).toBeInTheDocument();
  expect(directory).toHaveValue(path);
  expect(name).toHaveValue(` ${newName} `);

  fireEvent.click(save());
  expect(
    await screen.findByRole("heading", { level: 1, name: newName }),
  ).toBeInTheDocument();
  expect(
    settings.queryByRole("button", { name: "Save" }),
  ).not.toBeInTheDocument();
  expect(writes).toEqual([
    { processing_mode: "staged" },
    { configured_path: path },
    { configured_path: path },
    { display_name: newName },
  ]);
  expect(scans).toBe(0);
});

it("distinguishes an unchecked file from a failed audio check", async () => {
  const file = {
    size_bytes: 42,
    mtime: "2026-09-26T10:20:00Z",
    has_result: false,
  };
  server.use(
    http.get("/api/sources/root-1", () => HttpResponse.json(root())),
    locationsHandler([
      {
        ...file,
        id: "waiting",
        relative_path: "Album/waiting.mp3",
        probe_status: "not_analyzed",
      },
      {
        ...file,
        id: "failed",
        relative_path: "Album/failed.mp3",
        probe_status: "probe_error",
        safe_error: "Could not read audio.",
      },
    ]),
  );
  window.location.hash = "/sources/root-1";
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <SourcesScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );

  const waiting = await screen.findByRole("row", { name: /Album\/waiting/ });
  const failed = screen.getByRole("row", { name: /Album\/failed/ });
  expect(waiting).toHaveTextContent("Не проверено");
  expect(waiting).not.toHaveTextContent("Ошибка проверки");
  expect(failed).toHaveTextContent("Ошибка проверки");
  expect(failed).toHaveTextContent("Could not read audio.");
});

it("confirms a batch of source paths and shows per-copy cleanup results", async () => {
  vi.stubGlobal(
    "EventSource",
    class extends EventTarget {
      close() {}
    },
  );
  const sourcePath = "music/Альбом/01. Песня.mp3";
  const artifactId = "ecb4e7c4-1d4d-481f-9cc3-3295325db30d";
  const retryPath = "other/Album/02.flac";
  const retryId = "e9094fd2-eef2-4011-b820-3124b44f51ca";
  const submitted: unknown[] = [];
  const queued = {
    id: "copy-cleanup-op",
    kind: "cleanup_source_analysis_artifacts",
    state: "queued",
    stage: "queued",
    bytes_completed: 0,
    updated_at: "2026-11-23T17:00:00Z",
    created_at: "2026-11-23T17:00:00Z",
  };
  server.use(
    http.get("/api/sources", () => json([])),
    http.get("/api/source-analysis/artifacts/cleanup", () =>
      HttpResponse.json({
        count: 2,
        candidates: [
          {
            artifact_id: artifactId,
            source_relative_path: sourcePath,
            state: "cleanup_eligible",
          },
          {
            artifact_id: retryId,
            source_relative_path: retryPath,
            state: "cleanup_failed",
          },
        ],
      }),
    ),
    http.post("/api/source-analysis/artifacts/cleanup", async ({ request }) => {
      submitted.push(await request.json());
      return HttpResponse.json(queued, { status: 202 });
    }),
    http.get("/api/operations/copy-cleanup-op", () =>
      HttpResponse.json({
        ...queued,
        state: "failed",
        stage: "finished",
        updated_at: "2026-11-23T17:00:01Z",
        cleanup_results: [{ artifact_id: artifactId, state: "succeeded" }],
      }),
    ),
  );
  window.location.hash = "/sources";
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <SourcesScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );

  fireEvent.click(
    await screen.findByRole("checkbox", {
      name: `${sourcePath} — Ожидает очистки`,
    }),
  );
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: `${retryPath} — Предыдущая очистка не удалась`,
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Очистить выбранные (2)" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Очистить выбранные копии?",
  });
  expect(dialog).toHaveTextContent(sourcePath);
  expect(dialog).toHaveTextContent(retryPath);
  expect(dialog).not.toHaveTextContent(artifactId);
  expect(submitted).toHaveLength(0);
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Подтвердить очистку" }),
  );
  const outcomes = await screen.findByRole("list", {
    name: "Результаты очистки",
  });
  expect(within(outcomes).getByText(/Очищена/)).toHaveTextContent(artifactId);
  expect(
    within(outcomes).getByText(/Результат очистки не получен/),
  ).toHaveTextContent(retryId);
  expect(submitted).toEqual([{ artifact_ids: [artifactId, retryId] }]);
});

it("confirms the path and inventory count shown in the source removal dialog", async () => {
  const deletes: unknown[] = [];
  server.use(
    http.get("/api/sources/root-1", () =>
      HttpResponse.json(root({ location_count: 1 })),
    ),
    locationsHandler([
      {
        id: "first-location",
        relative_path: "01.flac",
        size_bytes: 42,
        mtime: "2026-09-26T10:20:00Z",
        has_result: false,
        probe_status: "not_analyzed",
      },
    ]),
    http.delete("/api/sources/root-1", async ({ request }) => {
      deletes.push(await request.json());
      return HttpResponse.json(
        { status: 409, detail: "source scope changed" },
        { status: 409 },
      );
    }),
  );
  window.location.hash = "/sources/root-1";
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <SourcesScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );

  const trigger = await screen.findByRole("button", {
    name: "Удалить источник Входящие",
  });
  trigger.focus();
  fireEvent.click(trigger);
  const dialog = await screen.findByRole("dialog", {
    name: "Подтверждение удаления источника",
  });
  expect(dialog).toHaveTextContent("/srv/inbox");
  expect(dialog).toHaveTextContent("Записей о файлах в инвентаре: 1.");
  expect(deletes).toHaveLength(0);
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
  );
  const error = await within(dialog).findByRole("alert");
  expect(error).toHaveFocus();
  expect(deletes).toEqual([
    { confirmed_path: "/srv/inbox", confirmed_location_count: 1 },
  ]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Отмена" }));
  await waitFor(() => expect(dialog).not.toBeInTheDocument());
  await waitFor(() => expect(trigger).toHaveFocus());
});

describe("source root creation", () => {
  it("registers a server path without scanning and reloads the list", async () => {
    const registered = root({
      id: "root-disk",
      display_name: "Диск",
      configured_path: "/srv/disk ",
      processing_mode: "staged",
      status: "unknown",
      scan_generation: 0,
      location_count: 0,
      inventory_path: undefined,
      last_successful_scan_at: undefined,
    });
    const created: SourceRootResponse[] = [];
    let sourceListRequests = 0;
    let scanRequests = 0;
    server.use(
      http.get("/api/sources", () => {
        sourceListRequests += 1;
        return json(created);
      }),
      http.get("/api/sources/root-disk", () => HttpResponse.json(registered)),
      locationsHandler([]),
      http.post("/api/sources/root-disk/scan", () => {
        scanRequests += 1;
        return new HttpResponse(null, { status: 500 });
      }),
      http.get("/api/source-analysis/artifacts/cleanup", () =>
        HttpResponse.json({ candidates: [], count: 0 }),
      ),
      http.post("/api/sources", async ({ request }) => {
        expect(await request.json()).toEqual({
          display_name: "Диск",
          configured_path: "/srv/disk ",
          processing_mode: "staged",
        });
        created.push(registered);
        return HttpResponse.json(registered);
      }),
    );
    window.location.hash = "/sources";
    render(
      <QueryClientProvider client={new QueryClient()}>
        <I18nProvider>
          <LocaleFixture>
            <SourcesScreen />
          </LocaleFixture>
        </I18nProvider>
      </QueryClientProvider>,
    );

    expect(
      await screen.findByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }));
    expect(screen.getByLabelText("Имя источника")).toHaveFocus();
    fireEvent.change(screen.getByLabelText("Имя источника"), {
      target: { value: "Диск" },
    });
    await selectDirectoryPath("Каталог на сервере", "/srv/disk ");
    const staged = screen.getByRole("radio", { name: "Через рабочую копию" });
    expect(staged).not.toBeChecked();
    expect(screen.getByRole("radio", { name: "На месте" })).not.toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Явно выберите режим обработки",
    );
    expect(created).toHaveLength(0);
    fireEvent.click(staged);
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Диск" }),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Сканировать каталог" }),
    ).toBeVisible();
    expect(scanRequests).toBe(0);
    expect(window.location.hash).toBe("#/sources/root-disk");
    fireEvent.click(screen.getByRole("button", { name: "К списку каталогов" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toHaveFocus();
    fireEvent.click(
      await screen.findByRole("button", { name: "Добавить источник" }),
    );
    expect(
      screen.getByRole("radio", { name: "Через рабочую копию" }),
    ).not.toBeChecked();
    fireEvent.change(screen.getByLabelText("Имя источника"), {
      target: { value: "Unsaved source draft" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Добавить источник" }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Выберите каталог на сервере.",
    );
    const requestsBeforeLocaleChange = sourceListRequests;
    fireEvent.click(screen.getByRole("button", { name: "Switch to English" }));
    expect(
      await screen.findByRole("button", { name: "Add source" }),
    ).toBeVisible();
    expect(screen.getByLabelText("Source name")).toHaveValue(
      "Unsaved source draft",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Choose a server directory.",
    );
    expect(sourceListRequests).toBe(requestsBeforeLocaleChange);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Add source" })).toHaveFocus();
  });
});

it("shares a pending row scan with the detail card without starting it twice", async () => {
  const source = root();
  const scan = {
    id: "scan-list",
    kind: "scan_source",
    state: "queued",
    stage: "queued",
    target_source_root_id: source.id,
    bytes_completed: 0,
    created_at: "2026-09-30T00:00:00Z",
    updated_at: "2026-09-30T00:00:00Z",
  };
  let releaseAdmission = () => {};
  const admission = new Promise<void>((resolve) => {
    releaseAdmission = resolve;
  });
  let scans = 0;
  let discoveries = 0;
  vi.stubGlobal(
    "EventSource",
    class extends EventTarget {
      close() {}
    },
  );
  server.use(
    http.get("/api/sources", () => json([source])),
    http.get("/api/source-analysis/artifacts/cleanup", () =>
      HttpResponse.json({ candidates: [], count: 0 }),
    ),
    http.get("/api/sources/root-1", () => HttpResponse.json(source)),
    locationsHandler([]),
    http.get("/api/operations", () => {
      discoveries += 1;
      return HttpResponse.json({ operations: [] });
    }),
    http.get("/api/operations/scan-list", () => HttpResponse.json(scan)),
    http.post("/api/sources/root-1/scan", async () => {
      scans += 1;
      await admission;
      return HttpResponse.json(scan);
    }),
  );
  window.location.hash = "/sources";
  render(
    <QueryClientProvider client={new QueryClient()}>
      <I18nProvider>
        <SourcesScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );

  const grid = within(
    await screen.findByRole("grid", { name: "Подключённые каталоги" }),
  );
  const start = await grid.findByRole("button", {
    name: "Сканировать: Входящие",
  });
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(start);
  expect(
    grid.getByRole("button", { name: "Сканируется…: Входящие" }),
  ).toBeDisabled();
  expect(window.location.hash).toBe("#/sources");
  act(() => {
    window.history.pushState(null, "", "/#/sources/root-1");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  const detailStart = await screen.findByRole("button", {
    name: "Сканировать каталог",
  });
  expect(detailStart).toBeDisabled();
  fireEvent.click(detailStart);
  releaseAdmission();
  expect(
    await screen.findByText("Сканирование поставлено в очередь."),
  ).toBeVisible();
  expect(scans).toBe(1);
  expect(discoveries).toBe(1);
});
