import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import type { LibraryMetadataGetResponse } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ReleaseMetadataEditor } from "./ReleaseMetadataEditor";

const releaseId = "release-metadata-editor-test";
const metadata = (revision: number): LibraryMetadataGetResponse => ({
  envelope: {
    scope: "release",
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
