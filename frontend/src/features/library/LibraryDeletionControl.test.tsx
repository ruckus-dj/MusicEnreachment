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
const artistID = "f2a1a759-b1a4-4f03-8dca-13a6ac78ff12";
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
  confirmationDigest = digest,
): LibraryDeletionPreviewResponse {
  return {
    blockers: [],
    confirmation_digest: confirmationDigest,
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
  const refreshedDigest = "b".repeat(64);
  const confirmBodies: Array<{ confirmation_digest: string }> = [];
  const retryIDs: string[] = [];
  let previewReads = 0;
  let resolveRefreshedPreview!: (response: Response) => void;
  let refreshedPreviewStarted!: () => void;
  const refreshStarted = new Promise<void>((resolve) => {
    refreshedPreviewStarted = resolve;
  });
  let confirmationCalls = 0;
  let operationReads = 0;
  let currentOperation = operation("failed", "2026-11-11T10:00:02Z", 512);
  server.use(
    http.get("*/api/library/releases/:releaseId/deletion/preview", () => {
      previewReads += 1;
      if (previewReads === 2) {
        refreshedPreviewStarted();
        return new Promise<Response>((resolve) => {
          resolveRefreshedPreview = resolve;
        });
      }
      const body = preview(
        releaseID,
        "Release A deletion",
        "Artist/Release/01 - Track.flac",
      );
      if (previewReads === 1) {
        body.blockers = [
          {
            code: "publication_blocked",
            message: "publication cannot be safely deleted",
            position_id: positionID,
          },
        ];
      }
      return HttpResponse.json(body);
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
  expect(dialog).toHaveTextContent("publication cannot be safely deleted");

  const blockedConfirm = screen.getByRole("button", {
    name: "Подтвердить удаление",
  });
  expect(blockedConfirm).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Обновить предпросмотр" }),
  ).toBeEnabled();
  fireEvent.click(blockedConfirm);
  expect(confirmBodies).toEqual([]);

  fireEvent.click(
    screen.getByRole("button", { name: "Обновить предпросмотр" }),
  );
  await refreshStarted;
  expect(previewReads).toBe(2);
  const confirmButton = screen.getByRole("button", {
    name: "Подтвердить удаление",
  });
  expect(confirmButton).toBeDisabled();
  fireEvent.click(confirmButton);
  expect(confirmBodies).toEqual([]);

  const refreshResponse = nextResponse(
    `/api/library/releases/${releaseID}/deletion/preview`,
    "GET",
  );
  await act(async () => {
    resolveRefreshedPreview(
      HttpResponse.json(
        preview(
          releaseID,
          "Release A refreshed deletion",
          "Artist/Release/01 - Refreshed.flac",
          refreshedDigest,
        ),
      ),
    );
  });
  await refreshResponse;
  expect(
    await screen.findByText("Release A refreshed deletion"),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  await screen.findByText(/Ответ на подтверждение не получен/);
  expect(
    screen.queryByRole("button", { name: "Обновить предпросмотр" }),
  ).not.toBeInTheDocument();
  expect(confirmBodies).toEqual([{ confirmation_digest: refreshedDigest }]);

  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторить подтверждение того же предпросмотра",
    }),
  );
  await screen.findByText(/Удаление завершилось с ошибкой/);
  expect(previewReads).toBe(2);
  expect(confirmBodies).toEqual([
    { confirmation_digest: refreshedDigest },
    { confirmation_digest: refreshedDigest },
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
  expect(previewReads).toBe(2);
  for (const id of [releaseID, positionID, operationID, nextReleaseID]) {
    expect(document.body).not.toHaveTextContent(id);
  }
});

it("previews artist release titles and coartist warnings without rendering UUIDs", async () => {
  const artistDigest = "b".repeat(64);
  let previewReads = 0;
  server.use(
    http.get(
      "*/api/library/artists/:artistId/deletion/preview",
      ({ params }) => {
        expect(params.artistId).toBe(artistID);
        previewReads += 1;
        return HttpResponse.json({
          artist_releases: [{ title: "Primary album" }],
          blockers: [],
          coartist_warnings: [
            { artist_names: ["Guest artist"], release_title: "Primary album" },
          ],
          confirmation_digest: artistDigest,
          kind: "artist",
          outputs: [],
          position_ids: [],
          recording_count: 0,
          recording_ids: [],
          title: "Artist",
        });
      },
    ),
  );

  render(
    <I18nProvider>
      <LibraryDeletionControl
        target={{ type: "artist", artistId: artistID }}
        releaseTitle="Artist"
      />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Удалить артиста" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить точный предпросмотр" }),
  );

  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Primary album");
  expect(dialog).toHaveTextContent("Guest artist");
  expect(dialog).toHaveTextContent("Основные релизы, включённые в удаление");
  expect(dialog).toHaveTextContent("Релизы с соисполнителями");
  expect(dialog).not.toHaveTextContent(artistID);
  expect(previewReads).toBe(1);
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
