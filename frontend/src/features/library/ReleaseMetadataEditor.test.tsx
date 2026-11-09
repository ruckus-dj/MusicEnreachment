import "@testing-library/jest-dom/vitest";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import type { LibraryMetadataGetResponse } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import {
  ReleaseMetadataEditor,
  ScopedLibraryMetadataEditor,
} from "./ReleaseMetadataEditor";

const releaseId = "release-metadata-editor-test";
const metadata = (
  revision: number,
  scope = "release",
): LibraryMetadataGetResponse => ({
  envelope: {
    scope,
    version: 1,
    desired: {
      title: {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: "Original title",
      },
      release_date: {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: "2020",
      },
      label: {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: ["First label", "Second label"],
      },
      catalog_number: {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: "CAT-1",
      },
    },
  },
  publication_applied: false,
  revision,
});

it("saves recording and position title overrides through their own scopes", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  const releaseId = "position-metadata-release-test";
  const recordingId = "recording-metadata-test";
  const positionId = "position-metadata-test";
  const patchBodies = new Map<string, unknown>();
  server.use(
    http.get(`*/api/library/recordings/${recordingId}/metadata`, () =>
      HttpResponse.json(metadata(3, "recording")),
    ),
    http.get(
      `*/api/library/releases/${releaseId}/positions/${positionId}/metadata`,
      () => HttpResponse.json(metadata(5, "position")),
    ),
    http.patch(
      `*/api/library/recordings/${recordingId}/metadata`,
      async ({ request }) => {
        patchBodies.set("recording", await request.json());
        return HttpResponse.json(metadata(4, "recording"));
      },
    ),
    http.patch(
      `*/api/library/releases/${releaseId}/positions/${positionId}/metadata`,
      async ({ request }) => {
        patchBodies.set("position", await request.json());
        return HttpResponse.json(metadata(6, "position"));
      },
    ),
  );

  render(
    <I18nProvider>
      <ScopedLibraryMetadataEditor scope="recording" ownerId={recordingId} />
      <ScopedLibraryMetadataEditor
        scope="position"
        ownerId={positionId}
        releaseId={releaseId}
      />
    </I18nProvider>,
  );

  const recording = screen.getByRole("region", {
    name: "Метаданные записи",
  });
  const position = screen.getByRole("region", {
    name: "Метаданные позиции",
  });
  const recordingEdit = within(recording).getByRole("button", {
    name: "Изменить",
  });
  const positionEdit = within(position).getByRole("button", {
    name: "Изменить",
  });
  await waitFor(() => {
    expect(recordingEdit).toBeEnabled();
    expect(positionEdit).toBeEnabled();
  });
  fireEvent.click(recordingEdit);
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Название" }),
    {
      target: { value: "Recording title" },
    },
  );
  fireEvent.click(within(position).getByRole("button", { name: "Изменить" }));
  fireEvent.change(
    within(position).getByRole("combobox", {
      name: "Сортировочное название — Состояние переопределения",
    }),
    { target: { value: "value" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Сортировочное название" }),
    {
      target: { value: "Position sort title" },
    },
  );
  fireEvent.change(
    within(position).getByRole("combobox", {
      name: "Название — Состояние переопределения",
    }),
    { target: { value: "explicit_empty" } },
  );
  fireEvent.click(within(recording).getByRole("button", { name: "Сохранить" }));
  fireEvent.click(within(position).getByRole("button", { name: "Сохранить" }));

  await waitFor(() => {
    expect(patchBodies.get("recording")).toEqual({
      expected_revision: 3,
      changes: { title: { state: "value", value: "Recording title" } },
    });
    expect(patchBodies.get("position")).toEqual({
      expected_revision: 5,
      changes: {
        sort_title: { state: "value", value: "Position sort title" },
        title: { state: "explicit_empty" },
      },
    });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

it("sends exact typed states at the loaded revision and retains the draft on conflict", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  let patchBody: unknown;
  server.use(
    http.get(`*/api/library/releases/${releaseId}/metadata`, () =>
      HttpResponse.json(metadata(7)),
    ),
    http.patch(
      `*/api/library/releases/${releaseId}/metadata`,
      async ({ request }) => {
        patchBody = await request.json();
        return HttpResponse.json(
          { detail: "revision conflict" },
          { status: 409 },
        );
      },
    ),
  );

  render(
    <I18nProvider>
      <ReleaseMetadataEditor releaseId={releaseId} />
    </I18nProvider>,
  );

  const editButton = screen.getByRole("button", { name: "Изменить" });
  await waitFor(() => expect(editButton).toBeEnabled());
  fireEvent.click(editButton);
  fireEvent.change(screen.getByRole("textbox", { name: "Название" }), {
    target: { value: "  New title  " },
  });
  fireEvent.change(
    screen.getByRole("combobox", {
      name: "Дата выпуска — Состояние переопределения",
    }),
    { target: { value: "explicit_empty" } },
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Лейблы" }), {
    target: { value: "North\nSouth" },
  });
  fireEvent.change(
    screen.getByRole("combobox", {
      name: "Номер каталога — Состояние переопределения",
    }),
    { target: { value: "absent" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));

  await waitFor(() => expect(patchBody).toBeDefined());
  expect(patchBody).toEqual({
    expected_revision: 7,
    changes: {
      title: { state: "value", value: "  New title  " },
      release_date: { state: "explicit_empty" },
      label: { state: "value", value: ["North", "South"] },
      catalog_number: { state: "absent" },
    },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Метаданные изменились во время редактирования",
  );
  expect(screen.getByRole("textbox", { name: "Название" })).toHaveValue(
    "  New title  ",
  );
});
