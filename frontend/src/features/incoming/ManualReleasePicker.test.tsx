import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { ManualReleasePicker } from "./ManualReleasePicker";

it("retries an uncertain admission with the identical request and reports the selected release", async () => {
  const onPrepared = vi.fn();
  const bodies: unknown[] = [];
  let admissionAttempts = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/manual-release",
      async ({ request }) => {
        bodies.push(await request.json());
        admissionAttempts += 1;
        if (admissionAttempts === 1) return HttpResponse.error();
        return HttpResponse.json(
          { operation_id: "operation-1" },
          { status: 202 },
        );
      },
    ),
    http.get("*/operations/operation-1", () =>
      HttpResponse.json({
        id: "operation-1",
        kind: "matching_evaluation_v1",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ManualReleasePicker
        groupId="group-1"
        groupRevision="group-rev"
        evaluationRevision="evaluation-rev"
        onPrepared={onPrepared}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(
    screen.getByLabelText("MBID или официальная ссылка на издание"),
    {
      target: {
        value:
          "https://musicbrainz.org/release/63f8a3a4-1bb8-4d2f-a3fd-2c0d1dc8bb0d",
      },
    },
  );
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
  await waitFor(() =>
    expect(onPrepared).toHaveBeenCalledWith(
      "63f8a3a4-1bb8-4d2f-a3fd-2c0d1dc8bb0d",
    ),
  );
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toEqual(bodies[0]);
  expect(bodies[0]).toMatchObject({
    expected_group_revision: "group-rev",
    expected_evaluation_revision: "evaluation-rev",
  });
});

it("rebuilds a rejected request with the current revisions", async () => {
  const bodies: Record<string, unknown>[] = [];
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/manual-release",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json(
          { detail: "Оценка изменилась." },
          { status: bodies.length === 1 ? 409 : 202 },
        );
      },
    ),
    http.get("*/operations/operation-2", () =>
      HttpResponse.json({
        id: "operation-2",
        kind: "matching_evaluation_v1",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const props = {
    groupId: "group-1",
    groupRevision: "group-rev-1",
    evaluationRevision: "evaluation-rev-1",
    onPrepared: vi.fn(),
  };
  const view = render(
    <QueryClientProvider client={client}>
      <ManualReleasePicker {...props} />
    </QueryClientProvider>,
  );
  const input = screen.getByLabelText("MBID или официальная ссылка на издание");
  fireEvent.change(input, { target: { value: "release-value" } });
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
  await screen.findByRole("alert");

  view.rerender(
    <QueryClientProvider client={client}>
      <ManualReleasePicker
        {...props}
        groupRevision="group-rev-2"
        evaluationRevision="evaluation-rev-2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[0]).toMatchObject({
    expected_group_revision: "group-rev-1",
    expected_evaluation_revision: "evaluation-rev-1",
  });
  expect(bodies[1]).toMatchObject({
    expected_group_revision: "group-rev-2",
    expected_evaluation_revision: "evaluation-rev-2",
    selected_release: "release-value",
  });
  expect(bodies[1].request_id).not.toBe(bodies[0].request_id);
});

it("keeps admission and operation tracking with the original group across navigation", async () => {
  const admission = deferred<Response>();
  const onPrepared = vi.fn();
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/manual-release",
      () => admission.promise,
    ),
    http.get("*/operations/operation-3", () =>
      HttpResponse.json({
        id: "operation-3",
        kind: "matching_evaluation_v1",
        state: "succeeded",
        stage: "complete",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const matchingKey = ["/api/incoming-groups/group-a/matching"];
  client.setQueryData(matchingKey, { revision: "old" });
  const renderPicker = (groupId: string) => (
    <QueryClientProvider client={client}>
      <ManualReleasePicker
        groupId={groupId}
        groupRevision={`${groupId}-revision`}
        evaluationRevision={`${groupId}-evaluation`}
        onPrepared={onPrepared}
      />
    </QueryClientProvider>
  );
  const view = render(renderPicker("group-a"));
  fireEvent.change(
    screen.getByLabelText("MBID или официальная ссылка на издание"),
    { target: { value: "release-a" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Подготовить издание" }));
  view.rerender(renderPicker("group-b"));
  admission.resolve(
    HttpResponse.json(
      { operation_id: "operation-3" },
      { status: 202 },
    ) as Response,
  );
  await waitFor(() =>
    expect(client.getQueryState(matchingKey)?.isInvalidated).toBe(true),
  );
  expect(onPrepared).not.toHaveBeenCalled();
  view.rerender(renderPicker("group-a"));
  expect(await screen.findByText("Издание подготовлено.")).toBeInTheDocument();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
