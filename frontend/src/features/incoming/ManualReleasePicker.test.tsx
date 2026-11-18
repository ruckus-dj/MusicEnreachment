import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { subscribeToOperation } from "../../api/client/operations";
import type { ManualReleaseSelectionBody } from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ManualReleasePicker } from "./ManualReleasePicker";

const { unsubscribe } = vi.hoisted(() => ({ unsubscribe: vi.fn() }));

vi.mock("../../api/client/operations", () => ({
  subscribeToOperation: vi.fn(() => unsubscribe),
}));

const releaseMBID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const officialURL = `https://musicbrainz.org/release/${releaseMBID}`;

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

function renderPicker(onPrepared = vi.fn()) {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider>
      <QueryClientProvider client={queryClient}>
        <LocaleToggle />
        <ManualReleasePicker
          groupId="group-1"
          groupRevision="revision-1"
          evaluationRevision="evaluation-1"
          onPrepared={onPrepared}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onPrepared, queryClient };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.mocked(subscribeToOperation).mockClear();
  unsubscribe.mockClear();
});

it("prepares an explicitly entered release and reports only its canonical MBID", async () => {
  const bodies: ManualReleaseSelectionBody[] = [];
  const operationReads: string[] = [];
  server.use(
    http.post(
      "/api/incoming-groups/group-1/matching/manual-release",
      async ({ request }) => {
        bodies.push((await request.json()) as ManualReleaseSelectionBody);
        return HttpResponse.json(
          { operation_id: "operation-1" },
          { status: 202 },
        );
      },
    ),
    http.get("/api/operations/:operationId", ({ params }) => {
      operationReads.push(String(params.operationId));
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
  );
  const { onPrepared, queryClient } = renderPicker();

  fireEvent.change(
    screen.getByRole("textbox", { name: "Release MBID or official URL" }),
    { target: { value: officialURL } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Prepare release" }));
  await screen.findByText("Release prepared.");

  expect(onPrepared).toHaveBeenCalledWith(releaseMBID);
  expect(bodies).toHaveLength(1);
  expect(bodies[0]).toMatchObject({
    expected_group_revision: "revision-1",
    expected_evaluation_revision: "evaluation-1",
    selected_release: officialURL,
  });
  expect(bodies[0].request_id).toEqual(expect.any(String));
  expect(operationReads).toEqual(["operation-1"]);
  expect(vi.mocked(subscribeToOperation)).toHaveBeenCalledTimes(1);
  expect(unsubscribe).toHaveBeenCalledTimes(1);

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(
    await screen.findByRole("heading", {
      name: "Выбрать издание по MBID или ссылке",
    }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("textbox", {
      name: "MBID или официальная ссылка на издание",
    }),
  ).toHaveValue(officialURL);
  expect(
    screen.getByRole("button", { name: "Подготовить издание" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Издание подготовлено.")).toBeInTheDocument();
  expect(bodies).toHaveLength(1);
  expect(operationReads).toEqual(["operation-1"]);

  queryClient.clear();
});

it("retains the admitted request for retry and falls back to safe operation errors", async () => {
  const bodies: ManualReleaseSelectionBody[] = [];
  let starts = 0;
  server.use(
    http.post(
      "/api/incoming-groups/group-1/matching/manual-release",
      async ({ request }) => {
        starts += 1;
        bodies.push((await request.json()) as ManualReleaseSelectionBody);
        if (starts === 1)
          return HttpResponse.json(
            { title: "Service Unavailable" },
            { status: 503 },
          );
        return HttpResponse.json(
          { operation_id: "operation-2" },
          { status: 202 },
        );
      },
    ),
    http.get("/api/operations/operation-2", () =>
      HttpResponse.json({
        id: "operation-2",
        kind: "provider_fetch",
        state: "failed",
        stage: "complete",
        bytes_completed: 0,
        safe_error: "MusicBrainz release was not found.",
        created_at: "2026-11-14T00:00:00Z",
        updated_at: "2026-11-14T00:00:01Z",
      }),
    ),
  );
  const { onPrepared, queryClient } = renderPicker();

  fireEvent.change(
    screen.getByRole("textbox", { name: "Release MBID or official URL" }),
    { target: { value: releaseMBID } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Prepare release" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not prepare the release (HTTP 503).",
  );

  fireEvent.click(screen.getByRole("button", { name: "Prepare release" }));
  expect(
    await screen.findByText("MusicBrainz release was not found."),
  ).toBeInTheDocument();
  expect(starts).toBe(2);
  expect(bodies[0].request_id).toBe(bodies[1].request_id);
  expect(bodies[1].selected_release).toBe(releaseMBID);
  expect(onPrepared).not.toHaveBeenCalled();
  expect(screen.queryByText("Release prepared.")).toBeNull();

  queryClient.clear();
});

it("prefills the explicit selection from the release search without preparing it", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <I18nProvider>
      <QueryClientProvider client={queryClient}>
        <ManualReleasePicker
          groupId="group-1"
          groupRevision="revision-1"
          searchRelease={releaseMBID}
          onPrepared={vi.fn()}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );

  await waitFor(() =>
    expect(
      screen.getByRole("textbox", { name: "Release MBID or official URL" }),
    ).toHaveValue(releaseMBID),
  );
  expect(vi.mocked(subscribeToOperation)).not.toHaveBeenCalled();

  queryClient.clear();
});
