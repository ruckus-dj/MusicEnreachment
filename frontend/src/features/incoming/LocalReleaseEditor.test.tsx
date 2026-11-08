import { fireEvent, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it } from "vitest";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { LocalReleaseEditor } from "./LocalReleaseEditor";

const releaseID = "local-provider-release";
const fileID = "incoming-member";
const trackID = "local-provider-track";

const group: IncomingGroupResponse = {
  id: "incoming-group",
  revision: "group-revision-1",
  manual: false,
  members: [fileID],
  diagnostics: [],
  unready_members: [],
};

const file: IncomingFileResponse = {
  variant_id: fileID,
  capture_analysis_id: "capture-analysis",
  capture_identity: "capture-identity",
  tags: {},
  locations: [
    {
      location_id: "location",
      relative_path: "album/01.flac",
      configured_path: "/incoming",
      inventory_path: "/incoming",
      mtime: "2026-01-01T00:00:00Z",
      root_id: "root",
      size_bytes: 100,
      technical_identity: "technical-identity",
      work_id: "work",
    },
  ],
};

const release: LocalProviderItem = {
  id: releaseID,
  kind: "release",
  provider_code: "local",
  provider_id: "local-provider",
  provider_key: releaseID,
  title: "Мой локальный альбом",
  credits: [],
  genres: [],
  tracks: [
    {
      id: trackID,
      provider_code: "local",
      provider_id: "local-provider",
      recording_id: "local-recording",
      recording_title: "Первая дорожка",
      title_override: null,
      position: 1,
      displayed_number: "1",
      duration_override_ms: null,
      recording_duration_ms: 180000,
    },
  ],
};

const matchingPreview: MatchingLocalPreview = {
  provider_release_id: releaseID,
  authority_revision: 2,
  group_id: group.id,
  group_revision: group.revision,
  omitted_file_member_ids: [],
  preview_digest: "preview-digest",
  release_artists: [],
  release_title: release.title ?? "",
  sources: [],
  tracks: [],
};

it("selects a local provider release, assigns an incoming file, and confirms the frozen preview", async () => {
  let previewBody: Record<string, unknown> | undefined;
  const confirmBodies: Record<string, unknown>[] = [];
  let failFirstConfirmation = true;
  server.use(
    http.get("/api/provider-catalog/releases", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("provider")).toBe("local");
      return HttpResponse.json({
        items: [release],
        limit: 100,
        offset: 0,
        total: 1,
      });
    }),
    http.get(`/api/provider-catalog/releases/${releaseID}`, () =>
      HttpResponse.json(release),
    ),
    http.post(
      `/api/incoming-groups/${group.id}/matching/local/preview`,
      async ({ request }) => {
        previewBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(matchingPreview);
      },
    ),
    http.post(
      `/api/incoming-groups/${group.id}/matching/local/confirm`,
      async ({ request }) => {
        confirmBodies.push((await request.json()) as Record<string, unknown>);
        if (failFirstConfirmation) {
          failFirstConfirmation = false;
          return HttpResponse.error();
        }
        return HttpResponse.json({
          ...matchingPreview,
          local_release_id: "library-release",
        });
      },
    ),
  );

  render(
    <LocalReleaseEditor group={group} files={[file]} authorityRevision={2} />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить локальные релизы" }),
  );
  fireEvent.change(await screen.findByLabelText("Локальный релиз"), {
    target: { value: releaseID },
  });
  fireEvent.change(
    await screen.findByLabelText("Дорожка для файла album/01.flac"),
    { target: { value: trackID } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });

  expect(previewBody).toMatchObject({
    provider_release_id: releaseID,
    expected_group_revision: group.revision,
    expected_authority_revision: 2,
    assignments: [{ file_member_id: fileID, provider_track_id: trackID }],
  });

  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить сопоставление" }),
  );
  await screen.findByText(
    /Повторите запрос, чтобы безопасно проверить тот же результат/,
  );

  expect(
    screen.getByLabelText("Дорожка для файла album/01.flac"),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  ).toBeDisabled();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторить тот же запрос подтверждения",
    }),
  );
  await screen.findByText(
    /Сопоставление подтверждено для релиза library-release/,
  );

  expect(confirmBodies).toHaveLength(2);
  expect(confirmBodies[1]).toEqual(confirmBodies[0]);
  expect(confirmBodies[0]).toMatchObject({
    ...previewBody,
    preview_digest: matchingPreview.preview_digest,
  });
  expect(confirmBodies[0]?.request_id).toEqual(expect.any(String));
});
