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
import { afterEach, describe, expect, it } from "vitest";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SourceLocations } from "./SourceLocations";

const locationsUrl = "/api/sources/:sourceId/locations";

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

function root(overrides: Partial<SourceRootResponse> = {}): SourceRootResponse {
  return {
    id: "root-1",
    display_name: "Входящие",
    configured_path: "/srv/inbox",
    enabled: true,
    status: "available",
    stale: false,
    scan_generation: 4,
    location_count: 1,
    inventory_path: "/srv/inbox",
    last_successful_scan_at: "2026-09-26T10:20:00Z",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-26T10:20:00Z",
    ...overrides,
  };
}

const modifiedLabel = (mtime: string) => new Date(mtime).toLocaleString();

afterEach(cleanup);

describe("published inventory listing", () => {
  it("lists path, size, modification time and a distinct label per probe result", async () => {
    const requests: URL[] = [];
    server.use(
      http.get(locationsUrl, ({ request }) => {
        requests.push(new URL(request.url));
        return HttpResponse.json({
          locations: [
            location(),
            location({
              id: "loc-2",
              relative_path: "Альбом/02 Разговор.mp3",
              size_bytes: 512,
              mtime: "2026-09-25T09:00:00Z",
              probe_status: "no_audio",
            }),
            location({
              id: "loc-3",
              relative_path: "Альбом/03 Битва.flac",
              size_bytes: 48 * 1024 * 1024,
              probe_status: "probe_error",
              safe_error: "ffprobe timed out after 120s",
            }),
          ],
        });
      }),
    );
    render(<SourceLocations sourceId="root-1" root={root()} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Загрузка списка файлов",
    );

    const audio = await screen.findByRole("row", { name: /01 Открытие/ });
    expect(
      within(audio).getByRole("link", { name: "Альбом/01 Открытие.flac" }),
    ).toHaveAttribute("href", "#/sources/root-1/locations/loc-1");
    expect(within(audio).getByText("Аудио")).toBeVisible();
    expect(within(audio).getByText("1,5 КБ")).toBeVisible();
    expect(
      within(audio).getByText(modifiedLabel("2026-09-26T10:20:00Z")),
    ).toBeVisible();

    const silent = screen.getByRole("row", { name: /02 Разговор/ });
    expect(within(silent).getByText("Нет аудиодорожки")).toBeVisible();
    expect(within(silent).getByText("512 Б")).toBeVisible();
    expect(within(silent).getByText(/Аудиодорожки нет/)).toHaveTextContent(
      "Аудиодорожки нет: файл не может участвовать в анализе.",
    );
    expect(within(silent).queryByText(/повторится/)).not.toBeInTheDocument();

    const broken = screen.getByRole("row", { name: /03 Битва/ });
    expect(within(broken).getByText("Ошибка проверки")).toBeVisible();
    expect(within(broken).getByText("48 МБ")).toBeVisible();
    expect(
      within(broken).getByText("ffprobe timed out after 120s"),
    ).toBeVisible();
    expect(
      within(broken).getByText(
        "Проверка повторится при следующем сканировании.",
      ),
    ).toBeVisible();

    expect(screen.getByText(/Пути указаны относительно/)).toHaveTextContent(
      "Показаны записи последнего успешного сканирования: файлы с поддерживаемым аудиорасширением. Пути указаны относительно /srv/inbox.",
    );
    expect(screen.getByText("Показано 3 файла.")).toBeVisible();
    expect(requests).toHaveLength(1);
    expect(requests[0].searchParams.get("limit")).toBe("50");
    expect(requests[0].searchParams.has("cursor")).toBe(false);
  });

  it("lists nothing and reads nothing for a root that was never scanned", async () => {
    const requests: URL[] = [];
    const fresh = root({
      id: "root-new",
      status: "unknown",
      scan_generation: 0,
      location_count: 0,
      inventory_path: undefined,
      last_successful_scan_at: undefined,
    });
    server.use(
      http.get(locationsUrl, ({ request }) => {
        requests.push(new URL(request.url));
        return HttpResponse.json({ locations: [location()] });
      }),
    );
    const { rerender } = render(
      <SourceLocations sourceId="root-new" root={fresh} />,
    );

    expect(screen.getByText(/Инвентаря нет/)).toHaveTextContent(
      "Инвентаря нет: каталог ещё ни разу не сканировался успешно. Список файлов появится после успешного сканирования.",
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(requests).toHaveLength(0);

    rerender(
      <SourceLocations
        sourceId="root-new"
        root={root({
          id: "root-new",
          scan_generation: 1,
          location_count: 1,
        })}
      />,
    );
    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(screen.queryByText(/Инвентаря нет/)).not.toBeInTheDocument();
    expect(requests).toHaveLength(1);
  });

  it("reports an empty successful scan as a state of its own", async () => {
    server.use(
      http.get(locationsUrl, () => HttpResponse.json({ locations: [] })),
    );
    render(
      <SourceLocations
        sourceId="root-1"
        root={root({ scan_generation: 6, location_count: 0 })}
      />,
    );

    expect(
      await screen.findByText(/Последнее успешное сканирование не нашло/),
    ).toHaveTextContent(
      "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Показать ещё" }),
    ).not.toBeInTheDocument();
  });

  it("treats a null location list as an empty inventory", async () => {
    server.use(
      http.get(locationsUrl, () => HttpResponse.json({ locations: null })),
    );
    render(
      <SourceLocations
        sourceId="root-1"
        root={root({ scan_generation: 6, location_count: 0 })}
      />,
    );

    expect(
      await screen.findByText(/Последнее успешное сканирование не нашло/),
    ).toBeVisible();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("keeps showing the previous inventory of a moved root and names its old path", async () => {
    const moved = root({
      configured_path: "/srv/new-home",
      inventory_path: "/srv/old-home",
      stale: true,
    });
    server.use(
      http.get(locationsUrl, () =>
        HttpResponse.json({ locations: [location()] }),
      ),
    );
    render(<SourceLocations sourceId="root-stale" root={moved} />);

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(
      screen.getByText("Инвентарь относится к прежнему пути /srv/old-home."),
    ).toBeVisible();
    expect(screen.getByText("/srv/old-home").closest("p")).toHaveTextContent(
      "Пути указаны относительно /srv/old-home.",
    );
  });

  it("keeps showing the previous inventory of an unavailable root", async () => {
    const offline = root({
      status: "unavailable",
      safe_error: "source directory is not readable",
    });
    server.use(
      http.get(locationsUrl, () =>
        HttpResponse.json({ locations: [location()] }),
      ),
    );
    render(<SourceLocations sourceId="root-offline" root={offline} />);

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(
      screen.getByText(
        "Каталог недоступен: показан инвентарь последнего успешного сканирования.",
      ),
    ).toBeVisible();
  });

  it("appends the next server page from its cursor and stops at the last page", async () => {
    const requests: URL[] = [];
    server.use(
      http.get(locationsUrl, ({ request }) => {
        const url = new URL(request.url);
        requests.push(url);
        if (url.searchParams.get("cursor") === null) {
          return HttpResponse.json({
            locations: [
              location(),
              location({
                id: "loc-2",
                relative_path: "Альбом/02 Разговор.mp3",
              }),
            ],
            next_cursor: "page-2",
          });
        }
        return HttpResponse.json({
          locations: [
            location({ id: "loc-3", relative_path: "Альбом/03.m4a" }),
          ],
        });
      }),
    );
    render(<SourceLocations sourceId="root-1" root={root()} />);

    expect(
      await screen.findByRole("row", { name: /02 Разговор/ }),
    ).toBeVisible();
    expect(
      screen.queryByRole("row", { name: /03\.m4a/ }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Показано 2 файла.")).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Показать ещё" }));

    expect(await screen.findByRole("row", { name: /03\.m4a/ })).toBeVisible();
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(screen.getByText("Показано 3 файла.")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Показать ещё" }),
    ).not.toBeInTheDocument();
    expect(requests).toHaveLength(2);
    expect(requests[0].searchParams.get("limit")).toBe("50");
    expect(requests[0].searchParams.has("cursor")).toBe(false);
    expect(requests[1].searchParams.get("cursor")).toBe("page-2");
    expect(requests[1].searchParams.get("limit")).toBe("50");
  });

  it("reports a failed first page on the alert and loads it again on retry", async () => {
    const requests: URL[] = [];
    let failing = true;
    server.use(
      http.get(locationsUrl, ({ request }) => {
        requests.push(new URL(request.url));
        return failing
          ? HttpResponse.json(
              { detail: "database unavailable" },
              { status: 503 },
            )
          : HttpResponse.json({ locations: [location()] });
      }),
    );
    render(<SourceLocations sourceId="root-1" root={root()} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("database unavailable");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(screen.queryByRole("table")).not.toBeInTheDocument();

    failing = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(requests).toHaveLength(2);
    expect(requests[1].searchParams.has("cursor")).toBe(false);
  });

  it("keeps the loaded rows when a later page fails and retries it from the same cursor", async () => {
    const cursors: (string | null)[] = [];
    let failing = true;
    server.use(
      http.get(locationsUrl, ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        cursors.push(cursor);
        if (cursor === null) {
          return HttpResponse.json({
            locations: [location()],
            next_cursor: "page-2",
          });
        }
        return failing
          ? HttpResponse.json({ detail: "list timed out" }, { status: 504 })
          : HttpResponse.json({
              locations: [
                location({
                  id: "loc-2",
                  relative_path: "Альбом/02 Ответ.flac",
                }),
              ],
            });
      }),
    );
    render(<SourceLocations sourceId="root-1" root={root()} />);

    await screen.findByRole("row", { name: /01 Открытие/ });
    fireEvent.click(screen.getByRole("button", { name: "Показать ещё" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("list timed out");
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(screen.getByText("Показано 1 файл.")).toBeVisible();

    failing = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));

    expect(await screen.findByRole("row", { name: /02 Ответ/ })).toBeVisible();
    expect(screen.getByRole("row", { name: /01 Открытие/ })).toBeVisible();
    expect(screen.getByText("Показано 2 файла.")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(cursors).toEqual([null, "page-2", "page-2"]);
  });

  it("drops the loaded pages and the cursor when the parent shows another source", async () => {
    const requested: string[] = [];
    server.use(
      http.get(locationsUrl, ({ request, params }) => {
        requested.push(new URL(request.url).pathname);
        if (params.sourceId === "root-1") {
          return new URL(request.url).searchParams.get("cursor") === null
            ? HttpResponse.json({
                locations: [location()],
                next_cursor: "page-2",
              })
            : HttpResponse.json({
                locations: [
                  location({
                    id: "loc-2",
                    relative_path: "Альбом/02 Ответ.flac",
                  }),
                ],
              });
        }
        return HttpResponse.json({
          locations: [
            location({ id: "loc-9", relative_path: "Другой/01 Начало.flac" }),
          ],
        });
      }),
    );
    const { rerender } = render(
      <SourceLocations sourceId="root-1" root={root()} />,
    );

    await screen.findByRole("row", { name: /01 Открытие/ });
    fireEvent.click(screen.getByRole("button", { name: "Показать ещё" }));
    await screen.findByRole("row", { name: /02 Ответ/ });

    rerender(
      <SourceLocations sourceId="root-2" root={root({ id: "root-2" })} />,
    );

    expect(
      await screen.findByRole("row", { name: /Другой\/01 Начало/ }),
    ).toBeVisible();
    expect(
      screen.queryByRole("row", { name: /Ответ/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("row", { name: /01 Открытие/ }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Показано 1 файл.")).toBeVisible();
    expect(requested).toEqual([
      "/api/sources/root-1/locations",
      "/api/sources/root-1/locations",
      "/api/sources/root-2/locations",
    ]);
  });

  it("re-reads the inventory from the first page when a scan advances the generation", async () => {
    const requests: URL[] = [];
    let published = [location()];
    server.use(
      http.get(locationsUrl, ({ request }) => {
        requests.push(new URL(request.url));
        return HttpResponse.json({ locations: published });
      }),
    );
    const { rerender } = render(
      <SourceLocations sourceId="root-1" root={root({ scan_generation: 4 })} />,
    );

    await screen.findByRole("row", { name: /01 Открытие/ });
    expect(
      screen.queryByRole("row", { name: /02 Новый/ }),
    ).not.toBeInTheDocument();

    published = [
      location(),
      location({ id: "loc-2", relative_path: "Альбом/02 Новый.flac" }),
    ];
    rerender(
      <SourceLocations
        sourceId="root-1"
        root={root({ scan_generation: 5, location_count: 2 })}
      />,
    );

    expect(await screen.findByRole("row", { name: /02 Новый/ })).toBeVisible();
    expect(screen.getByText("Показано 2 файла.")).toBeVisible();
    expect(requests).toHaveLength(2);
    expect(requests[1].searchParams.has("cursor")).toBe(false);
  });

  it("re-reads the same inventory when the parent bumps its refresh token", async () => {
    let calls = 0;
    server.use(
      http.get(locationsUrl, () => {
        calls += 1;
        return HttpResponse.json({
          locations:
            calls === 1
              ? [
                  location({
                    id: "loc-9",
                    relative_path: "Альбом/09 Новый.flac",
                  }),
                ]
              : [location()],
        });
      }),
    );
    const { rerender } = render(
      <SourceLocations sourceId="root-1" root={root()} refreshToken={0} />,
    );

    expect(await screen.findByRole("row", { name: /09 Новый/ })).toBeVisible();

    rerender(
      <SourceLocations sourceId="root-1" root={root()} refreshToken={1} />,
    );

    expect(
      await screen.findByRole("row", { name: /01 Открытие/ }),
    ).toBeVisible();
    expect(
      screen.queryByRole("row", { name: /09 Новый/ }),
    ).not.toBeInTheDocument();
    expect(calls).toBe(2);
  });
});
