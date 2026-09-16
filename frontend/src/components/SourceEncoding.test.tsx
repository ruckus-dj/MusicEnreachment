// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SourceEncoding } from "./SourceEncoding";

const field = {
  field_id: 7,
  tag_name: "TITLE",
  container: "ID3v2",
  physical_id: "TIT2:0",
  extraction_version: "v1",
  selected: true,
  original_value: "Ïðèâåò",
  current_value: "Ïðèâåò",
  raw_evidence_available: true,
  declared_codec: "latin-1",
  applied_choice: null,
};
const applyButton = () =>
  screen.getByRole("button", { name: "Применить кодировки" }) as HTMLButtonElement;
const choose = async (mode = "cp1251") =>
  fireEvent.change(await screen.findByLabelText("Кодировка TITLE · ID3v2 · TIT2:0 · #7"), {
    target: { value: mode },
  });
const detail = { source_id: "a", source_revision: 3, fields: [field] };
const preview = {
  source_id: "a",
  source_revision: 3,
  valid: true,
  fields: [{ field_id: 7, value: "Привет", status: "changed", error: null }],
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("has exactly one codec selector per field, original label and effective auto codec", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(
    Response.json({
      ...detail,
      fields: [
        {
          ...field,
          current_value: "Привет",
          decision_origin: "auto",
          applied_choice: {
            field_id: 7,
            mode: "unicode",
            encode_codec: "latin-1",
            decode_codec: "cp1251",
          },
        },
        { ...field, field_id: 8, selected: false, declared_codec: null },
      ],
    }),
  );
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await screen.findByText("Привет");
  expect(screen.getAllByRole("combobox")).toHaveLength(2);
  expect(
    (screen.getByLabelText("Кодировка TITLE · ID3v2 · TIT2:0 · #7") as HTMLSelectElement).value,
  ).toBe("cp1251");
  expect(screen.getByRole("option", { name: "LATIN-1 (Исходная)" })).toBeTruthy();
  expect(screen.getByRole("option", { name: "Неизвестна (Исходная)" })).toBeTruthy();
  expect(screen.getByText("Авто")).toBeTruthy();
  expect(screen.queryByText("Расширенные настройки")).toBeNull();
  expect(screen.queryByText("Использовать предложение")).toBeNull();
  expect(screen.getByText(/Теневые физические поля/).closest("details")?.open).toBe(false);
  expect(applyButton().disabled).toBe(true);
  expect(fetcher.mock.calls).toHaveLength(1);
});

it("distinguishes recovered and original interpretations of the same codec", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    Response.json(
      String(url).endsWith("/preview")
        ? preview
        : {
            ...detail,
            fields: [
              {
                ...field,
                declared_codec: "cp1251",
                current_value: "Привет",
                decision_origin: "auto",
                applied_choice: {
                  field_id: 7,
                  mode: "unicode",
                  encode_codec: "latin-1",
                  decode_codec: "cp1251",
                },
              },
            ],
          },
    ),
  );
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  const select = (await screen.findByRole("combobox")) as HTMLSelectElement;
  expect(select.value).toBe("cp1251");
  expect(screen.getByRole("option", { name: "CP1251 (Исходная)" })).toBeTruthy();
  expect(screen.getByRole("option", { name: "CP1251" })).toBeTruthy();
  expect(screen.queryByText("Оригинал и сведения")).toBeNull();
  await choose("original");
  await waitFor(() => expect(applyButton().disabled).toBe(false));
  const request = fetcher.mock.calls.find(([url]) => String(url).endsWith("/preview"));
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    expected_revision: 3,
    choices: [{ field_id: 7, mode: "original" }],
  });
  expect(select.value).toBe("original");
});

it("reads a persisted original decision with nullable backend codec fields", async () => {
  const errors = vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    Response.json({
      ...detail,
      fields: [
        {
          ...field,
          decision_origin: "manual",
          applied_choice: {
            field_id: 7,
            mode: "original",
            encode_codec: null,
            decode_codec: null,
          },
        },
      ],
      suggestions: [
        {
          field_id: 7,
          state: "suggested",
          value: "Привет",
          choice: { field_id: 7, mode: "unicode", encode_codec: "latin-1", decode_codec: "cp1251" },
          reason: "Предложение детектора",
        },
      ],
    }),
  );
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  const select = (await screen.findByRole("combobox")) as HTMLSelectElement;
  expect(select.value).toBe("original");
  expect(errors).not.toHaveBeenCalled();
  expect(screen.queryByText("Привет")).toBeNull();
  expect(screen.queryByText("Предложение детектора")).toBeNull();
  expect(screen.queryByText("Авто")).toBeNull();
  expect(screen.queryByText("Использовать предложение")).toBeNull();
  expect(applyButton().disabled).toBe(true);
});

it("previews a field and atomically applies then reads back before reporting success", async () => {
  let applied = false;
  const refresh = vi.fn().mockResolvedValue(undefined);
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/preview")) return Response.json(preview);
    if (String(url).endsWith("/apply")) {
      applied = true;
      return Response.json({ ...preview, source_revision: 4, queued: true });
    }
    return Response.json(
      applied
        ? {
            ...detail,
            source_revision: 4,
            fields: [
              {
                ...field,
                current_value: "Привет",
                decision_origin: "manual",
                applied_choice: { field_id: 7, mode: "codec", decode_codec: "cp1251" },
              },
            ],
          }
        : detail,
    );
  });
  render(<SourceEncoding sourceId="a" onApplied={refresh} />);
  fireEvent.change(await screen.findByLabelText("Кодировка TITLE · ID3v2 · TIT2:0 · #7"), {
    target: { value: "cp1251" },
  });
  await screen.findByText("Привет");
  fireEvent.click(screen.getByRole("button", { name: "Применить кодировки" }));
  await screen.findByText(/Сохранено.*MusicBrainz/);
  expect(refresh).toHaveBeenCalledWith(true);
  const writes = fetcher.mock.calls.filter(([url]) => /\/(preview|apply)$/.test(String(url)));
  expect(writes).toHaveLength(2);
  for (const [, request] of writes) {
    expect(JSON.parse(String(request?.body))).toEqual({
      expected_revision: 3,
      choices: [{ field_id: 7, mode: "codec", decode_codec: "cp1251" }],
    });
  }
  expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("cp1251");
  expect(screen.queryByText("Авто")).toBeNull();
  expect(screen.getByText("Source rev 4")).toBeTruthy();
});

it("shows field errors and never enables atomic apply for invalid previews", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    Response.json(
      String(url).endsWith("/preview")
        ? {
            ...preview,
            valid: false,
            fields: [
              { ...preview.fields[0], value: null, status: "error", error: "invalid_bytes" },
            ],
          }
        : detail,
    ),
  );
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await choose();
  await screen.findByText("Ошибка поля: invalid_bytes");
  expect(applyButton().disabled).toBe(true);
});

it.each(["source_revision_changed", "source_processing_busy"])(
  "blocks apply on %s and permits explicit reload",
  async (conflict) => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
      if (String(url).endsWith("/apply"))
        return Response.json({ detail: conflict }, { status: 409 });
      return Response.json(String(url).endsWith("/preview") ? preview : detail);
    });
    render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
    await choose();
    await waitFor(() => expect(applyButton().disabled).toBe(false));
    fireEvent.click(applyButton());
    await screen.findByRole("alert");
    expect(applyButton().disabled).toBe(true);
    fireEvent.click(screen.getByText("Отменить выбор / обновить поля"));
    await screen.findByText("Source rev 3");
    expect(applyButton().disabled).toBe(true);
  },
);

it("ignores old preview responses after a new selection or source switch", async () => {
  let resolveOld: (response: Response) => void = () => {};
  let previews = 0;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/preview")) {
      previews++;
      if (previews === 1)
        return new Promise<Response>((resolve) => {
          resolveOld = resolve;
        });
      return Response.json({
        ...preview,
        valid: false,
        fields: [{ ...preview.fields[0], error: "new_error", status: "error", value: null }],
      });
    }
    return Response.json(
      String(url).includes("/b/")
        ? { ...detail, source_id: "b", fields: [{ ...field, current_value: "Другой источник" }] }
        : detail,
    );
  });
  const view = render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await choose();
  await waitFor(() => expect(previews).toBe(1));
  await choose("original");
  await screen.findByText("Ошибка поля: new_error");
  resolveOld(Response.json(preview));
  await waitFor(() => expect(applyButton().disabled).toBe(true));
  expect(screen.queryByText("Привет")).toBeNull();
  view.rerender(<SourceEncoding sourceId="b" onApplied={vi.fn()} />);
  await screen.findAllByText("Другой источник");
  expect(screen.queryByText("Ошибка поля: new_error")).toBeNull();
  expect(applyButton().disabled).toBe(true);
});

it("keeps legacy Unicode recovery and shadow fields selectable", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) =>
    Response.json(
      String(url).endsWith("/preview")
        ? preview
        : { ...detail, fields: [{ ...field, raw_evidence_available: false, selected: false }] },
    ),
  );
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await choose("cp1251");
  expect(screen.getAllByRole("combobox")).toHaveLength(1);
  expect(screen.getByText(/Теневые физические поля/)).toBeTruthy();
  expect(screen.queryByLabelText(/Ошибочно прочитано как/)).toBeNull();
  await waitFor(() => expect(applyButton().disabled).toBe(false));
});

it("displays structured 422 field errors returned by atomic apply", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/apply"))
      return Response.json(
        {
          detail: {
            ...preview,
            valid: false,
            fields: [
              { ...preview.fields[0], status: "error", value: null, error: "strict_decode_failed" },
            ],
          },
        },
        { status: 422 },
      );
    return Response.json(String(url).endsWith("/preview") ? preview : detail);
  });
  render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await choose();
  await waitFor(() => expect(applyButton().disabled).toBe(false));
  fireEvent.click(applyButton());
  await screen.findByText("Ошибка поля: strict_decode_failed");
  expect(applyButton().disabled).toBe(true);
});
it("discards a pending preview when switching sources", async () => {
  let resolvePreview: (response: Response) => void = () => {};
  let requested = false;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/preview")) {
      requested = true;
      return new Promise<Response>((resolve) => {
        resolvePreview = resolve;
      });
    }
    return Response.json({ ...detail, source_id: String(url).includes("/b/") ? "b" : "a" });
  });
  const view = render(<SourceEncoding sourceId="a" onApplied={vi.fn()} />);
  await choose();
  await waitFor(() => expect(requested).toBe(true));
  view.rerender(<SourceEncoding sourceId="b" onApplied={vi.fn()} />);
  await screen.findByText("Source rev 3");
  resolvePreview(Response.json(preview));
  await waitFor(() => expect(applyButton().disabled).toBe(true));
  expect(screen.queryByText("Привет")).toBeNull();
});

it("reads back revision-only saves without claiming a queued job", async () => {
  let saved = false;
  const refresh = vi.fn().mockResolvedValue(undefined);
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/preview")) return Response.json(preview);
    if (String(url).endsWith("/apply")) {
      saved = true;
      return Response.json({ ...preview, source_revision: 4, queued: false });
    }
    return Response.json({ ...detail, source_revision: saved ? 4 : 3 });
  });
  render(<SourceEncoding sourceId="a" onApplied={refresh} />);
  await choose();
  await waitFor(() => expect(applyButton().disabled).toBe(false));
  fireEvent.click(applyButton());
  await screen.findByText(/повторная обработка не нужна/);
  expect(refresh).toHaveBeenCalledWith(false);
  expect(screen.getByText("Source rev 4")).toBeTruthy();
});

it("does not claim success or retry apply when readback fails", async () => {
  let saved = false;
  const refresh = vi.fn();
  vi.spyOn(globalThis, "fetch").mockImplementation(async (url) => {
    if (String(url).endsWith("/preview")) return Response.json(preview);
    if (String(url).endsWith("/apply")) {
      saved = true;
      return Response.json({ ...preview, source_revision: 4, queued: true });
    }
    return saved ? Response.json({ detail: "offline" }, { status: 500 }) : Response.json(detail);
  });
  render(<SourceEncoding sourceId="a" onApplied={refresh} />);
  await choose();
  await waitFor(() => expect(applyButton().disabled).toBe(false));
  fireEvent.click(applyButton());
  await screen.findByText(/Изменения сохранены, но обновление данных не удалось/);
  expect(refresh).not.toHaveBeenCalled();
  expect(applyButton().disabled).toBe(true);
});
