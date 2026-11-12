import "@testing-library/jest-dom/vitest";
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
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type {
  LibraryDeletionPreviewResponse,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { nextResponse, server } from "../../test/server";
import { LibraryDeletionControl } from "./LibraryDeletionControl";

const releaseID = "f2a1a759-b1a4-4f03-8dca-13a6ac78ff10";
const nextReleaseID = "f2a1a759-b1a4-4f03-8dca-13a6ac78ff11";
const positionID = "6c754873-65f0-486e-8b5f-cdf70a6f30e6";
const operationID = "76092c63-0e0f-4dc1-af39-674dc1ce037c";
const digest = "a".repeat(64);

class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  readonly url: string;
  close = vi.fn();

  constructor(url: string) {
    super();
    this.url = url;
    TestEventSource.instances.push(this);
  }
}

function operation(
  state: string,
  updatedAt: string,
  bytesCompleted = 0,
): OperationResponse {
  return {
    id: operationID,
    kind: "library_deletion",
    state,
    stage: state === "failed" ? "deleting_publications" : "queued",
    bytes_completed: bytesCompleted,
    bytes_total: 1024,
    created_at: "2026-11-11T10:00:00Z",
    updated_at: updatedAt,
    ...(state === "failed"
      ? { safe_error: "One remaining publication could not be removed." }
      : {}),
  };
}

function preview(
  releaseId: string,
  title: string,
  path: string,
): LibraryDeletionPreviewResponse {
  return {
    blockers: [],
    confirmation_digest: digest,
    kind: "release",
    outputs: [
      {
        capturable: true,
        position_id: positionID,
        relative_path: path,
      },
    ],
    position_ids: [positionID],
    recording_ids: [],
    recording_count: 0,
    release_id: releaseId,
    title,
  };
}

function component(targetReleaseID = releaseID, onSnapshot = vi.fn()) {
  return (
    <I18nProvider>
      <LibraryDeletionControl
        target={{ type: "release", releaseId: targetReleaseID }}
        releaseTitle={targetReleaseID === releaseID ? "Release A" : "Release B"}
        positionLabels={new Map([[positionID, "1. Position One"]])}
        onSnapshot={onSnapshot}
      />
    </I18nProvider>
  );
}

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("replays one frozen confirmation, follows partial failure, and retries the same operation", async () => {
  const confirmBodies: Array<{ confirmation_digest: string }> = [];
  const retryIDs: string[] = [];
  let previewReads = 0;
  let confirmationCalls = 0;
  let operationReads = 0;
  let currentOperation = operation("failed", "2026-11-11T10:00:02Z", 512);
  server.use(
    http.get("*/api/library/releases/:releaseId/deletion/preview", () => {
      previewReads += 1;
      return HttpResponse.json(
        preview(
          releaseID,
          "Release A deletion",
          "Artist/Release/01 - Track.flac",
        ),
      );
    }),
    http.post("*/api/library/deletion/confirm", async ({ request }) => {
      confirmBodies.push(
        (await request.json()) as { confirmation_digest: string },
      );
      confirmationCalls += 1;
      if (confirmationCalls === 1) return HttpResponse.error();
      return HttpResponse.json(operation("queued", "2026-11-11T10:00:01Z"), {
        status: 202,
      });
    }),
    http.get("*/api/operations/:operationId", () => {
      operationReads += 1;
      return HttpResponse.json(currentOperation);
    }),
    http.post("*/api/operations/:operationId/retry", ({ params }) => {
      retryIDs.push(String(params.operationId));
      currentOperation = operation("queued", "2026-11-11T10:00:03Z", 512);
      return HttpResponse.json(currentOperation);
    }),
  );

  render(component());
  fireEvent.click(screen.getByRole("button", { name: "Удалить релиз" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );

  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Release A deletion");
  expect(dialog).toHaveTextContent("Позиции");
  expect(dialog).toHaveTextContent("Файлы текущих публикаций");
  expect(dialog).toHaveTextContent("Композиции, удаляемые из медиатеки");
  expect(dialog).toHaveTextContent("1. Position One");
  expect(dialog).toHaveTextContent("Artist/Release/01 - Track.flac");
  expect(within(dialog).getAllByText("1", { exact: true })).toHaveLength(2);
  for (const id of [releaseID, positionID, operationID]) {
    expect(dialog).not.toHaveTextContent(id);
  }
  expect(previewReads).toBe(1);

  fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  await screen.findByText(/Ответ на подтверждение не получен/);
  expect(confirmBodies).toEqual([{ confirmation_digest: digest }]);

  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторить подтверждение того же предпросмотра",
    }),
  );
  await screen.findByText(/Удаление завершилось с ошибкой/);
  expect(previewReads).toBe(1);
  expect(confirmBodies).toEqual([
    { confirmation_digest: digest },
    { confirmation_digest: digest },
  ]);
  expect(
    screen.getByText(/успешно удалённые публикации отражаются сразу/i),
  ).toBeInTheDocument();

  fireEvent.click(
    screen.getByRole("button", { name: "Повторить неудавшееся удаление" }),
  );
  await waitFor(() => expect(retryIDs).toEqual([operationID]));
  await waitFor(() => expect(operationReads).toBeGreaterThan(1));
  await waitFor(() => expect(TestEventSource.instances).toHaveLength(2));
  const terminalEventSource = TestEventSource.instances[0];
  const activeEventSource = TestEventSource.instances[1];
  expect(terminalEventSource?.close).toHaveBeenCalledTimes(1);
  expect(activeEventSource?.url).toBe("/api/operations/events");

  currentOperation = operation("running", "2026-11-11T10:00:04Z", 768);
  const readsBeforeOpen = operationReads;
  await act(async () => {
    activeEventSource?.dispatchEvent(new Event("open"));
  });
  await waitFor(() => expect(operationReads).toBeGreaterThan(readsBeforeOpen));
  expect(await screen.findByText(/Удаление выполняется/)).toBeInTheDocument();

  const previousReads = operationReads;
  await act(async () => {
    activeEventSource?.dispatchEvent(
      new MessageEvent("operation-changed", {
        data: operationID,
      }),
    );
  });
  await waitFor(() => expect(operationReads).toBeGreaterThan(previousReads));
  expect(await screen.findByText(/Удаление выполняется/)).toBeInTheDocument();
  const progress = screen.getByLabelText("Обработано байт");
  expect(progress).toHaveAttribute("value", "768");
  expect(progress).toHaveAttribute("max", "1024");
  expect(previewReads).toBe(1);
  for (const id of [releaseID, positionID, operationID, nextReleaseID]) {
    expect(document.body).not.toHaveTextContent(id);
  }
});

it("ignores a stale preview after target change and ignores confirmation after unmount", async () => {
  let resolveFirstPreview!: (response: Response) => void;
  let firstPreviewStarted!: () => void;
  let resolveConfirmation!: (response: Response) => void;
  let confirmationStarted!: () => void;
  const previewStarted = new Promise<void>((resolve) => {
    firstPreviewStarted = resolve;
  });
  const confirmStarted = new Promise<void>((resolve) => {
    confirmationStarted = resolve;
  });
  const onSnapshot = vi.fn();
  server.use(
    http.get(
      "*/api/library/releases/:releaseId/deletion/preview",
      ({ params }) => {
        if (params.releaseId === releaseID) {
          firstPreviewStarted();
          return new Promise<Response>((resolve) => {
            resolveFirstPreview = resolve;
          });
        }
        return HttpResponse.json(
          preview(nextReleaseID, "Release B deletion", "B/Release/01.flac"),
        );
      },
    ),
    http.post("*/api/library/deletion/confirm", () => {
      confirmationStarted();
      return new Promise<Response>((resolve) => {
        resolveConfirmation = resolve;
      });
    }),
  );

  const view = render(component(releaseID, onSnapshot));
  fireEvent.click(screen.getByRole("button", { name: "Удалить релиз" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );
  await previewStarted;
  view.rerender(component(nextReleaseID, onSnapshot));
  const staleResponse = nextResponse(
    `/api/library/releases/${releaseID}/deletion/preview`,
    "GET",
  );
  await act(async () => {
    resolveFirstPreview(
      HttpResponse.json(
        preview(releaseID, "Stale Release A", "A/Release/stale.flac"),
      ),
    );
  });
  await staleResponse;
  fireEvent.click(screen.getByRole("button", { name: "Удалить релиз" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );
  expect(await screen.findByText("Release B deletion")).toBeInTheDocument();
  expect(screen.queryByText("Stale Release A")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  await confirmStarted;
  const lateConfirmation = nextResponse(
    "/api/library/deletion/confirm",
    "POST",
  );
  view.unmount();
  await act(async () => {
    resolveConfirmation(
      HttpResponse.json(operation("succeeded", "2026-11-11T10:00:01Z"), {
        status: 202,
      }),
    );
  });
  await lateConfirmation;
  expect(onSnapshot).not.toHaveBeenCalled();
});
