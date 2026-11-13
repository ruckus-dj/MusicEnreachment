import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  confirmIncomingGroups,
  getIncomingGroups,
  previewIncomingGroups,
} from "../../api/generated/client";
import type { IncomingGroupsBody } from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { IncomingGroupingEditor } from "./IncomingGroupingEditor";

vi.mock("../../api/generated/client", () => ({
  confirmIncomingGroups: vi.fn(),
  getIncomingGroups: vi.fn(),
  previewIncomingGroups: vi.fn(),
}));

const snapshot: IncomingGroupsBody = {
  epoch: 1,
  revision: "revision-1",
  groups: [
    {
      id: "group-1",
      revision: "group-revision-1",
      manual: true,
      members: ["file-1"],
      unready_members: [],
      diagnostics: [],
    },
    {
      id: "group-2",
      revision: "group-revision-2",
      manual: true,
      members: ["file-2"],
      unready_members: [],
      diagnostics: [],
    },
  ],
  files: [
    {
      variant_id: "file-1",
      capture_analysis_id: "analysis-1",
      capture_identity: "capture-1",
      locations: [
        {
          configured_path: "/music",
          inventory_path: "/music",
          location_id: "location-1",
          mtime: "2025-01-01T00:00:00Z",
          relative_path: "one.flac",
          root_id: "root-1",
          size_bytes: 1,
          technical_identity: "identity-1",
          work_id: "work-1",
        },
      ],
      tags: {},
    },
    {
      variant_id: "file-2",
      capture_analysis_id: "analysis-2",
      capture_identity: "capture-2",
      locations: [],
      tags: {},
    },
  ],
};

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

describe("IncomingGroupingEditor locale presentation", () => {
  beforeEach(() => {
    const storage = createMemoryStorage();
    storage.setItem("melotrove-language", "ru");
    vi.stubGlobal("localStorage", storage);
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps the draft and preview across locale changes and presents errors appropriately", async () => {
    vi.mocked(previewIncomingGroups)
      .mockResolvedValueOnce({
        status: 500,
        data: {},
        headers: new Headers(),
      })
      .mockResolvedValueOnce({
        status: 200,
        data: { ...snapshot, draft_revision: "draft-revision-1" },
        headers: new Headers(),
      })
      .mockResolvedValueOnce({
        status: 409,
        data: { detail: "incoming groups changed" },
        headers: new Headers(),
      });

    render(
      <I18nProvider>
        <LocaleToggle />
        <IncomingGroupingEditor snapshot={snapshot} />
      </I18nProvider>,
    );

    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать Группа 1 для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать Группа 2 для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Предпросмотр не выполнен (HTTP 500).",
    );

    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    expect(
      await screen.findByText(
        "Предпросмотр готов; для сохранения требуется отдельное подтверждение.",
      ),
    ).toBeInTheDocument();

    const memberCheckbox = screen.getByRole("checkbox", {
      name: "Выбрать файл file-1",
    });
    fireEvent.click(memberCheckbox);
    expect(memberCheckbox).toBeChecked();

    const previewCallCount = vi.mocked(previewIncomingGroups).mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));

    expect(
      screen.getByRole("heading", { name: "Correct incoming groups" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Draft actions (1)" }),
    ).toBeInTheDocument();
    expect(screen.getByText("merge: group-1, group-2")).toBeInTheDocument();
    expect(
      screen.getByText("Preview is ready; confirm separately to save changes."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select file file-1" }),
    ).toBeChecked();
    expect(vi.mocked(previewIncomingGroups).mock.calls).toHaveLength(
      previewCallCount,
    );
    expect(vi.mocked(confirmIncomingGroups)).not.toHaveBeenCalled();
    expect(vi.mocked(getIncomingGroups)).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "incoming groups changed",
    );
    fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent(
        "incoming groups changed",
      );
    });
    expect(vi.mocked(previewIncomingGroups).mock.calls).toHaveLength(
      previewCallCount + 1,
    );
  });
});
