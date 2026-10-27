import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { useState } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { ManualReleasePicker } from "./ManualReleasePicker";
import { ManualSearchPicker } from "./ManualSearchPicker";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

class PickerEventSource extends EventTarget {
  static instances: PickerEventSource[] = [];
  close = vi.fn();
  constructor(readonly url: string) {
    super();
    PickerEventSource.instances.push(this);
  }
  wake() {
    this.dispatchEvent(new Event("operation-changed"));
  }
}
vi.stubGlobal("EventSource", PickerEventSource);

beforeEach(() => {
  PickerEventSource.instances = [];
});

it("browses releases for the explicitly selected artist and passes any chosen release to the direct picker", async () => {
  const searchBodies: Record<string, unknown>[] = [];
  let operation = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        if (body.artist_id && body.entity !== "artist")
          return HttpResponse.json({ detail: "wrong entity" }, { status: 422 });
        searchBodies.push(body);
        operation += 1;
        return HttpResponse.json(
          { operation_id: `search-${operation}` },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_artist_search",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get(
      "*/incoming-groups/:groupId/manual-search/:operationId",
      ({ params }) => {
        const payload =
          params.operationId === "search-1"
            ? {
                count: 2,
                offset: 0,
                artists: [
                  {
                    id: "11111111-1111-4111-8111-111111111111",
                    name: "First",
                    disambiguation: "first group",
                    score: 99,
                  },
                  {
                    id: "22222222-2222-4222-8222-222222222222",
                    name: "Second",
                    disambiguation: "second group",
                    score: 54,
                  },
                ],
              }
            : {
                "release-count": 1,
                "release-offset": 0,
                releases: [
                  {
                    id: "33333333-3333-4333-8333-333333333333",
                    title: "New release",
                    disambiguation: "2025",
                    score: 81,
                  },
                ],
              };
        return HttpResponse.json({
          group_id: "group-a",
          operation_id: params.operationId,
          kind:
            params.operationId === "search-1"
              ? "artist_search"
              : "artist_release_browse",
          payload,
          payload_sha256: "hash",
        });
      },
    ),
    http.post(
      "*/incoming-groups/:groupId/matching/manual-release",
      async ({ request }) => {
        expect(await request.json()).toMatchObject({
          selected_release: "33333333-3333-4333-8333-333333333333",
        });
        return HttpResponse.json(
          { operation_id: "prepare-1" },
          { status: 202 },
        );
      },
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onPrepared = vi.fn();
  function PickerIntegration() {
    const [selectedRelease, setSelectedRelease] = useState("");
    return (
      <QueryClientProvider client={client}>
        <ManualSearchPicker
          groupId="group-a"
          groupRevision="revision-a"
          onSelectRelease={setSelectedRelease}
        />
        <ManualReleasePicker
          groupId="group-a"
          groupRevision="revision-a"
          searchRelease={selectedRelease}
          onPrepared={onPrepared}
        />
      </QueryClientProvider>
    );
  }
  render(<PickerIntegration />);

  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "literal artist" },
  });
  fireEvent.change(screen.getByLabelText("Количество результатов"), {
    target: { value: "25" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() =>
    expect(
      screen.getAllByRole("button", { name: "Просмотреть издания артиста" }),
    ).toHaveLength(2),
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Просмотреть издания артиста" })[1],
  );

  await waitFor(() => expect(searchBodies).toHaveLength(2));
  expect(searchBodies[0]).toMatchObject({
    entity: "artist",
    query: "literal artist",
    offset: 0,
    limit: 25,
    expected_group_revision: "revision-a",
  });
  expect(searchBodies[1]).toMatchObject({
    entity: "artist",
    artist_id: "22222222-2222-4222-8222-222222222222",
    offset: 0,
    limit: 25,
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "Выбрать это издание" }),
  );
  await waitFor(() =>
    expect(
      screen.getByLabelText("MBID или официальная ссылка на издание"),
    ).toHaveValue("33333333-3333-4333-8333-333333333333"),
  );
  expect(searchBodies[1]).toMatchObject({
    artist_id: "22222222-2222-4222-8222-222222222222",
  });
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
});

it("defaults to an editable page size of ten and submits the chosen offset and limit", async () => {
  const bodies: Record<string, unknown>[] = [];
  let operation = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        operation += 1;
        return HttpResponse.json(
          { operation_id: `page-${operation}` },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_release_search",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get(
      "*/incoming-groups/:groupId/manual-search/:operationId",
      ({ params }) =>
        HttpResponse.json({
          group_id: "group-a",
          operation_id: params.operationId,
          kind: "release_search",
          payload: {
            count: 31,
            offset: operation === 1 ? 0 : 25,
            releases: [],
          },
          payload_sha256: "hash",
        }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ManualSearchPicker
        groupId="group-a"
        groupRevision="revision-a"
        onSelectRelease={() => {}}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("Тип поиска"), {
    target: { value: "release" },
  });
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "literal release" },
  });
  expect(screen.getByLabelText("Количество результатов")).toHaveValue(10);
  expect(screen.getByRole("button", { name: "Искать" })).toBeEnabled();
  fireEvent.change(screen.getByLabelText("Количество результатов"), {
    target: { value: "25" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  fireEvent.click(await screen.findByRole("button", { name: "Следующая" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies.map(({ offset, limit }) => ({ offset, limit }))).toEqual([
    { offset: 0, limit: 25 },
    { offset: 25, limit: 25 },
  ]);
});

it("keeps an admitted search tracked across a group revision change without showing stale results", async () => {
  const admission = deferred<Response>();
  let operationReads = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      () => admission.promise,
    ),
    http.get("/api/operations/search-revision", () => {
      operationReads += 1;
      return HttpResponse.json({
        id: "search-revision",
        kind: "provider_artist_search",
        state: "queued",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      });
    }),
    http.get("*/incoming-groups/group-a/manual-search/search-revision", () =>
      HttpResponse.json({
        group_id: "group-a",
        operation_id: "search-revision",
        kind: "artist_search",
        payload: {
          count: 1,
          offset: 0,
          artists: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              name: "Old revision result",
            },
          ],
        },
        payload_sha256: "hash",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const props = { groupId: "group-a", onSelectRelease: () => {} };
  const renderPicker = (groupRevision: string) => (
    <QueryClientProvider client={client}>
      <ManualSearchPicker {...props} groupRevision={groupRevision} />
    </QueryClientProvider>
  );
  const view = render(renderPicker("revision-old"));
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "artist query" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  view.rerender(renderPicker("revision-new"));
  admission.resolve(
    HttpResponse.json(
      { operation_id: "search-revision" },
      { status: 202 },
    ) as Response,
  );
  await waitFor(() =>
    expect(PickerEventSource.instances.length).toBeGreaterThan(0),
  );
  PickerEventSource.instances.at(-1)?.wake();
  await waitFor(() => expect(operationReads).toBeGreaterThan(0));
  await waitFor(() =>
    expect(screen.queryByText("Old revision result")).toBeNull(),
  );
  expect(screen.getByLabelText("Количество результатов")).toHaveValue(10);
});

it("locks edits while operation state is unknown and restores them on success", async () => {
  const bodies: Record<string, unknown>[] = [];
  let operationReads = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json(
          { operation_id: "slow-search" },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/slow-search", () => {
      operationReads += 1;
      return HttpResponse.json({
        id: "slow-search",
        kind: "provider_artist_search",
        state: operationReads === 1 ? "queued" : "succeeded",
        stage: operationReads === 1 ? "queued" : "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: `2026-01-01T00:00:0${operationReads}Z`,
      });
    }),
    http.get("*/incoming-groups/group-a/manual-search/slow-search", () =>
      HttpResponse.json({
        group_id: "group-a",
        operation_id: "slow-search",
        kind: "artist_search",
        payload: {
          count: 1,
          offset: 0,
          artists: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              name: "Frozen result",
            },
          ],
        },
        payload_sha256: "hash",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ManualSearchPicker
        groupId="group-a"
        groupRevision="revision-a"
        onSelectRelease={() => {}}
      />
    </QueryClientProvider>,
  );
  const query = screen.getByLabelText("Поисковый запрос");
  fireEvent.change(query, { target: { value: "initial" } });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(query).toBeDisabled();
  expect(screen.getByLabelText("Тип поиска")).toBeDisabled();
  expect(screen.getByLabelText("Количество результатов")).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  expect(bodies).toHaveLength(1);
  await waitFor(() => expect(operationReads).toBe(1));
  expect(PickerEventSource.instances.at(-1)?.url).toContain(
    "slow-search/events",
  );
  PickerEventSource.instances.at(-1)?.wake();
  expect(await screen.findByText("Frozen result")).toBeInTheDocument();
  expect(operationReads).toBe(2);
  await waitFor(() => expect(query).toBeEnabled());
  expect(screen.getByLabelText("Тип поиска")).toBeEnabled();
  expect(screen.getByLabelText("Количество результатов")).toBeEnabled();
});

it("stops following completed searches across groups and follows a new search", async () => {
  let operation = 0;
  server.use(
    http.post("*/incoming-groups/:groupId/manual-search", () => {
      operation += 1;
      return HttpResponse.json(
        { operation_id: `terminal-${operation}` },
        { status: 202 },
      );
    }),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_artist_search",
        state: params.operationId === "terminal-2" ? "failed" : "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get("*/incoming-groups/:groupId/manual-search/:operationId", () =>
      HttpResponse.json({
        group_id: "group-a",
        operation_id: "terminal",
        kind: "artist_search",
        payload: { count: 0, offset: 0, artists: [] },
        payload_sha256: "hash",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function GroupPicker() {
    const [groupId, setGroupId] = useState("group-a");
    return (
      <QueryClientProvider client={client}>
        <button type="button" onClick={() => setGroupId("group-a")}>
          Group A
        </button>
        <button type="button" onClick={() => setGroupId("group-b")}>
          Group B
        </button>
        <ManualSearchPicker
          groupId={groupId}
          groupRevision="revision-a"
          onSelectRelease={() => {}}
        />
      </QueryClientProvider>
    );
  }
  render(<GroupPicker />);

  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "first search" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(PickerEventSource.instances).toHaveLength(1));
  await waitFor(() =>
    expect(PickerEventSource.instances[0].close).toHaveBeenCalled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Group B" }));
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "second search" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(PickerEventSource.instances).toHaveLength(2));
  await waitFor(() =>
    expect(PickerEventSource.instances[1].close).toHaveBeenCalled(),
  );

  fireEvent.click(screen.getByRole("button", { name: "Group A" }));
  fireEvent.click(screen.getByRole("button", { name: "Group B" }));
  expect(PickerEventSource.instances).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(PickerEventSource.instances).toHaveLength(3));
});

it("creates a fresh request id and revision when resubmitting the same query", async () => {
  const bodies: Record<string, unknown>[] = [];
  let n = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        n += 1;
        return HttpResponse.json(
          { operation_id: `revision-${n}` },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_artist_search",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get(
      "*/incoming-groups/group-a/manual-search/:operationId",
      ({ params }) =>
        HttpResponse.json({
          group_id: "group-a",
          operation_id: params.operationId,
          kind: "artist_search",
          payload: { count: 0, offset: 0, artists: [] },
          payload_sha256: "hash",
        }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const props = { groupId: "group-a", onSelectRelease: () => {} };
  const view = render(
    <QueryClientProvider client={client}>
      <ManualSearchPicker {...props} groupRevision="revision-old" />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "same query" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() =>
    expect(screen.getByText("Совпадений нет.")).toBeInTheDocument(),
  );
  view.rerender(
    <QueryClientProvider client={client}>
      <ManualSearchPicker {...props} groupRevision="revision-new" />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[0]).toMatchObject({
    query: "same query",
    expected_group_revision: "revision-old",
    offset: 0,
    limit: 10,
  });
  expect(bodies[1]).toMatchObject({
    query: "same query",
    expected_group_revision: "revision-new",
    offset: 0,
    limit: 10,
  });
  expect(bodies[1].request_id).not.toBe(bodies[0].request_id);
});

it("clears explicit artist browsing when the query changes to a release search", async () => {
  const bodies: Record<string, unknown>[] = [];
  let n = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        n += 1;
        return HttpResponse.json(
          { operation_id: `browse-${n}` },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_artist_search",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get(
      "*/incoming-groups/group-a/manual-search/:operationId",
      ({ params }) =>
        HttpResponse.json({
          group_id: "group-a",
          operation_id: params.operationId,
          kind: "artist_search",
          payload:
            params.operationId === "browse-1"
              ? {
                  count: 1,
                  offset: 0,
                  artists: [
                    {
                      id: "22222222-2222-4222-8222-222222222222",
                      name: "Artist",
                    },
                  ],
                }
              : {
                  "release-count": 1,
                  "release-offset": 0,
                  releases: [
                    {
                      id: "33333333-3333-4333-8333-333333333333",
                      title: "Frozen release",
                    },
                  ],
                },
          payload_sha256: "hash",
        }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ManualSearchPicker
        groupId="group-a"
        groupRevision="revision-a"
        onSelectRelease={() => {}}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "artist" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Просмотреть издания артиста" }),
  );
  expect(await screen.findByText("Frozen release")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "new release query" },
  });
  fireEvent.change(screen.getByLabelText("Тип поиска"), {
    target: { value: "release" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(bodies).toHaveLength(3));
  expect(bodies[2]).toMatchObject({
    entity: "release",
    query: "new release query",
    offset: 0,
    limit: 10,
  });
  expect(bodies[2]).not.toHaveProperty("artist_id");
});

it("uses the edited page size in a fresh request after a completed search", async () => {
  const bodies: Record<string, unknown>[] = [];
  let n = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/manual-search",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        n += 1;
        return HttpResponse.json(
          { operation_id: `size-${n}` },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/:operationId", ({ params }) =>
      HttpResponse.json({
        id: params.operationId,
        kind: "provider_artist_search",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.get(
      "*/incoming-groups/group-a/manual-search/:operationId",
      ({ params }) =>
        HttpResponse.json({
          group_id: "group-a",
          operation_id: params.operationId,
          kind: "artist_search",
          payload: { count: 0, offset: 0, artists: [] },
          payload_sha256: "hash",
        }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ManualSearchPicker
        groupId="group-a"
        groupRevision="revision-a"
        onSelectRelease={() => {}}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "same query" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() =>
    expect(screen.getByText("Совпадений нет.")).toBeInTheDocument(),
  );
  fireEvent.change(screen.getByLabelText("Количество результатов"), {
    target: { value: "25" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[0]).toMatchObject({ query: "same query", limit: 10 });
  expect(bodies[1]).toMatchObject({
    query: "same query",
    limit: 25,
    offset: 0,
  });
  expect(bodies[1].request_id).not.toBe(bodies[0].request_id);
});

it("keeps a stream disconnect notice through a failed fallback and a successful manual reread", async () => {
  let operationReads = 0;
  server.use(
    http.post("*/incoming-groups/:groupId/manual-search", () =>
      HttpResponse.json({ operation_id: "streamer" }, { status: 202 }),
    ),
    http.get("*/operations/streamer", () => {
      operationReads += 1;
      if (operationReads === 2)
        return HttpResponse.json({ detail: "unavailable" }, { status: 500 });
      return HttpResponse.json({
        id: "streamer",
        kind: "provider_artist_search",
        state: "queued",
        stage: "queued",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      });
    }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <ManualSearchPicker
        groupId="group-a"
        groupRevision="revision-a"
        onSelectRelease={() => {}}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText("Поисковый запрос"), {
    target: { value: "stream query" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Искать" }));
  await waitFor(() => expect(operationReads).toBe(1));
  const events = PickerEventSource.instances.at(-1);
  expect(events?.url).toContain("streamer/events");
  events?.dispatchEvent(new Event("error"));
  await waitFor(() => expect(operationReads).toBe(2));
  expect(
    await screen.findByText("Operation event stream disconnected."),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Повторить чтение" }));
  await waitFor(() => expect(operationReads).toBe(3));
  expect(
    screen.getByText("Operation event stream disconnected."),
  ).toBeInTheDocument();
  events?.wake();
  await waitFor(() =>
    expect(
      screen.queryByText("Operation event stream disconnected."),
    ).toBeNull(),
  );
});
