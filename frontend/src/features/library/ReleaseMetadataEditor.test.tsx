import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
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
  artist_credit_baseline: [],
  effective_artist_credit: {
    credits: [],
    origin: "release",
    state: "value",
    status: "resolved",
  },
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

function metadataWithDuplicateCredits(
  revision: number,
): LibraryMetadataGetResponse {
  const result = metadata(revision);
  return {
    ...result,
    envelope: {
      ...result.envelope,
      desired: {
        ...result.envelope.desired,
        artist_credit: {
          authority: "local",
          provenance: { kind: "manual" },
          state: "value",
          value: [
            {
              artist_id: "123e4567-e89b-42d3-a456-426614174000",
              artist_name: "Same local artist",
              credit_name: "First credit",
              join_phrase: " feat. ",
              role: "performer",
            },
            {
              artist_id: "123e4567-e89b-42d3-a456-426614174000",
              artist_name: "Same local artist",
              credit_name: "Second credit",
              join_phrase: " & ",
              role: "legacy-role-value",
            },
          ],
        },
      },
    },
  };
}

function openGroup(region: HTMLElement, label: string) {
  fireEvent.click(within(region).getByText(label, { selector: "summary" }));
}

function renderMetadataEditor(children: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>{children}</I18nProvider>
    </QueryClientProvider>,
  );
}

it("saves recording and position title overrides through their own scopes", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  const releaseId = "position-metadata-release-test";
  const recordingId = "recording-metadata-test";
  const positionId = "position-metadata-test";
  const patchBodies = new Map<string, unknown>();
  const positionMetadata = (revision: number) => {
    const result = metadata(revision, "position");
    result.effective_key = {
      field: {
        authority: "automatic",
        provenance: { kind: "source_metadata", source_field: "KEY" },
        state: "value",
        value: "D♭ minor",
      },
      scope: "recording",
      present: true,
    };
    result.envelope.desired.contributors = {
      authority: "local",
      provenance: { kind: "manual" },
      state: "value",
      value: [
        {
          name: "A, (B)",
          role: "performer",
          instrument: "Piano",
          artist_id: "123e4567-e89b-42d3-a456-426614174000",
          mbid: "223e4567-e89b-42d3-a456-426614174000",
        },
        {
          name: "Added MBID",
          role: "composer",
          artist_id: "323e4567-e89b-42d3-a456-426614174000",
        },
        {
          name: "Cleared MBID",
          role: "lyricist",
          artist_id: "423e4567-e89b-42d3-a456-426614174000",
          mbid: "523e4567-e89b-42d3-a456-426614174000",
        },
      ],
    };
    return result;
  };
  server.use(
    http.get(`*/api/library/recordings/${recordingId}/metadata`, () =>
      HttpResponse.json(metadata(3, "recording")),
    ),
    http.get(
      `*/api/library/releases/${releaseId}/positions/${positionId}/metadata`,
      () => HttpResponse.json(positionMetadata(5)),
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
        return HttpResponse.json(positionMetadata(6));
      },
    ),
  );

  renderMetadataEditor(
    <>
      <ScopedLibraryMetadataEditor scope="recording" ownerId={recordingId} />
      <ScopedLibraryMetadataEditor
        scope="position"
        ownerId={positionId}
        releaseId={releaseId}
      />
    </>,
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
  openGroup(recording, "Названия");
  openGroup(position, "Названия");
  openGroup(position, "Сведения о записи");
  expect(within(position).getByText("D♭ minor")).toBeInTheDocument();
  expect(
    within(position).getByText("Унаследовано от записи"),
  ).toBeInTheDocument();
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
  openGroup(position, "Участники");
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Имя участника 1" }),
    { target: { value: "Doe, Jane (live)" } },
  );
  fireEvent.change(within(position).getByRole("combobox", { name: "Роль 1" }), {
    target: { value: "composer" },
  });
  expect(
    within(position).getByRole("textbox", { name: "Инструмент 1" }),
  ).toHaveValue("Piano");
  fireEvent.change(within(position).getByRole("combobox", { name: "Роль 1" }), {
    target: { value: "performer" },
  });
  fireEvent.change(
    within(position).getByRole("textbox", {
      name: "MusicBrainz ID участника 1",
    }),
    { target: { value: "623e4567-e89b-42d3-a456-426614174000" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", {
      name: "MusicBrainz ID участника 2",
    }),
    { target: { value: "723e4567-e89b-42d3-a456-426614174000" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", {
      name: "MusicBrainz ID участника 3",
    }),
    { target: { value: "" } },
  );
  expect(
    within(position).getByRole("textbox", { name: "Инструмент 1" }),
  ).toHaveValue("Piano");
  expect(
    within(position).queryByText("123e4567-e89b-42d3-a456-426614174000"),
  ).not.toBeInTheDocument();
  expect(
    within(position).queryByText("223e4567-e89b-42d3-a456-426614174000"),
  ).not.toBeInTheDocument();
  expect(
    within(position).getAllByText("Связь с локальным артистом сохранена"),
  ).toHaveLength(3);
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
        contributors: {
          state: "value",
          value: [
            {
              name: "Doe, Jane (live)",
              role: "performer",
              instrument: "Piano",
              artist_id: "123e4567-e89b-42d3-a456-426614174000",
              mbid: "623e4567-e89b-42d3-a456-426614174000",
            },
            {
              name: "Added MBID",
              role: "composer",
              artist_id: "323e4567-e89b-42d3-a456-426614174000",
              mbid: "723e4567-e89b-42d3-a456-426614174000",
            },
            {
              name: "Cleared MBID",
              role: "lyricist",
              artist_id: "423e4567-e89b-42d3-a456-426614174000",
            },
          ],
        },
      },
    });
    expect(patchBodies.get("position")).not.toHaveProperty("changes.key");
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
  const loaded = metadataWithDuplicateCredits(7);
  loaded.envelope.desired.asin = {
    authority: "local",
    provenance: { kind: "manual" },
    state: "absent",
  };
  loaded.provider_release_identifier_baseline = {
    asin: "B001234567",
    digest: "release-identifiers-digest",
    evidence: {
      asin: { authority: 4, fetch_order: 11 },
      "text-representation": { authority: 4, fetch_order: 12 },
    },
    provider_code: "musicbrainz",
    provider_key: "release-provider-key",
    provider_source_id: "provider-source-id",
    script: "Latn",
  };
  server.use(
    http.get(`*/api/library/releases/${releaseId}/metadata`, () =>
      HttpResponse.json(loaded),
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

  renderMetadataEditor(<ReleaseMetadataEditor releaseId={releaseId} />);

  const editButton = screen.getByRole("button", { name: "Изменить" });
  await waitFor(() => expect(editButton).toBeEnabled());
  const release = screen.getByRole("region", { name: "Метаданные релиза" });
  openGroup(release, "Сведения о релизе");
  expect(within(release).getByText("Latn")).toBeInTheDocument();
  expect(
    within(release).getByText("Эффективное значение MusicBrainz"),
  ).toBeInTheDocument();
  const asinField = within(release).getByText("ASIN", {
    selector: "dt",
  }).parentElement;
  expect(asinField).not.toBeNull();
  expect(
    within(asinField as HTMLElement).queryByText("B001234567"),
  ).not.toBeInTheDocument();
  expect(
    within(asinField as HTMLElement).queryByText(
      "Эффективное значение MusicBrainz",
    ),
  ).not.toBeInTheDocument();
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
  const creditDisclosure = within(release).getByText("Исполнители", {
    selector: "summary",
  });
  fireEvent.click(creditDisclosure);
  const credits = within(release).getByRole("region", {
    name: "Исполнители",
  });
  expect(
    within(credits).queryByRole("option", {
      name: "Локальное переопределение не задано",
    }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    within(credits).getByRole("button", {
      name: "Изменить порядок исполнителей: Выше — Исполнитель 2",
    }),
  );
  fireEvent.change(
    within(credits).getAllByRole("textbox", {
      name: "Текст после этого исполнителя",
    })[0],
    { target: { value: "" } },
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
      artist_credit: {
        state: "value",
        value: [
          {
            artist_id: "123e4567-e89b-42d3-a456-426614174000",
            artist_name: "Same local artist",
            credit_name: "Second credit",
            join_phrase: "",
            role: "legacy-role-value",
          },
          {
            artist_id: "123e4567-e89b-42d3-a456-426614174000",
            artist_name: "Same local artist",
            credit_name: "First credit",
            join_phrase: " feat. ",
            role: "performer",
          },
        ],
      },
    },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Метаданные изменились во время редактирования",
  );
  expect(screen.getByRole("textbox", { name: "Название" })).toHaveValue(
    "  New title  ",
  );
  expect(
    within(credits).getAllByRole("textbox", {
      name: "Имя в списке исполнителей",
    }),
  ).toHaveLength(2);
  expect(
    within(credits).getAllByRole("textbox", {
      name: "Имя в списке исполнителей",
    })[0],
  ).toHaveValue("Second credit");
  expect(
    within(credits).getAllByRole("textbox", {
      name: "Текст после этого исполнителя",
    })[0],
  ).toHaveValue("");
});

it("round-trips boolean, decimal, integer, and advisory values by scope", async () => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
  const releaseId = "typed-metadata-release";
  const recordingId = "typed-metadata-recording";
  const positionId = "typed-metadata-position";
  const typed = (
    revision: number,
    scope: string,
    overrides: {
      compilation?: boolean;
      content_advisory?: string;
      bpm?: number;
      track_number?: number;
    } = {},
  ) => {
    const result = metadata(revision, scope);
    if (scope === "release") {
      result.envelope.desired.compilation = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: overrides.compilation ?? false,
      };
      result.envelope.desired.content_advisory = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: overrides.content_advisory ?? "explicit",
      };
    } else {
      result.envelope.desired.bpm = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: overrides.bpm ?? 123.45,
      };
      result.envelope.desired.content_advisory = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: "explicit",
      };
      if (scope === "position")
        result.envelope.desired.track_number = {
          authority: "local",
          provenance: { kind: "manual" },
          state: "value",
          value: overrides.track_number ?? 2,
        };
      result.envelope.desired.works = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value:
          scope === "recording"
            ? [
                {
                  name: "Recording work",
                  mbid: "11111111-1111-4111-8111-111111111111",
                },
              ]
            : [
                {
                  name: "Position work",
                  mbid: "22222222-2222-4222-8222-222222222222",
                },
              ],
      };
      result.envelope.desired.movement = {
        authority: "local",
        provenance: { kind: "manual" },
        state: "value",
        value: { name: "Opening", number: 1, count: 4 },
      };
    }
    return result;
  };
  const patchBodies = new Map<string, unknown>();
  let releaseResponse = typed(3, "release");
  let recordingResponse = typed(5, "recording");
  let positionResponse = typed(7, "position");
  server.use(
    http.get(`*/api/library/releases/${releaseId}/metadata`, () =>
      HttpResponse.json(releaseResponse),
    ),
    http.get(`*/api/library/recordings/${recordingId}/metadata`, () =>
      HttpResponse.json(recordingResponse),
    ),
    http.get(
      `*/api/library/releases/${releaseId}/positions/${positionId}/metadata`,
      () => HttpResponse.json(positionResponse),
    ),
    http.patch(
      `*/api/library/releases/${releaseId}/metadata`,
      async ({ request }) => {
        patchBodies.set("release", await request.json());
        releaseResponse = typed(4, "release", {
          compilation: true,
          content_advisory: "clean",
        });
        return HttpResponse.json(releaseResponse);
      },
    ),
    http.patch(
      `*/api/library/recordings/${recordingId}/metadata`,
      async ({ request }) => {
        patchBodies.set("recording", await request.json());
        recordingResponse = typed(6, "recording", { bpm: 128.75 });
        return HttpResponse.json(recordingResponse);
      },
    ),
    http.patch(
      `*/api/library/releases/${releaseId}/positions/${positionId}/metadata`,
      async ({ request }) => {
        patchBodies.set("position", await request.json());
        positionResponse = typed(8, "position", { track_number: 3 });
        return HttpResponse.json(positionResponse);
      },
    ),
  );

  renderMetadataEditor(
    <>
      <ReleaseMetadataEditor releaseId={releaseId} />
      <ScopedLibraryMetadataEditor scope="recording" ownerId={recordingId} />
      <ScopedLibraryMetadataEditor
        scope="position"
        ownerId={positionId}
        releaseId={releaseId}
      />
    </>,
  );

  const release = screen.getByRole("region", { name: "Метаданные релиза" });
  const recording = screen.getByRole("region", { name: "Метаданные записи" });
  const position = screen.getByRole("region", { name: "Метаданные позиции" });
  await waitFor(() => {
    expect(
      within(release).getByRole("button", { name: "Изменить" }),
    ).toBeEnabled();
    expect(
      within(recording).getByRole("button", { name: "Изменить" }),
    ).toBeEnabled();
    expect(
      within(position).getByRole("button", { name: "Изменить" }),
    ).toBeEnabled();
  });
  openGroup(release, "Сведения о релизе");
  openGroup(recording, "Сведения о записи");
  openGroup(position, "Сведения о позиции");
  openGroup(position, "Дополнительные метаданные");
  openGroup(recording, "Произведения и части");
  openGroup(position, "Произведения и части");
  fireEvent.click(within(release).getByRole("button", { name: "Изменить" }));
  fireEvent.click(within(recording).getByRole("button", { name: "Изменить" }));
  fireEvent.click(within(position).getByRole("button", { name: "Изменить" }));
  fireEvent.change(within(release).getByRole("combobox", { name: "Сборник" }), {
    target: { value: "true" },
  });
  fireEvent.change(
    within(release).getByRole("combobox", {
      name: "Предупреждение о содержании",
    }),
    { target: { value: "clean" } },
  );
  fireEvent.change(
    within(release).getByRole("textbox", { name: "Дата выпуска" }),
    {
      target: { value: "2024-13" },
    },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Темп (BPM)" }),
    {
      target: { value: "-4" },
    },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Номер дорожки" }),
    { target: { value: "3" } },
  );
  fireEvent.click(within(release).getByRole("button", { name: "Сохранить" }));
  expect(await within(release).findByRole("alert")).toHaveTextContent(
    "Введите корректную дату",
  );
  expect(patchBodies.has("release")).toBe(false);
  expect(
    within(release).getByRole("textbox", { name: "Дата выпуска" }),
  ).toHaveValue("2024-13");
  fireEvent.change(
    within(release).getByRole("textbox", { name: "Дата выпуска" }),
    {
      target: { value: "2024-02" },
    },
  );
  fireEvent.change(
    within(release).getByRole("combobox", {
      name: "Версия релиза — Состояние переопределения",
    }),
    { target: { value: "value" } },
  );
  fireEvent.change(
    within(release).getByRole("textbox", { name: "Версия релиза" }),
    { target: { value: "Deluxe Edition" } },
  );
  fireEvent.change(
    within(recording).getByRole("combobox", {
      name: "Дата записи — Состояние переопределения",
    }),
    { target: { value: "value" } },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Дата записи" }),
    { target: { value: "1999-03-04" } },
  );
  fireEvent.change(
    within(position).getByRole("combobox", {
      name: "Подзаголовок трека — Состояние переопределения",
    }),
    { target: { value: "value" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Подзаголовок трека" }),
    { target: { value: "Live at the Forum" } },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Название произведения 1" }),
    { target: { value: "Recording work edited" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Название произведения 1" }),
    { target: { value: "Position work edited" } },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Название части" }),
    { target: { value: "Finale" } },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Номер части" }),
    { target: { value: "2" } },
  );
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Всего частей" }),
    { target: { value: "6" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Название части" }),
    { target: { value: "Coda" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Номер части" }),
    { target: { value: "3" } },
  );
  fireEvent.change(
    within(position).getByRole("textbox", { name: "Всего частей" }),
    { target: { value: "7" } },
  );
  expect(
    within(recording).getByText("11111111-1111-4111-8111-111111111111"),
  ).toBeInTheDocument();
  expect(
    within(recording).queryByRole("textbox", {
      name: "MusicBrainz ID произведения",
    }),
  ).not.toBeInTheDocument();
  fireEvent.click(within(recording).getByRole("button", { name: "Сохранить" }));
  expect(await within(recording).findByRole("alert")).toHaveTextContent(
    "Введите положительное конечное число",
  );
  expect(patchBodies.has("recording")).toBe(false);
  expect(
    within(recording).getByRole("textbox", { name: "Темп (BPM)" }),
  ).toHaveValue("-4");
  fireEvent.change(
    within(recording).getByRole("textbox", { name: "Темп (BPM)" }),
    {
      target: { value: "128.75" },
    },
  );
  fireEvent.click(within(release).getByRole("button", { name: "Сохранить" }));
  fireEvent.click(within(recording).getByRole("button", { name: "Сохранить" }));
  fireEvent.click(within(position).getByRole("button", { name: "Сохранить" }));

  await waitFor(() => {
    expect(patchBodies.get("release")).toEqual({
      expected_revision: 3,
      changes: {
        compilation: { state: "value", value: true },
        content_advisory: { state: "value", value: "clean" },
        release_date: { state: "value", value: "2024-02" },
        release_version: { state: "value", value: "Deluxe Edition" },
      },
    });
    expect(patchBodies.get("recording")).toEqual({
      expected_revision: 5,
      changes: {
        bpm: { state: "value", value: 128.75 },
        recording_date: { state: "value", value: "1999-03-04" },
        works: {
          state: "value",
          value: [
            {
              name: "Recording work edited",
              mbid: "11111111-1111-4111-8111-111111111111",
            },
          ],
        },
        movement: {
          state: "value",
          value: { name: "Finale", number: 2, count: 6 },
        },
      },
    });
    expect(patchBodies.get("position")).toEqual({
      expected_revision: 7,
      changes: {
        track_number: { state: "value", value: 3 },
        subtitle: { state: "value", value: "Live at the Forum" },
        works: {
          state: "value",
          value: [
            {
              name: "Position work edited",
              mbid: "22222222-2222-4222-8222-222222222222",
            },
          ],
        },
        movement: {
          state: "value",
          value: { name: "Coda", number: 3, count: 7 },
        },
      },
    });
  });
  await waitFor(() => {
    expect(within(release).getByText("true")).toBeInTheDocument();
    expect(within(release).getByText("clean")).toBeInTheDocument();
    expect(within(recording).getByText("128.75")).toBeInTheDocument();
    expect(within(position).getByText("3")).toBeInTheDocument();
  });
});
