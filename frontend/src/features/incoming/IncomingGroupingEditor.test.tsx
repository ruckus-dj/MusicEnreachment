import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
    {
      id: "group-3",
      revision: "group-revision-3",
      manual: true,
      members: ["file-3"],
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
      locations: [
        {
          configured_path: "/music",
          inventory_path: "/music",
          location_id: "location-2",
          mtime: "2025-01-01T00:00:00Z",
          relative_path: "two.flac",
          root_id: "root-1",
          size_bytes: 1,
          technical_identity: "identity-2",
          work_id: "work-2",
        },
      ],
      tags: {},
    },
    {
      variant_id: "file-3",
      capture_analysis_id: "analysis-3",
      capture_identity: "capture-3",
      locations: [
        {
          configured_path: "/music",
          inventory_path: "/music",
          location_id: "location-3",
          mtime: "2025-01-01T00:00:00Z",
          relative_path: "three.flac",
          root_id: "root-1",
          size_bytes: 1,
          technical_identity: "identity-3",
          work_id: "work-3",
        },
      ],
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

  it("names groups by agreed album, shared folder, or relative path instead of ordinals", () => {
    const [baseOne, baseTwo, baseThree] = snapshot.files ?? [];
    const [firstGroup, secondGroup, thirdGroup] = snapshot.groups ?? [];
    const namedSnapshot: IncomingGroupsBody = {
      ...snapshot,
      groups: [
        { ...firstGroup, members: ["file-1"] },
        { ...secondGroup, members: ["file-2", "file-4"] },
        { ...thirdGroup, members: ["file-3"] },
      ],
      files: [
        {
          ...baseOne,
          tags: { " album ": [" Выход в город ", "выход в город"] },
        },
        {
          ...baseTwo,
          tags: { ALBUM: ["First title", "Second title"] },
          locations: [
            {
              configured_path: "/music",
              inventory_path: "/music",
              location_id: "location-2",
              mtime: "2025-01-01T00:00:00Z",
              relative_path: "shared/two.flac",
              root_id: "root-1",
              size_bytes: 1,
              technical_identity: "identity-2",
              work_id: "work-2",
            },
          ],
        },
        baseThree,
        {
          variant_id: "file-4",
          capture_analysis_id: "analysis-4",
          capture_identity: "capture-4",
          locations: [
            {
              configured_path: "/music",
              inventory_path: "/music",
              location_id: "location-4",
              mtime: "2025-01-01T00:00:00Z",
              relative_path: "shared/four.flac",
              root_id: "root-1",
              size_bytes: 1,
              technical_identity: "identity-4",
              work_id: "work-4",
            },
          ],
          tags: {},
        },
      ],
    };
    render(
      <I18nProvider>
        <IncomingGroupingEditor snapshot={namedSnapshot} />
      </I18nProvider>,
    );

    expect(screen.getByRole("group", { name: "Выход в город" })).toBeVisible();
    expect(screen.getByRole("group", { name: "shared" })).toBeVisible();
    expect(screen.getByRole("group", { name: "three.flac" })).toBeVisible();
    expect(
      screen.queryByRole("group", { name: /^Группа \d/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать Выход в город для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Выбрать shared для объединения" }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    expect(screen.getByText("Объединить: Выход в город, shared")).toBeVisible();
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
    expect(
      screen.getByRole("checkbox", { name: "Выбрать файл two.flac" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: /^Группа \d/ }),
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать one.flac для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать two.flac для объединения",
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
      name: "Выбрать файл one.flac",
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
    expect(screen.getByText("Merge one.flac, two.flac")).toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: /^Group \d/ }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/group-1|group-2/)).not.toBeInTheDocument();
    expect(screen.queryByText("file-1")).not.toBeInTheDocument();
    expect(previewIncomingGroups).toHaveBeenNthCalledWith(1, {
      epoch: snapshot.epoch,
      base_revision: snapshot.revision,
      actions: [{ kind: "merge", group_ids: ["group-1", "group-2"] }],
    });
    expect(
      screen.getByText("Preview is ready; confirm separately to save changes."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select file one.flac" }),
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

  it("keeps draft names after a transformed preview without changing wire identities", async () => {
    const transformedSnapshot: IncomingGroupsBody = {
      epoch: snapshot.epoch,
      revision: snapshot.revision,
      groups: [
        {
          id: "group-1-2",
          revision: "group-revision-1-2",
          manual: true,
          members: ["file-1", "file-2"],
          unready_members: [],
          diagnostics: [],
        },
        {
          id: "group-3",
          revision: "group-revision-3",
          manual: true,
          members: ["file-3"],
          unready_members: [],
          diagnostics: [],
        },
      ],
      files: snapshot.files,
    };
    const finalSnapshot: IncomingGroupsBody = {
      epoch: snapshot.epoch,
      revision: "revision-2",
      groups: [
        {
          id: "group-1-2",
          revision: "group-revision-1-2-moved",
          manual: true,
          members: ["file-2"],
          unready_members: [],
          diagnostics: [],
        },
        {
          id: "group-3",
          revision: "group-revision-3-moved",
          manual: true,
          members: ["file-3", "file-1"],
          unready_members: [],
          diagnostics: [],
        },
      ],
      files: snapshot.files,
    };
    const mergeAction = { kind: "merge", group_ids: ["group-1", "group-2"] };
    const moveAction = {
      kind: "move",
      member_ids: ["file-1"],
      target_group_id: "group-3",
    };

    vi.mocked(previewIncomingGroups)
      .mockResolvedValueOnce({
        status: 200,
        data: { ...transformedSnapshot, draft_revision: "draft-revision-1" },
        headers: new Headers(),
      })
      .mockResolvedValueOnce({
        status: 200,
        data: { ...finalSnapshot, draft_revision: "draft-revision-2" },
        headers: new Headers(),
      });
    vi.mocked(confirmIncomingGroups).mockResolvedValueOnce({
      status: 200,
      data: finalSnapshot,
      headers: new Headers(),
    });

    render(
      <I18nProvider>
        <IncomingGroupingEditor snapshot={snapshot} />
      </I18nProvider>,
    );

    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать one.flac для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Выбрать two.flac для объединения",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    await screen.findByText(
      "Предпросмотр готов; для сохранения требуется отдельное подтверждение.",
    );
    expect(screen.getByRole("group", { name: "/music" })).toBeInTheDocument();
    expect(
      screen.getByRole("group", { name: "three.flac" }),
    ).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("checkbox", { name: "Выбрать файл one.flac" }),
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Целевая группа" }), {
      target: { value: "group-3" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Переместить файлы" }));

    // The draft retains the labels as shown when each action was added.
    expect(
      screen.getByText("Объединить: one.flac, two.flac"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Переместить 1 файл в группу «three.flac»"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    await waitFor(() => {
      expect(previewIncomingGroups).toHaveBeenCalledTimes(2);
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Подтвердить изменения" }),
      ).toBeEnabled();
    });
    expect(vi.mocked(previewIncomingGroups)).toHaveBeenNthCalledWith(2, {
      epoch: snapshot.epoch,
      base_revision: snapshot.revision,
      actions: [mergeAction, moveAction],
    });

    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить изменения" }),
    );
    await waitFor(() => {
      expect(confirmIncomingGroups).toHaveBeenCalledWith({
        epoch: snapshot.epoch,
        base_revision: snapshot.revision,
        actions: [mergeAction, moveAction],
        draft_revision: "draft-revision-2",
      });
    });
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: "Действия черновика (0)" }),
      ).toBeInTheDocument();
      // File rows remain list items, so scope the draft emptiness check to the
      // draft-actions region instead of the whole document.
      const draftRegion = screen.getByRole("region", {
        name: "Действия черновика",
      });
      expect(
        within(draftRegion).queryByRole("listitem"),
      ).not.toBeInTheDocument();
    });

    // The confirmed response is rendered instead of the baseline snapshot,
    // whose groups and revision stay untouched as the request base.
    expect(snapshot.groups).toHaveLength(3);
    expect(snapshot.revision).toBe("revision-1");

    const confirmedGroups = screen.getAllByRole("group");
    expect(confirmedGroups).toHaveLength(2);
    const mergedGroup = screen.getByRole("group", { name: "two.flac" });
    const targetGroup = screen.getByRole("group", { name: "/music" });
    expect(
      within(mergedGroup).getAllByRole("checkbox", {
        name: /^Выбрать файл /,
      }),
    ).toHaveLength(1);
    expect(within(mergedGroup).getByText(/^1 файл/)).toBeInTheDocument();
    expect(
      within(targetGroup).getAllByRole("checkbox", {
        name: /^Выбрать файл /,
      }),
    ).toHaveLength(2);
    expect(within(targetGroup).getByText(/^2 файла/)).toBeInTheDocument();
    expect(
      within(targetGroup).getByRole("checkbox", {
        name: "Выбрать файл three.flac",
      }),
    ).toBeInTheDocument();
    expect(
      within(targetGroup).getByRole("checkbox", {
        name: "Выбрать файл one.flac",
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: /^Группа \d/ }),
    ).not.toBeInTheDocument();
  });
});
