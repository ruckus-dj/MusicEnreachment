import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { getGetIncomingGroupManualPickerSearchQueryKey } from "../../api/generated/client";
import type { ManualPickerSearchBody } from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ManualSearchPicker } from "./ManualSearchPicker";

vi.mock("../../api/client/operations", () => ({
  subscribeToOperation: vi.fn(() => vi.fn()),
}));

function LocaleToggle() {
  const { locale, setPreference } = useI18n();
  return (
    <button
      type="button"
      onClick={() => setPreference(locale === "ru" ? "en" : "ru")}
    >
      Switch locale
    </button>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

it("localizes picker validation without resetting search state or refetching", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);

  let starts = 0;
  let operationReads = 0;
  let resultReads = 0;
  const startBodies: ManualPickerSearchBody[] = [];
  const resultOperations: string[] = [];
  server.use(
    http.post(
      "/api/incoming-groups/group-1/manual-search",
      async ({ request }) => {
        starts += 1;
        startBodies.push((await request.json()) as ManualPickerSearchBody);
        return HttpResponse.json(
          { operation_id: `operation-${starts}` },
          { status: 202 },
        );
      },
    ),
    http.get("/api/operations/:operationId", ({ params }) => {
      operationReads += 1;
      return HttpResponse.json({
        id: params.operationId,
        kind: "provider_fetch",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-11-14T00:00:00Z",
        updated_at: "2026-11-14T00:00:01Z",
      });
    }),
    http.get(
      "/api/incoming-groups/group-1/manual-search/:operationId",
      ({ params }) => {
        resultReads += 1;
        resultOperations.push(String(params.operationId));
        const firstPage = params.operationId === "operation-1";
        return HttpResponse.json({
          payload:
            params.operationId === "operation-3"
              ? {}
              : {
                  count: 6,
                  offset: firstPage ? 0 : 2,
                  releases: [
                    {
                      id: firstPage
                        ? "11111111-1111-4111-8111-111111111111"
                        : "33333333-3333-4333-8333-333333333333",
                      title: firstPage ? "Blue album one" : "Blue album three",
                    },
                    {
                      id: firstPage
                        ? "22222222-2222-4222-8222-222222222222"
                        : "44444444-4444-4444-8444-444444444444",
                      title: firstPage ? "Blue album two" : "Blue album four",
                    },
                  ],
                },
        });
      },
    ),
  );

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider>
      <QueryClientProvider client={queryClient}>
        <LocaleToggle />
        <ManualSearchPicker
          groupId="group-1"
          groupRevision="revision-1"
          onSelectRelease={vi.fn()}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );

  fireEvent.change(screen.getByRole("combobox", { name: "Search type" }), {
    target: { value: "release" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Search query" }), {
    target: { value: "Blue" },
  });
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "Number of results" }),
    { target: { value: "2" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await screen.findByText("Results 1–2 of 6");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByText("Results 3–4 of 6");
  await waitFor(() => {
    const secondPageQuery = queryClient.getQueryState([
      ...getGetIncomingGroupManualPickerSearchQueryKey(
        "group-1",
        "operation-2",
      ),
      startBodies[1].request_id,
    ]);
    expect(secondPageQuery?.status).toBe("success");
    expect(secondPageQuery?.fetchStatus).toBe("idle");
    expect(starts).toBe(2);
    expect(operationReads).toBe(2);
    expect(queryClient.isFetching()).toBe(0);
  });
  const secondPageBaseline = {
    starts,
    operationReads,
    resultReads,
    startBodies: startBodies.map((body) => ({ ...body })),
    resultOperations: [...resultOperations],
  };

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));

  expect(
    await screen.findByRole("heading", { name: "Поиск MusicBrainz" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Тип поиска" })).toHaveValue(
    "release",
  );
  expect(screen.getByRole("textbox", { name: "Поисковый запрос" })).toHaveValue(
    "Blue",
  );
  expect(
    screen.getByRole("spinbutton", { name: "Количество результатов" }),
  ).toHaveValue(2);
  expect(await screen.findByText("Результаты 3–4 из 6")).toBeInTheDocument();
  expect(screen.getByText("Blue album three")).toBeInTheDocument();
  await waitFor(() => {
    expect(queryClient.isFetching()).toBe(0);
  });
  expect({
    starts,
    operationReads,
    resultReads,
    startBodies,
    resultOperations,
  }).toEqual(secondPageBaseline);

  fireEvent.click(screen.getByRole("button", { name: "Следующая" }));
  await waitFor(() => {
    const requestId =
      startBodies.length > 2 ? startBodies[2].request_id : undefined;
    const thirdPageQuery = requestId
      ? queryClient.getQueryState([
          ...getGetIncomingGroupManualPickerSearchQueryKey(
            "group-1",
            "operation-3",
          ),
          requestId,
        ])
      : undefined;
    expect(thirdPageQuery?.status).toBe("error");
    expect(thirdPageQuery?.fetchStatus).toBe("idle");
    expect(starts).toBe(3);
    expect(operationReads).toBe(3);
    expect(queryClient.isFetching()).toBe(0);
  });
  const thirdPageBaseline = {
    starts,
    operationReads,
    resultReads,
    startBodies: startBodies.map((body) => ({ ...body })),
    resultOperations: [...resultOperations],
  };
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "MusicBrainz вернул некорректные результаты поиска.",
  );

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));

  expect(
    await screen.findByRole("heading", { name: "Search MusicBrainz" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Search type" })).toHaveValue(
    "release",
  );
  expect(screen.getByRole("textbox", { name: "Search query" })).toHaveValue(
    "Blue",
  );
  expect(
    screen.getByRole("spinbutton", { name: "Number of results" }),
  ).toHaveValue(2);
  expect(screen.getByRole("alert").textContent).toBe(
    "MusicBrainz returned invalid search results.",
  );
  await waitFor(() => {
    expect(queryClient.isFetching()).toBe(0);
  });
  expect({
    starts,
    operationReads,
    resultReads,
    startBodies,
    resultOperations,
  }).toEqual(thirdPageBaseline);
  expect(startBodies.map(({ offset }) => offset)).toEqual([0, 2, 4]);
  expect(
    startBodies.map(({ entity, query, expected_group_revision, limit }) => ({
      entity,
      query,
      expected_group_revision,
      limit,
    })),
  ).toEqual([
    {
      entity: "release",
      query: "Blue",
      expected_group_revision: "revision-1",
      limit: 2,
    },
    {
      entity: "release",
      query: "Blue",
      expected_group_revision: "revision-1",
      limit: 2,
    },
    {
      entity: "release",
      query: "Blue",
      expected_group_revision: "revision-1",
      limit: 2,
    },
  ]);
  expect(new Set(startBodies.map(({ request_id }) => request_id)).size).toBe(3);
  expect(new Set(resultOperations)).toEqual(
    new Set(["operation-1", "operation-2", "operation-3"]),
  );

  queryClient.clear();
});

it("shows a localized fallback for a detail-less rejection without extra requests", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);

  let starts = 0;
  server.use(
    http.post("/api/incoming-groups/group-1/manual-search", () => {
      starts += 1;
      return HttpResponse.json(
        { title: "Service Unavailable" },
        { status: 503 },
      );
    }),
  );

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider>
      <QueryClientProvider client={queryClient}>
        <LocaleToggle />
        <ManualSearchPicker
          groupId="group-1"
          groupRevision="revision-1"
          onSelectRelease={vi.fn()}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );

  fireEvent.change(screen.getByRole("textbox", { name: "Search query" }), {
    target: { value: "Blue" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Search was not accepted (HTTP 503).",
  );

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(
    await screen.findByRole("heading", { name: "Поиск MusicBrainz" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Поиск не принят (HTTP 503).",
  );
  expect(starts).toBe(1);

  queryClient.clear();
});
