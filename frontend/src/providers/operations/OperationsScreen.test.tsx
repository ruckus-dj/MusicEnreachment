import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider, useI18n } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { OperationsScreen } from "./OperationsScreen";

const releaseId = "123e4567-e89b-42d3-a456-426614174000";
const positionId = "223e4567-e89b-42d3-a456-426614174000";

function deferred() {
  let resolve = () => {};
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function LocaleSwitcher() {
  const { locale, setPreference } = useI18n();
  return (
    <button
      type="button"
      onClick={() => setPreference(locale === "en" ? "ru" : "en")}
    >
      Switch language
    </button>
  );
}

function renderScreen(
  withLocaleSwitcher = false,
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  }),
) {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        {withLocaleSwitcher && <LocaleSwitcher />}
        <OperationsScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  window.location.hash = "";
  vi.unstubAllGlobals();
});

it("shows loading then current operations with target links, diagnostics and failed retry", async () => {
  let retried = false;
  server.use(
    http.get("/api/operations/current", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("status")).toBe("attention");
      expect(url.searchParams.get("offset")).toBe("0");
      expect(url.searchParams.get("kind")).toBeNull();
      expect(url.searchParams.get("q")).toBeNull();
      if (retried) {
        return HttpResponse.json(
          { detail: "List temporarily unavailable" },
          { status: 503 },
        );
      }
      return HttpResponse.json({
        operations: [
          {
            id: "failed-id",
            kind: "scan_source",
            state: "failed",
            stage: "scan_files",
            bytes_completed: 12,
            bytes_total: 30,
            target_identity: "/music",
            target_label: "Music library",
            target_ref: { kind: "source_root", root_id: "root-1" },
            safe_error: "Permission denied",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
          {
            id: "other-failure",
            kind: "scan_source",
            state: "failed",
            stage: "scan_files",
            bytes_completed: 0,
            target_label: "Archive",
            safe_error: "Disk not available",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
        ],
        total: 2,
      });
    }),
    http.post("/api/operations/failed-id/retry", () => {
      retried = true;
      return HttpResponse.json({
        id: "failed-id",
        kind: "scan_source",
        state: "queued",
        stage: "queued",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:02:00Z",
      });
    }),
  );

  renderScreen();
  expect(screen.getByRole("status")).toHaveTextContent("Loading");
  const target = await screen.findByRole("link", { name: "Music library" });
  const row = screen.getByRole("row", { name: /Music library/ });
  expect(target).toHaveAttribute("href", "#/sources/root-1");
  expect(within(row).getByText("Source scan")).toBeInTheDocument();
  expect(screen.queryByText("Permission denied")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("columnheader", { name: /progress|error/i }),
  ).not.toBeInTheDocument();

  fireEvent.click(within(row).getByRole("button", { name: "Details" }));
  expect(screen.getByRole("dialog")).toHaveTextContent("Permission denied");
  expect(
    within(screen.getByRole("dialog")).queryByText("Progress"),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));

  fireEvent.click(within(row).getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "List temporarily unavailable",
  );
  expect(
    screen.queryByRole("link", { name: "Music library" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("Archive")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Retry" }),
  ).not.toBeInTheDocument();
});

it("delegates the sort across pages and keeps the server order", async () => {
  window.location.hash = "#/operations?offset=50";
  const sortedReads: URL[] = [];
  const created = "2026-01-01T00:00:00Z";
  const operation = {
    kind: "scan_source",
    state: "running",
    stage: "traversing",
    bytes_completed: 0,
    created_at: created,
    updated_at: created,
  };
  server.use(
    http.get("/api/operations/current", ({ request }) => {
      const url = new URL(request.url);
      if (url.searchParams.get("sort") === "target") {
        sortedReads.push(url);
        return HttpResponse.json({
          operations: [
            { ...operation, id: "test-zebra", target_label: "Zebra" },
            { ...operation, id: "test-archive", target_label: "Archive" },
          ],
          total: 2,
        });
      }
      return HttpResponse.json({
        operations: [
          { ...operation, id: "test-start", target_label: "Second page" },
        ],
        total: 51,
      });
    }),
  );
  renderScreen();
  await screen.findByText("Second page");
  fireEvent.click(screen.getByRole("columnheader", { name: "Target" }));
  const zebra = await screen.findByText("Zebra");
  expect(
    within(screen.getByRole("grid")).getAllByRole("row")[1],
  ).toContainElement(zebra);
  expect(sortedReads[0]?.searchParams.get("direction")).toBe("asc");
  expect(sortedReads[0]?.searchParams.get("offset")).toBe("0");
  expect(sortedReads[0]?.searchParams.get("locale")).toBe("en");
  expect(screen.getByRole("columnheader", { name: "Target" })).toHaveAttribute(
    "aria-sort",
    "ascending",
  );
  expect(
    new URLSearchParams(window.location.hash.split("?")[1]).get("sort"),
  ).toBe("target");
});

it("shows a measured copy percentage only in operation details", async () => {
  server.use(
    http.get("/api/operations/current", () =>
      HttpResponse.json({
        operations: [
          {
            id: "moving-tools",
            kind: "move_tools_root",
            state: "running",
            stage: "copy",
            bytes_completed: 20,
            bytes_total: 50,
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
        ],
        total: 1,
      }),
    ),
  );
  renderScreen();
  const details = await screen.findByRole("button", { name: "Details" });
  expect(screen.queryByText(/40\s*%/)).not.toBeInTheDocument();
  fireEvent.click(details);
  expect(screen.getByRole("dialog")).toHaveTextContent(/40\s*%/);
});

it("refreshes the active filter after a retry completes across navigation", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const retry = deferred();
  const firstRead = deferred();
  const staleRead = deferred();
  const operation = {
    id: "failed-id",
    kind: "scan_source",
    state: "failed",
    stage: "scan_files",
    bytes_completed: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:01:00Z",
  };
  let queuedReads = 0;
  server.use(
    http.get("/api/operations/current", async ({ request }) => {
      const status = new URL(request.url).searchParams.get("status");
      if (status !== "queued") {
        return HttpResponse.json({ operations: [operation], total: 1 });
      }
      if (++queuedReads === 1) {
        firstRead.resolve();
        await staleRead.promise;
        return HttpResponse.json({ operations: [], total: 0 });
      }
      return HttpResponse.json({
        operations: [
          { ...operation, state: "queued", target_label: "Retried source" },
        ],
        total: 1,
      });
    }),
    http.post("/api/operations/failed-id/retry", async () => {
      await retry.promise;
      return HttpResponse.json({ ...operation, state: "queued" });
    }),
  );

  const first = renderScreen(false, client);
  fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
  first.unmount();
  window.location.hash = "#/operations?status=queued";
  renderScreen(false, client);
  try {
    await firstRead.promise;
    retry.resolve();
    expect(await screen.findByText("Retried source")).toBeInTheDocument();
  } finally {
    retry.resolve();
    staleRead.resolve();
  }
});

it.each([
  "install",
  "move_tools_root",
])("dismisses a failed %s operation and restores heading focus", async (kind) => {
  let dismissed = false;
  server.use(
    http.get("/api/operations/current", () =>
      HttpResponse.json(
        dismissed
          ? { operations: [], total: 0 }
          : {
              operations: [
                {
                  id: "failed-tool",
                  kind,
                  state: "failed",
                  stage: "download",
                  bytes_completed: 0,
                  target_label: "FFmpeg 7.1",
                  safe_error: "Download failed",
                  created_at: "2026-01-01T00:00:00Z",
                  updated_at: "2026-01-01T00:00:00Z",
                },
              ],
              total: 1,
            },
      ),
    ),
    http.delete("/api/operations/failed-tool", () => {
      dismissed = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );

  renderScreen();
  const dismiss = await screen.findByRole("button", { name: "Dismiss" });
  dismiss.focus();
  fireEvent.click(dismiss);

  expect(
    await screen.findByRole("heading", { name: "No current operations" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Operation dismissed.")).toBeInTheDocument();
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { level: 1, name: "Current operations" }),
    ),
  );
});

it("keeps the current rows and language while a running operation is pending", async () => {
  let operationReads = 0;
  server.use(
    http.get("/api/operations/current", () => {
      operationReads += 1;
      return HttpResponse.json({
        operations: [
          {
            id: "pending-id",
            kind: "scan_source",
            state: "running",
            stage: "traversing",
            bytes_completed: 12,
            bytes_total: 30,
            target_identity: "/music",
            target_label: "Music library",
            target_ref: { kind: "source_root", root_id: "root-1" },
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
        ],
        total: 1,
      });
    }),
  );

  renderScreen(true);
  expect(await screen.findByText("Source scan")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Current operations" }),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Switch language" }));

  expect(
    screen.getByRole("heading", { name: "Текущие операции" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Сканирование источника")).toBeInTheDocument();
  expect(
    within(screen.getByRole("grid")).getByText("Выполняется"),
  ).toBeInTheDocument();
  expect(operationReads).toBe(1);
});

it("uses URL filters, clamps a page after the list shrinks and keeps position links", async () => {
  window.location.hash =
    "#/operations?status=failed&kind=publication&offset=100";
  let reads = 0;
  server.use(
    http.get("/api/operations/current", ({ request }) => {
      const url = new URL(request.url);
      reads += 1;
      expect(url.searchParams.get("status")).toBe("failed");
      expect(url.searchParams.get("kind")).toBe("publication");
      expect(url.searchParams.get("offset")).toBe(
        reads === 1 ? "100" : reads === 2 ? "50" : "0",
      );
      return HttpResponse.json({
        operations:
          reads === 1
            ? []
            : [
                {
                  id: "failed-publication",
                  kind: "publish_track_v1",
                  state: "failed",
                  stage: "write_tags",
                  bytes_completed: 0,
                  target_label: "Northern Lines · position 01",
                  target_ref: {
                    kind: "position",
                    release_id: releaseId,
                    position_id: positionId,
                  },
                  safe_error: "Write failed",
                  created_at: "2026-01-01T00:00:00Z",
                  updated_at: "2026-01-01T00:01:00Z",
                },
              ],
        total: 60,
      });
    }),
  );

  renderScreen();
  expect(
    await screen.findByRole("link", { name: "Northern Lines · position 01" }),
  ).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}/positions/${positionId}`,
  );
  expect(await screen.findByText("51–60 of 60")).toBeInTheDocument();
  await waitFor(() =>
    expect(window.location.hash).toBe(
      "#/operations?status=failed&kind=publication&offset=50",
    ),
  );

  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  await waitFor(() => expect(reads).toBe(3));
  expect(window.location.hash).toBe(
    "#/operations?status=failed&kind=publication",
  );
});
