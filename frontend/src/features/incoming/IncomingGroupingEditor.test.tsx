import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IncomingGroupsBody } from "../../api/generated/client.schemas";
import { IncomingGroupingEditor } from "./IncomingGroupingEditor";

const api = vi.hoisted(() => ({
  confirmIncomingGroups: vi.fn(),
  getIncomingGroups: vi.fn(),
  previewIncomingGroups: vi.fn(),
}));

vi.mock("../../api/generated/client", () => api);

const initial: IncomingGroupsBody = {
  epoch: 4,
  revision: "base-revision",
  groups: [
    {
      id: "group-a",
      revision: "a",
      manual: false,
      members: ["file-1", "file-2"],
      unready_members: ["file-2"],
      diagnostics: [],
    },
    {
      id: "group-b",
      revision: "b",
      manual: false,
      members: ["file-3"],
      unready_members: [],
      diagnostics: [],
    },
  ],
  files: ["file-1", "file-2", "file-3"].map((variant_id) => ({
    variant_id,
    capture_analysis_id: `analysis-${variant_id}`,
    capture_identity: `identity-${variant_id}`,
    tags: {},
    locations: [
      {
        location_id: `location-${variant_id}`,
        relative_path: `folder/${variant_id}.flac`,
        configured_path: "/incoming",
        inventory_path: "/incoming",
        root_id: "root",
        size_bytes: 20,
        mtime: "now",
        technical_identity: `tech-${variant_id}`,
        work_id: `work-${variant_id}`,
      },
    ],
  })),
};

const previewed: IncomingGroupsBody = {
  ...initial,
  draft_revision: "draft-revision",
};

const merged: IncomingGroupsBody = {
  ...initial,
  draft_revision: "merged-draft",
  groups: [
    {
      id: "replacement-merge",
      revision: "merged",
      manual: false,
      members: ["file-1", "file-2", "file-3"],
      unready_members: ["file-2"],
      diagnostics: [],
    },
  ],
};

const split: IncomingGroupsBody = {
  ...merged,
  draft_revision: "split-draft",
  groups: [
    {
      id: "replacement-split",
      revision: "split",
      manual: false,
      members: ["file-1"],
      unready_members: [],
      diagnostics: [],
    },
    {
      id: "replacement-remainder",
      revision: "remainder",
      manual: false,
      members: ["file-2", "file-3"],
      unready_members: ["file-2"],
      diagnostics: [],
    },
  ],
};

function ok(data: IncomingGroupsBody) {
  return { status: 200, data, headers: new Headers() };
}

describe("IncomingGroupingEditor", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.previewIncomingGroups.mockResolvedValue(ok(previewed));
    api.confirmIncomingGroups.mockResolvedValue(ok(initial));
    api.getIncomingGroups.mockResolvedValue(ok(initial));
  });

  it("previews replacement IDs between cumulative actions before separately confirming", async () => {
    api.previewIncomingGroups
      .mockResolvedValueOnce(ok(merged))
      .mockResolvedValueOnce(ok(split));
    render(<IncomingGroupingEditor snapshot={initial} />);
    const groups = screen.getAllByRole("group");
    fireEvent.click(
      within(groups[0]).getByRole("checkbox", { name: /объединения/i }),
    );
    fireEvent.click(
      within(groups[1]).getByRole("checkbox", { name: /объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    await waitFor(() =>
      expect(api.previewIncomingGroups).toHaveBeenCalledTimes(1),
    );
    expect(
      await screen.findByRole("option", { name: "replacement-merge" }),
    ).toBeTruthy();

    fireEvent.click(
      screen.getByRole("checkbox", { name: "Выбрать файл file-1" }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Отделить выбранные файлы" }),
    );
    expect(
      screen.getByRole("checkbox", { name: "Выбрать файл file-1" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    await waitFor(() =>
      expect(api.previewIncomingGroups).toHaveBeenCalledTimes(2),
    );
    expect(
      await screen.findByRole("option", { name: "replacement-split" }),
    ).toBeTruthy();
    expect(api.previewIncomingGroups).toHaveBeenNthCalledWith(1, {
      epoch: 4,
      base_revision: "base-revision",
      actions: [{ kind: "merge", group_ids: ["group-a", "group-b"] }],
    });
    expect(api.previewIncomingGroups).toHaveBeenNthCalledWith(2, {
      epoch: 4,
      base_revision: "base-revision",
      actions: [
        { kind: "merge", group_ids: ["group-a", "group-b"] },
        {
          kind: "split",
          group_id: "replacement-merge",
          member_ids: ["file-1"],
        },
      ],
    });
    expect(api.confirmIncomingGroups).not.toHaveBeenCalled();

    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить изменения" }),
    );
    await waitFor(() =>
      expect(api.confirmIncomingGroups).toHaveBeenCalledWith({
        epoch: 4,
        base_revision: "base-revision",
        draft_revision: "split-draft",
        actions: [
          { kind: "merge", group_ids: ["group-a", "group-b"] },
          {
            kind: "split",
            group_id: "replacement-merge",
            member_ids: ["file-1"],
          },
        ],
      }),
    );
  });

  it("cancels page actions without calling the API", () => {
    render(<IncomingGroupingEditor snapshot={initial} />);
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 1.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 2.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Отменить черновик" }));
    expect(screen.getByText("Действия черновика (0)")).toBeTruthy();
    expect(api.previewIncomingGroups).not.toHaveBeenCalled();
    expect(api.confirmIncomingGroups).not.toHaveBeenCalled();
  });

  it("retains actions on a stale preview and blocks confirm until snapshot refresh and review", async () => {
    api.previewIncomingGroups.mockResolvedValueOnce({
      status: 409,
      data: { detail: "Снимок устарел" },
      headers: new Headers(),
    });
    render(<IncomingGroupingEditor snapshot={initial} />);
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 1.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 2.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Снимок устарел",
    );
    expect(screen.getByText("Действия черновика (1)")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Подтвердить изменения" }),
    ).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Обновить снимок" }));
    await waitFor(() => expect(api.getIncomingGroups).toHaveBeenCalled());
    expect(screen.getByText("Действия черновика (1)")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Подтвердить изменения" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    await waitFor(() =>
      expect(api.previewIncomingGroups).toHaveBeenCalledTimes(2),
    );
    expect(
      screen.getByRole("button", { name: "Подтвердить изменения" }),
    ).toBeEnabled();
  });

  it("ignores an older preview response after the server snapshot changes", async () => {
    let resolvePreview!: (value: ReturnType<typeof ok>) => void;
    api.previewIncomingGroups.mockReturnValueOnce(
      new Promise((resolve) => {
        resolvePreview = resolve;
      }),
    );
    const { rerender } = render(<IncomingGroupingEditor snapshot={initial} />);
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 1.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: /Группа 2.*объединения/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Объединить выбранные группы" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Предпросмотр" }));
    rerender(
      <IncomingGroupingEditor
        snapshot={{ ...initial, epoch: 5, revision: "new-revision" }}
      />,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Предпросмотр" }),
      ).toBeEnabled(),
    );
    expect(
      screen.getByText(
        "Создайте успешный предпросмотр, прежде чем добавлять следующее действие.",
      ),
    ).toBeTruthy();
    resolvePreview(ok(previewed));
    await waitFor(() =>
      expect(screen.getByText("Действия черновика (1)")).toBeTruthy(),
    );
    expect(screen.queryByText(/Предпросмотр готов/)).toBeNull();
  });
});
