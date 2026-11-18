import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { subscribeToOperation } from "../../api/client/operations";
import { getGetIncomingGroupManualPickerSearchQueryKey } from "../../api/generated/client";
import type { ManualPickerSearchBody } from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ManualSearchPicker } from "./ManualSearchPicker";

const { unsubscribe } = vi.hoisted(() => ({ unsubscribe: vi.fn() }));

vi.mock("../../api/client/operations", () => ({
  subscribeToOperation: vi.fn(() => unsubscribe),
}));

const freeformQuery = "anacondaz выходи за меня";

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

it("sends the freeform release query unchanged and keeps pagination, results, and locale state", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);

  let starts = 0;
  let operationReads = 0;
  let resultReads = 0;
  const startBodies: ManualPickerSearchBody[] = [];
  const resultOperations: string[] = [];
  const onSelectRelease = vi.fn();
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
                  releases: firstPage
                    ? [
                        {
                          id: "11111111-1111-4111-8111-111111111111",
                          title: "Выходи за меня",
                          disambiguation: "deluxe edition",
                          "artist-credit": [{ name: "Anacondaz" }],
                          date: "2019-05-17",
                          country: "RU",
                          "track-count": 12,
                          media: [{ format: "CD", "track-count": 12 }],
                          "label-info": [
                            {
                              label: { name: "Warner Music Russia" },
                              "catalog-number": "5054197-1",
                            },
                          ],
                          score: 100,
                        },
                        {
                          id: "22222222-2222-4222-8222-222222222222",
                          title: "Выходи за меня",
                          "artist-credit": [{ name: "Anacondaz" }],
                          date: "2020-03-06",
                          media: [{ format: "Vinyl" }, { format: "CD" }],
                          score: 98,
                        },
                      ]
                    : [
                        {
                          id: "33333333-3333-4333-8333-333333333333",
                          title: "Выходи за меня (live)",
                          "artist-credit": [{ name: "Anacondaz" }],
                          date: "2021-11-12",
                          media: [{ format: "CD" }],
                          score: 87,
                        },
                        {
                          id: "44444444-4444-4444-8444-444444444444",
                          title: "Паганини",
                          "artist-credit": [{ name: "Anacondaz" }],
                          date: "2011-04-01",
                          media: [{ format: "CD" }],
                          score: 71,
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
          onSelectRelease={onSelectRelease}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );

  expect(screen.queryByRole("combobox")).toBeNull();
  fireEvent.change(
    screen.getByRole("textbox", { name: "Artist and release title" }),
    { target: { value: freeformQuery } },
  );
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "Number of results" }),
    { target: { value: "2" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await screen.findByText("Results 1–2 of 6");

  expect(screen.getAllByText("Выходи за меня")).toHaveLength(2);
  expect(screen.getByText("(deluxe edition)")).toBeInTheDocument();
  const cdDetails = screen.getByText(/Released: 2019-05-17/);
  expect(cdDetails).toHaveTextContent("Country: RU");
  expect(cdDetails).toHaveTextContent("Tracks: 12");
  expect(cdDetails).toHaveTextContent("Format: CD");
  expect(cdDetails).toHaveTextContent(
    "Labels: Warner Music Russia (5054197-1)",
  );
  expect(cdDetails).toHaveTextContent("MusicBrainz score: 100");
  expect(screen.getByText(/Format: Vinyl \/ CD · 2 discs/)).toHaveTextContent(
    "MusicBrainz score: 98",
  );
  expect(
    screen.getByRole("button", {
      name: /Choose “Выходи за меня” \(Anacondaz,.*Format: CD/,
    }),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", {
      name: /Choose “Выходи за меня” \(Anacondaz,.*Format: Vinyl \/ CD/,
    }),
  );
  expect(onSelectRelease).toHaveBeenCalledWith(
    "22222222-2222-4222-8222-222222222222",
  );

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
    expect(vi.mocked(subscribeToOperation)).toHaveBeenCalledTimes(2);
    expect(unsubscribe).toHaveBeenCalledTimes(2);
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
  expect(
    screen.getByRole("textbox", { name: "Артист и название издания" }),
  ).toHaveValue(freeformQuery);
  expect(
    screen.getByRole("spinbutton", { name: "Количество результатов" }),
  ).toHaveValue(2);
  expect(await screen.findByText("Результаты 3–4 из 6")).toBeInTheDocument();
  expect(screen.getByText("Выходи за меня (live)")).toBeInTheDocument();
  expect(
    screen.getByRole("button", {
      name: /Выбрать «Выходи за меня \(live\)» \(Anacondaz,.*2021-11-12/,
    }),
  ).toBeInTheDocument();
  await waitFor(() => {
    expect(queryClient.isFetching()).toBe(0);
  });
  expect(vi.mocked(subscribeToOperation)).toHaveBeenCalledTimes(2);
  expect(unsubscribe).toHaveBeenCalledTimes(2);
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
    expect(vi.mocked(subscribeToOperation)).toHaveBeenCalledTimes(3);
    expect(unsubscribe).toHaveBeenCalledTimes(3);
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
  expect(
    screen.getByRole("textbox", { name: "Artist and release title" }),
  ).toHaveValue(freeformQuery);
  expect(
    screen.getByRole("spinbutton", { name: "Number of results" }),
  ).toHaveValue(2);
  expect(screen.getByRole("alert").textContent).toBe(
    "MusicBrainz returned invalid search results.",
  );
  await waitFor(() => {
    expect(queryClient.isFetching()).toBe(0);
  });
  expect(vi.mocked(subscribeToOperation)).toHaveBeenCalledTimes(3);
  expect(unsubscribe).toHaveBeenCalledTimes(3);
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
      query: freeformQuery,
      expected_group_revision: "revision-1",
      limit: 2,
    },
    {
      entity: "release",
      query: freeformQuery,
      expected_group_revision: "revision-1",
      limit: 2,
    },
    {
      entity: "release",
      query: freeformQuery,
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

  fireEvent.change(
    screen.getByRole("textbox", { name: "Artist and release title" }),
    { target: { value: freeformQuery } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Search was not accepted (HTTP 503).",
  );

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(
    await screen.findByRole("heading", { name: "Поиск MusicBrainz" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("textbox", { name: "Артист и название издания" }),
  ).toHaveValue(freeformQuery);
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Поиск не принят (HTTP 503).",
  );
  expect(starts).toBe(1);

  queryClient.clear();
});
