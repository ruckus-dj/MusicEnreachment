import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  MatchingLocalCatalog,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { LocalReleaseEditor } from "./LocalReleaseEditor";

const group = (
  id: string,
  revision = `revision-${id}`,
): IncomingGroupResponse => ({
  id,
  revision,
  manual: false,
  members: [`${id}-file-1`, `${id}-file-2`],
  unready_members: [],
  diagnostics: [],
});

const filesFor = (id: string): IncomingFileResponse[] => [
  {
    variant_id: `${id}-file-1`,
    capture_analysis_id: "analysis-1",
    capture_identity: "capture-1",
    tags: {
      ALBUM: ["__unset__", "Artist / name"],
      ALBUMARTIST: ["Composer / performer", "Second artist"],
      ARTIST: ["Fallback artist"],
      TITLE: ["Opening", "Alternate opening"],
      TRACKNUMBER: ["4"],
      DISCNUMBER: ["2"],
    },
    locations: [],
  },
  {
    variant_id: `${id}-file-2`,
    capture_analysis_id: "analysis-2",
    capture_identity: "capture-2",
    tags: { ALBUM: ["Other album"], TITLE: ["Ending"] },
    locations: [],
  },
];

const previewResponse = (
  groupID: string,
  body: Record<string, unknown>,
): MatchingLocalPreview => ({
  group_id: groupID,
  group_revision: String(body.expected_group_revision),
  authority_revision: Number(body.expected_authority_revision),
  release_title: String(body.release_title),
  release_artists: (body.release_artists ??
    []) as MatchingLocalPreview["release_artists"],
  tracks: (body.tracks ?? []) as MatchingLocalPreview["tracks"],
  sources: [],
  omitted_file_member_ids: [],
  preview_digest: "digest-1",
});

function setup(groupData = group("group-a"), onConfirmed = vi.fn()) {
  const bodies: Record<string, unknown>[] = [];
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      async ({ request, params }) => {
        const body = (await request.json()) as Record<string, unknown>;
        bodies.push(body);
        return HttpResponse.json(previewResponse(String(params.groupId), body));
      },
    ),
    http.post(
      "*/incoming-groups/:groupId/matching/local/confirm",
      async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        bodies.push(body);
        return HttpResponse.json({
          ...previewResponse(groupData.id, body),
          local_release_id: "local-1",
        });
      },
    ),
  );
  const view = render(
    <LocalReleaseEditor
      group={groupData}
      files={filesFor(groupData.id)}
      authorityRevision={7}
      onConfirmed={onConfirmed}
    />,
  );
  return { ...view, bodies, onConfirmed };
}

function fillManualTrack() {
  fireEvent.change(screen.getByLabelText("Название релиза"), {
    target: { value: "A local album" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Добавить дорожку" }));
  fireEvent.change(screen.getByLabelText("Носитель (с 1)"), {
    target: { value: "3" },
  });
  fireEvent.change(screen.getByLabelText("Позиция (с 1)"), {
    target: { value: "8" },
  });
  fireEvent.change(screen.getByLabelText("Отображаемый номер"), {
    target: { value: "A2" },
  });
  fireEvent.change(screen.getByLabelText("Название дорожки"), {
    target: { value: "Manually ordered" },
  });
}

it("starts blank and copies source tags only after explicit selection and action", () => {
  setup();
  expect(screen.getByLabelText("Название релиза")).toHaveValue("");
  fireEvent.click(screen.getByRole("button", { name: "Добавить дорожку" }));
  expect(screen.getByLabelText("Носитель (с 1)")).toHaveValue("");
  expect(screen.getByLabelText("Позиция (с 1)")).toHaveValue("");
  expect(screen.getByLabelText("Название дорожки")).toHaveValue("");

  fireEvent.change(screen.getByLabelText("Исходный файл"), {
    target: { value: "group-a-file-1" },
  });
  fireEvent.change(screen.getByLabelText("Значение ALBUM"), {
    target: { value: "2" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Скопировать в название релиза" }),
  );
  expect(screen.getByLabelText("Название релиза")).toHaveValue("Artist / name");
  fireEvent.click(
    screen.getByRole("button", { name: "Скопировать ALBUMARTIST / ARTIST" }),
  );
  expect(screen.getByLabelText("Исполнитель релиза 1")).toHaveValue(
    "Composer / performer",
  );
  expect(screen.getByLabelText("Исполнитель релиза 2")).toHaveValue(
    "Second artist",
  );
  fireEvent.change(screen.getByLabelText(/Значение TITLE/), {
    target: { value: "2" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Скопировать TITLE/ }));
  expect(screen.getByLabelText("Название дорожки")).toHaveValue(
    "Alternate opening",
  );
  expect(screen.getByLabelText("Позиция (с 1)")).toHaveValue("4");
  expect(screen.getByLabelText("Носитель (с 1)")).toHaveValue("2");
});

it("sends explicitly ordered manual metadata and many-to-many file membership, without provider fields", async () => {
  const { bodies } = setup();
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(screen.getByLabelText("group-a-file-2"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  expect(bodies[0]).toEqual({
    expected_group_revision: "revision-group-a",
    expected_authority_revision: 7,
    release_title: "A local album",
    release_artists: [],
    tracks: [
      {
        medium: 3,
        position: 8,
        displayed_number: "A2",
        title: "Manually ordered",
        artists: [],
        file_member_ids: ["group-a-file-1", "group-a-file-2"],
      },
    ],
  });
  expect(Object.keys(bodies[0])).not.toEqual(
    expect.arrayContaining([
      "provider_release_id",
      "provider_track_id",
      "release_id",
    ]),
  );
});

it("accepts empty text metadata and preserves empty artist names", async () => {
  const { bodies } = setup();
  fireEvent.click(screen.getByRole("button", { name: "Добавить дорожку" }));
  fireEvent.change(screen.getByLabelText("Носитель (с 1)"), {
    target: { value: "1" },
  });
  fireEvent.change(screen.getByLabelText("Позиция (с 1)"), {
    target: { value: "1" },
  });
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(screen.getByRole("button", { name: "Добавить исполнителя" }));
  fireEvent.click(screen.getByRole("button", { name: "Добавить исполнителя" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({
    release_title: "",
    release_artists: [{ name: "" }, { name: "" }],
    tracks: [{ title: "", artists: [], file_member_ids: ["group-a-file-1"] }],
  });
});

it("allows a real tag value matching the old unset sentinel and keeps tag text intact", async () => {
  setup();
  fireEvent.change(screen.getByLabelText("Исходный файл"), {
    target: { value: "group-a-file-1" },
  });
  const album = screen.getByLabelText("Значение ALBUM");
  fireEvent.change(album, { target: { value: "1" } });
  fireEvent.click(
    screen.getByRole("button", { name: "Скопировать в название релиза" }),
  );
  expect(screen.getByLabelText("Название релиза")).toHaveValue("__unset__");
});

it("loads a local target only on request and submits only assigned target tracks without trusting source IDs", async () => {
  const { bodies } = setup();
  let catalogRequests = 0;
  server.use(
    http.get("*/local-releases", () => {
      catalogRequests += 1;
      const catalog: MatchingLocalCatalog = {
        releases: [
          {
            id: "local-release-1",
            title: "Existing local",
            artists: [{ name: "Release artist" }],
            tracks: [
              {
                local_track_id: "local-track-1",
                medium: 1,
                position: 1,
                displayed_number: "A1",
                title: "Saved track",
                artists: [{ name: "Track artist" }],
                source_media_ids: ["saved-media-1"],
                file_member_ids: [],
              },
              {
                local_track_id: "local-track-2",
                medium: 1,
                position: 2,
                displayed_number: "A2",
                title: "Unassigned saved track",
                artists: [],
                source_media_ids: ["saved-media-2"],
                file_member_ids: [],
              },
            ],
          },
        ],
      };
      return HttpResponse.json(catalog);
    }),
  );

  expect(catalogRequests).toBe(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить локальные релизы" }),
  );
  await screen.findByRole("option", { name: "Existing local" });
  expect(catalogRequests).toBe(1);
  expect(screen.getByLabelText("Название релиза")).toHaveValue("");

  fireEvent.change(screen.getByLabelText("Локальный релиз"), {
    target: { value: "local-release-1" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Использовать выбранный релиз" }),
  );
  expect(screen.getByLabelText("Название релиза")).toHaveValue(
    "Existing local",
  );
  expect(screen.getAllByLabelText("Носитель (с 1)")[0]).toHaveAttribute(
    "readonly",
  );
  expect(screen.getAllByLabelText("Позиция (с 1)")[0]).toHaveAttribute(
    "readonly",
  );
  expect(screen.getByText(/saved-media-1/)).toBeInTheDocument();
  expect(screen.getByText(/saved-media-2/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Исходный файл"), {
    target: { value: "group-a-file-1" },
  });
  fireEvent.change(
    screen.getByLabelText("Значение TITLE для явного копирования в дорожку"),
    {
      target: { value: "1" },
    },
  );
  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Скопировать TITLE и однозначный номер из выбранного файла",
    })[0],
  );
  expect(screen.getAllByLabelText("Носитель (с 1)")[0]).toHaveValue("1");
  expect(screen.getAllByLabelText("Позиция (с 1)")[0]).toHaveValue("1");
  fireEvent.click(screen.getAllByLabelText("group-a-file-1")[0]);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });

  expect(bodies[0]).toMatchObject({
    local_release_id: "local-release-1",
    release_title: "Existing local",
    tracks: [
      {
        local_track_id: "local-track-1",
        medium: 1,
        position: 1,
        title: "Opening",
        displayed_number: "4",
        file_member_ids: ["group-a-file-1"],
      },
    ],
  });
  expect(bodies[0].tracks).toHaveLength(1);
  expect(JSON.stringify(bodies[0])).not.toContain("source_media_ids");
  expect(JSON.stringify(bodies[0])).not.toContain("saved-media-");
});

it("keeps independent drafts for different groups", () => {
  function SwitchGroups() {
    const [active, setActive] = useState("group-a");
    return (
      <>
        <button
          type="button"
          onClick={() =>
            setActive(active === "group-a" ? "group-b" : "group-a")
          }
        >
          Switch group
        </button>
        <LocalReleaseEditor
          group={group(active)}
          files={filesFor(active)}
          authorityRevision={2}
        />
      </>
    );
  }
  render(<SwitchGroups />);
  fireEvent.change(screen.getByLabelText("Название релиза"), {
    target: { value: "Draft A" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  expect(screen.getByLabelText("Название релиза")).toHaveValue("");
  fireEvent.change(screen.getByLabelText("Название релиза"), {
    target: { value: "Draft B" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  expect(screen.getByLabelText("Название релиза")).toHaveValue("Draft A");
});

it("retries an uncertain confirmation with the exact same request identity", async () => {
  const { bodies, onConfirmed } = setup();
  let attempts = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/confirm",
      async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        bodies.push(body);
        attempts += 1;
        if (attempts === 1) return HttpResponse.error();
        return HttpResponse.json({
          ...previewResponse("group-a", body),
          local_release_id: "local-1",
        });
      },
    ),
  );
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить локальный релиз" }),
  );
  await screen.findByRole("alert");
  fireEvent.click(
    screen.getByRole("button", { name: "Повторить подтверждение" }),
  );
  await screen.findByText(/Подтверждён локальный релиз/);
  expect(bodies[1]).toEqual(bodies[2]);
  expect(bodies[1]).toMatchObject({
    preview_digest: "digest-1",
    expected_authority_revision: 7,
  });
  expect(bodies[1].request_id).toEqual(expect.any(String));
  expect(onConfirmed).toHaveBeenCalledTimes(1);
});

it("retains draft after stale preview and invalidates previews when the group revision changes", async () => {
  const bodies: Record<string, unknown>[] = [];
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      async ({ request }) => {
        bodies.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json({ detail: "stale" }, { status: 409 });
      },
    ),
  );
  const view = render(
    <LocalReleaseEditor
      group={group("group-a")}
      files={filesFor("group-a")}
      authorityRevision={undefined}
    />,
  );
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  expect(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  ).toBeDisabled();
  view.rerender(
    <LocalReleaseEditor
      group={group("group-a")}
      files={filesFor("group-a")}
      authorityRevision={7}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByText(/Снимок устарел/);
  expect(screen.getByLabelText("Название релиза")).toHaveValue("A local album");
  expect(bodies).toHaveLength(1);
  expect(bodies[0].expected_authority_revision).toBe(7);
  await waitFor(() => expect(screen.getByRole("status")).toBeInTheDocument());
});

it("does not let an old in-flight preview attach to a different group", async () => {
  let resolvePreview: ((response: Response) => void) | undefined;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      () =>
        new Promise((resolve) => {
          resolvePreview = resolve;
        }),
    ),
  );
  function SwitchGroups() {
    const [active, setActive] = useState("group-a");
    return (
      <>
        <button type="button" onClick={() => setActive("group-b")}>
          Switch group
        </button>
        <LocalReleaseEditor
          group={group(active)}
          files={filesFor(active)}
          authorityRevision={7}
        />
      </>
    );
  }
  render(<SwitchGroups />);
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await waitFor(() => expect(resolvePreview).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  resolvePreview?.(
    HttpResponse.json(
      previewResponse("group-a", {
        expected_group_revision: "revision-group-a",
        expected_authority_revision: 7,
        release_title: "A local album",
        release_artists: [],
        tracks: [],
      }),
    ) as Response,
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Предпросмотр — только чтение" }),
    ).not.toBeInTheDocument(),
  );
});

it("locks edits during confirmation and settles its receipt after navigating groups", async () => {
  let resolveConfirm: ((response: Response) => void) | undefined;
  const onConfirmed = vi.fn();
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      async ({ request, params }) => {
        const body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(previewResponse(String(params.groupId), body));
      },
    ),
    http.post(
      "*/incoming-groups/:groupId/matching/local/confirm",
      () =>
        new Promise((resolve) => {
          resolveConfirm = resolve;
        }),
    ),
  );
  function SwitchGroups() {
    const [active, setActive] = useState("group-a");
    return (
      <>
        <button
          type="button"
          onClick={() =>
            setActive(active === "group-a" ? "group-b" : "group-a")
          }
        >
          Switch group
        </button>
        <LocalReleaseEditor
          group={group(active)}
          files={filesFor(active)}
          authorityRevision={7}
          onConfirmed={onConfirmed}
        />
      </>
    );
  }
  render(<SwitchGroups />);
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить локальный релиз" }),
  );
  await waitFor(() => expect(resolveConfirm).toBeDefined());
  expect(screen.getByLabelText("Название релиза")).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Новый локальный релиз" }),
  ).toBeDisabled();

  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  resolveConfirm?.(
    HttpResponse.json({
      ...previewResponse("group-a", {
        expected_group_revision: "revision-group-a",
        expected_authority_revision: 7,
        release_title: "A local album",
        release_artists: [],
        tracks: [],
      }),
      local_release_id: "local-1",
    }) as Response,
  );
  await waitFor(() => expect(onConfirmed).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  expect(
    await screen.findByText(/Подтверждён локальный релиз local-1/),
  ).toBeInTheDocument();
});

it("keeps a deferred confirmation pending across group navigation and replays the same request after an ambiguous failure", async () => {
  let resolveConfirm: ((response: Response) => void) | undefined;
  const bodies: Record<string, unknown>[] = [];
  const onConfirmed = vi.fn();
  let attempts = 0;
  server.use(
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      async ({ request, params }) => {
        const body = (await request.json()) as Record<string, unknown>;
        bodies.push(body);
        return HttpResponse.json(previewResponse(String(params.groupId), body));
      },
    ),
    http.post(
      "*/incoming-groups/:groupId/matching/local/confirm",
      async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        bodies.push(body);
        attempts += 1;
        if (attempts === 1) {
          return new Promise((resolve) => {
            resolveConfirm = resolve;
          });
        }
        return HttpResponse.json({
          ...previewResponse("group-a", body),
          local_release_id: "local-1",
        });
      },
    ),
  );
  function SwitchGroups() {
    const [active, setActive] = useState("group-a");
    const [authority, setAuthority] = useState(7);
    return (
      <>
        <button type="button" onClick={() => setAuthority(8)}>
          Refresh authority
        </button>
        <button
          type="button"
          onClick={() =>
            setActive(active === "group-a" ? "group-b" : "group-a")
          }
        >
          Switch group
        </button>
        <LocalReleaseEditor
          group={group(active)}
          files={filesFor(active)}
          authorityRevision={authority}
          onConfirmed={onConfirmed}
        />
      </>
    );
  }
  render(<SwitchGroups />);
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить локальный релиз" }),
  );
  await waitFor(() => expect(resolveConfirm).toBeDefined());
  expect(screen.getByLabelText("Название релиза")).toBeDisabled();

  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  expect(screen.getByLabelText("Название релиза")).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Повторить подтверждение" }),
  ).toBeDisabled();

  resolveConfirm?.(HttpResponse.error() as Response);
  await screen.findByRole("alert");
  expect(screen.getByLabelText("Название релиза")).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh authority" }));
  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторить тот же запрос подтверждения",
    }),
  );
  await screen.findByText(/Подтверждён локальный релиз local-1/);
  expect(bodies[2]).toEqual(bodies[1]);
  expect(bodies[1].request_id).toEqual(bodies[2].request_id);
  expect(onConfirmed).toHaveBeenCalledTimes(1);
});

it("clears the originating catalog busy state after switching groups", async () => {
  let resolveCatalog: ((response: Response) => void) | undefined;
  server.use(
    http.get(
      "*/local-releases",
      () =>
        new Promise((resolve) => {
          resolveCatalog = resolve;
        }),
    ),
  );
  function SwitchGroups() {
    const [active, setActive] = useState("group-a");
    return (
      <>
        <button
          type="button"
          onClick={() =>
            setActive(active === "group-a" ? "group-b" : "group-a")
          }
        >
          Switch group
        </button>
        <LocalReleaseEditor
          group={group(active)}
          files={filesFor(active)}
          authorityRevision={7}
        />
      </>
    );
  }
  render(<SwitchGroups />);
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить локальные релизы" }),
  );
  await waitFor(() => expect(resolveCatalog).toBeDefined());
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  resolveCatalog?.(HttpResponse.json({ releases: [] }) as Response);
  fireEvent.click(screen.getByRole("button", { name: "Switch group" }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Загрузить локальные релизы" }),
    ).toBeEnabled(),
  );
});

it("settles an old target preview when another target starts previewing", async () => {
  let resolveFirstPreview: ((response: Response) => void) | undefined;
  let previewRequests = 0;
  server.use(
    http.get("*/local-releases", () =>
      HttpResponse.json({
        releases: [
          {
            id: "local-a",
            title: "Target A",
            artists: [],
            tracks: [
              {
                local_track_id: "track-a",
                medium: 1,
                position: 1,
                displayed_number: "1",
                title: "Track A",
                artists: [],
                source_media_ids: [],
                file_member_ids: [],
              },
            ],
          },
        ],
      }),
    ),
    http.post(
      "*/incoming-groups/:groupId/matching/local/preview",
      async ({ request, params }) => {
        previewRequests += 1;
        const body = (await request.json()) as Record<string, unknown>;
        const response = HttpResponse.json(
          previewResponse(String(params.groupId), body),
        );
        if (previewRequests === 1) {
          return new Promise((resolve) => {
            resolveFirstPreview = resolve;
          });
        }
        return response;
      },
    ),
  );
  render(
    <LocalReleaseEditor
      group={group("group-a")}
      files={filesFor("group-a")}
      authorityRevision={7}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить локальные релизы" }),
  );
  await screen.findByRole("option", { name: "Target A" });
  fireEvent.change(screen.getByLabelText("Локальный релиз"), {
    target: { value: "local-a" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Использовать выбранный релиз" }),
  );
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await waitFor(() => expect(resolveFirstPreview).toBeDefined());

  fireEvent.click(
    screen.getByRole("button", { name: "Новый локальный релиз" }),
  );
  fillManualTrack();
  fireEvent.click(screen.getByLabelText("group-a-file-1"));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  resolveFirstPreview?.(
    HttpResponse.json(
      previewResponse("group-a", {
        expected_group_revision: "revision-group-a",
        expected_authority_revision: 7,
        local_release_id: "local-a",
        release_title: "Target A",
        release_artists: [],
        tracks: [],
      }),
    ) as Response,
  );

  fireEvent.change(screen.getByLabelText("Локальный релиз"), {
    target: { value: "local-a" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Использовать выбранный релиз" }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Предпросмотр локального релиза" }),
    ).toBeEnabled(),
  );
});
