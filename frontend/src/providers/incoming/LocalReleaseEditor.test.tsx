import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
  LocalProviderTrack,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { useLocalMatching } from "./LocalReleaseEditor";
import { localMatchingMessages } from "./localMatchingMessages";

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
      title_override: "Переименованная дорожка",
      position: 0,
      displayed_number: " ",
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

function incomingFile(
  variantID: string,
  relativePath: string,
  tags: Record<string, string[]> = {},
): IncomingFileResponse {
  return {
    variant_id: variantID,
    capture_analysis_id: `capture-${variantID}`,
    capture_identity: `identity-${variantID}`,
    tags,
    locations:
      file.locations?.map((location) => ({
        ...location,
        location_id: `location-${variantID}`,
        relative_path: relativePath,
      })) ?? [],
  };
}

function trackLabel(track: LocalProviderTrack) {
  const number = track.displayed_number.trim() || String(track.position + 1);
  return `${number}. ${track.title_override || track.recording_title}`;
}

function LocaleToggle() {
  const { locale, setPreference } = useI18n();
  return (
    <button
      type="button"
      onClick={() => setPreference(locale === "ru" ? "en" : "ru")}
    >
      Toggle locale
    </button>
  );
}

interface HarnessProps {
  group: IncomingGroupResponse | undefined;
  files: IncomingFileResponse[];
  authorityRevision: number | undefined;
  preferredReleaseId?: string;
  onConfirmed?: (receipt: MatchingLocalPreview) => void | Promise<void>;
}

/**
 * Minimal host for the headless controller. It mirrors the production widgets
 * the removed panel used to own: release summary, per-file assignment selects,
 * preview/confirm actions and localized status output.
 */
function LocalMatchingHarness({
  group,
  files,
  authorityRevision,
  preferredReleaseId,
  onConfirmed,
}: HarnessProps) {
  const { locale } = useI18n();
  const copy = localMatchingMessages[locale];
  const matching = useLocalMatching({
    group,
    files,
    authorityRevision,
    preferredReleaseId,
    onConfirmed,
  });

  function assign(fileMemberID: string, trackID: string) {
    const next: Record<string, string> = { ...matching.assignments };
    if (trackID) next[fileMemberID] = trackID;
    else delete next[fileMemberID];
    matching.setAssignments(next);
  }

  return (
    <section>
      <h2>{copy.heading}</h2>
      {matching.release && (
        <p role="status">
          {copy.releaseTrackCount(
            matching.release.title,
            matching.tracks.length,
          )}
        </p>
      )}
      {matching.release &&
        matching.files.map((member) => {
          const name =
            member.locations?.[0]?.relative_path || member.variant_id;
          return (
            <label key={member.variant_id}>
              {name}
              <select
                aria-label={copy.trackForFile(name)}
                value={matching.assignments[member.variant_id] ?? ""}
                disabled={matching.pending || matching.confirmationPending}
                onChange={(event) =>
                  assign(member.variant_id, event.target.value)
                }
              >
                <option value="">{copy.unassigned}</option>
                {matching.tracks.map((track) => (
                  <option key={track.id} value={track.id}>
                    {trackLabel(track)}
                  </option>
                ))}
              </select>
            </label>
          );
        })}
      {matching.error && <p role="alert">{matching.error}</p>}
      {matching.notice && <p role="status">{copy.notice(matching.notice)}</p>}
      {matching.stale && <p role="status">{copy.staleNotice}</p>}
      <button
        type="button"
        disabled={!matching.canPreview}
        onClick={() => void matching.preview()}
      >
        {matching.busy === "preview" ? copy.previewing : copy.previewAction}
      </button>
      {(matching.pending || matching.previewResult) && (
        <button
          type="button"
          disabled={!matching.canConfirm}
          onClick={() => void matching.confirm()}
        >
          {matching.pending ? copy.confirmRetry : copy.confirmAction}
        </button>
      )}
      <button type="button" onClick={() => void matching.read()}>
        Reread release
      </button>
      {matching.previewResult && (
        <section aria-label={copy.previewTitle}>
          <h3>{copy.previewTitle}</h3>
          <p>
            {copy.previewSummary(
              matching.previewResult.release_title,
              matching.previewResult.tracks?.length ?? 0,
            )}
          </p>
          <p>
            {copy.omittedFiles(
              matching.previewResult.omitted_file_member_ids?.length ?? 0,
            )}
          </p>
        </section>
      )}
      {matching.receipt?.local_release_id && (
        <p role="status">{copy.receipt(matching.receipt.release_title)}</p>
      )}
    </section>
  );
}

let storage: Storage;

beforeEach(() => {
  storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

it("opens the exact catalog release with no file assignments or matching writes", async () => {
  const matching = vi.fn();
  const readRelease = vi.fn(() => HttpResponse.json(release));
  server.use(
    http.get(`/api/provider-catalog/releases/${releaseID}`, readRelease),
    http.post("/api/incoming-groups/:group/matching/local/:action", () => {
      matching();
      return HttpResponse.json(matchingPreview);
    }),
  );
  render(
    <I18nProvider>
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={2}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );
  expect(
    await screen.findByRole("option", { name: "1. Переименованная дорожка" }),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Мой локальный альбом · 1 дорожка"),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Дорожка для файла album/01.flac")).toHaveValue(
    "",
  );
  expect(readRelease).toHaveBeenCalledTimes(1);
  expect(matching).not.toHaveBeenCalled();
});

it("assigns an incoming file and confirms the frozen preview with an exact retry", async () => {
  let previewBody: Record<string, unknown> | undefined;
  let releaseRequests = 0;
  let previewRequests = 0;
  const confirmBodies: Record<string, unknown>[] = [];
  let failFirstConfirmation = true;
  server.use(
    http.get(`/api/provider-catalog/releases/${releaseID}`, () => {
      releaseRequests += 1;
      return HttpResponse.json(release);
    }),
    http.post(
      `/api/incoming-groups/${group.id}/matching/local/preview`,
      async ({ request }) => {
        previewRequests += 1;
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

  const view = render(
    <I18nProvider>
      <LocaleToggle />
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={2}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );

  fireEvent.change(
    await screen.findByLabelText("Дорожка для файла album/01.flac"),
    { target: { value: trackID } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });

  fireEvent.click(screen.getByRole("button", { name: "Toggle locale" }));
  expect(
    await screen.findByRole("heading", { name: "Preview — read only" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Track for file album/01.flac")).toHaveValue(
    trackID,
  );
  expect(
    screen.getByText(/Мой локальный альбом · 0 tracks/),
  ).toBeInTheDocument();
  expect(releaseRequests).toBe(1);
  expect(previewRequests).toBe(1);
  fireEvent.click(screen.getByRole("button", { name: "Toggle locale" }));
  expect(
    await screen.findByRole("heading", {
      name: "Предпросмотр — только чтение",
    }),
  ).toBeInTheDocument();
  expect(releaseRequests).toBe(1);
  expect(previewRequests).toBe(1);

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
    /Сопоставление подтверждено для релиза Мой локальный альбом/,
  );
  expect(screen.queryByText(/library-release/)).not.toBeInTheDocument();
  expect(
    screen.getByText("Локальное сопоставление подтверждено."),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Toggle locale" }));
  expect(
    screen.getByText("Matching confirmed for release Мой локальный альбом."),
  ).toBeInTheDocument();
  expect(screen.getByText("Local matching confirmed.")).toBeInTheDocument();
  expect(screen.getByLabelText("Track for file album/01.flac")).toHaveValue(
    trackID,
  );
  expect(releaseRequests).toBe(1);
  expect(previewRequests).toBe(1);
  fireEvent.click(screen.getByRole("button", { name: "Toggle locale" }));

  expect(confirmBodies).toHaveLength(2);
  expect(confirmBodies[1]).toEqual(confirmBodies[0]);
  expect(confirmBodies[0]).toMatchObject({
    ...previewBody,
    preview_digest: matchingPreview.preview_digest,
  });
  expect(confirmBodies[0]?.request_id).toEqual(expect.any(String));

  view.unmount();
  storage.setItem("melotrove-language", "en");
  render(
    <I18nProvider>
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={2}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );
  expect(
    await screen.findByLabelText("Track for file album/01.flac"),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Match with a Local release" }),
  ).toBeInTheDocument();
});

it("orders assignment files by disc and track and previews the chosen variant", async () => {
  const shuffledFiles = [
    // Transport order (and IDs) deliberately differs from disc/track order.
    incomingFile("variant-c", "album/disc-2-track-1.flac", {
      DISCNUMBER: ["2"],
      TRACKNUMBER: ["1"],
    }),
    // A file without a track number sorts after every numbered file, even by name.
    incomingFile("variant-untagged", "album/00-untagged.flac"),
    incomingFile("variant-b", "album/disc-1-track-10.flac", {
      DISCNUMBER: ["1"],
      TRACKNUMBER: ["10"],
    }),
    incomingFile("variant-d", "album/disc-1-track-2.flac", {
      DISCNUMBER: ["1"],
      TRACKNUMBER: ["2"],
    }),
  ];
  const shuffledGroup: IncomingGroupResponse = {
    ...group,
    members: shuffledFiles.map((item) => item.variant_id),
  };
  const previewRequest = vi.fn();
  server.use(
    http.get(`/api/provider-catalog/releases/${releaseID}`, () =>
      HttpResponse.json(release),
    ),
    http.post(
      `/api/incoming-groups/${group.id}/matching/local/preview`,
      async ({ request }) => {
        previewRequest(await request.json());
        return HttpResponse.json(matchingPreview);
      },
    ),
  );
  render(
    <I18nProvider>
      <LocalMatchingHarness
        group={shuffledGroup}
        files={shuffledFiles}
        authorityRevision={2}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );
  await screen.findByLabelText("Дорожка для файла album/disc-1-track-2.flac");

  const assignmentOrder = () =>
    screen
      .getAllByLabelText(/^Дорожка для файла /)
      .map((select) => select.getAttribute("aria-label"));
  // Numeric track order, not transport order, file names, or IDs.
  expect(assignmentOrder()).toEqual([
    "Дорожка для файла album/disc-1-track-2.flac",
    "Дорожка для файла album/disc-1-track-10.flac",
    "Дорожка для файла album/disc-2-track-1.flac",
    "Дорожка для файла album/00-untagged.flac",
  ]);

  fireEvent.change(
    screen.getByLabelText("Дорожка для файла album/disc-1-track-2.flac"),
    { target: { value: trackID } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("region", { name: "Предпросмотр — только чтение" });
  expect(previewRequest).toHaveBeenCalledExactlyOnceWith({
    assignments: [{ file_member_id: "variant-d", provider_track_id: trackID }],
    expected_authority_revision: 2,
    expected_group_revision: group.revision,
    provider_release_id: releaseID,
  });
});

it("keeps the draft while invalidating a stale preview and replays an uncertain confirmation exactly after a revision change", async () => {
  let releaseRequests = 0;
  const previewBodies: Record<string, unknown>[] = [];
  const confirmBodies: Record<string, unknown>[] = [];
  let failFirstConfirmation = true;
  server.use(
    http.get(`/api/provider-catalog/releases/${releaseID}`, () => {
      releaseRequests += 1;
      return HttpResponse.json(release);
    }),
    http.post(
      `/api/incoming-groups/${group.id}/matching/local/preview`,
      async ({ request }) => {
        previewBodies.push((await request.json()) as Record<string, unknown>);
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

  const view = render(
    <I18nProvider>
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={2}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );

  fireEvent.change(
    await screen.findByLabelText("Дорожка для файла album/01.flac"),
    { target: { value: trackID } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  expect(previewBodies).toEqual([
    {
      assignments: [{ file_member_id: fileID, provider_track_id: trackID }],
      expected_authority_revision: 2,
      expected_group_revision: group.revision,
      provider_release_id: releaseID,
    },
  ]);

  // A repeated read keeps the draft but must discard the stale preview.
  fireEvent.click(screen.getByRole("button", { name: "Reread release" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Предпросмотр — только чтение" }),
    ).not.toBeInTheDocument(),
  );
  expect(
    await screen.findByLabelText("Дорожка для файла album/01.flac"),
  ).toHaveValue(trackID);
  expect(releaseRequests).toBe(2);

  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });

  // A new authority revision invalidates the preview without touching the draft.
  view.rerender(
    <I18nProvider>
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={3}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );
  await screen.findByText(
    /Снимок изменился\. После обновления данных создайте новый предпросмотр/,
  );
  expect(
    screen.queryByRole("heading", { name: "Предпросмотр — только чтение" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("Дорожка для файла album/01.flac")).toHaveValue(
    trackID,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначения" }),
  );
  await screen.findByRole("heading", { name: "Предпросмотр — только чтение" });
  expect(previewBodies.at(-1)).toMatchObject({
    assignments: [{ file_member_id: fileID, provider_track_id: trackID }],
    expected_authority_revision: 3,
  });

  // The uncertain confirmation retains its body and request id.
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить сопоставление" }),
  );
  await screen.findByText(
    /Повторите запрос, чтобы безопасно проверить тот же результат/,
  );

  // A later scope change hides the preview but must not drop the pending replay.
  view.rerender(
    <I18nProvider>
      <LocalMatchingHarness
        group={group}
        files={[file]}
        authorityRevision={4}
        preferredReleaseId={releaseID}
      />
    </I18nProvider>,
  );
  await screen.findByText(
    /Снимок изменился\. После обновления данных создайте новый предпросмотр/,
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторить тот же запрос подтверждения",
    }),
  );
  await screen.findByText(
    /Сопоставление подтверждено для релиза Мой локальный альбом/,
  );

  expect(confirmBodies).toHaveLength(2);
  expect(confirmBodies[1]).toEqual(confirmBodies[0]);
  expect(confirmBodies[0]).toMatchObject({
    assignments: [{ file_member_id: fileID, provider_track_id: trackID }],
    expected_authority_revision: 3,
    expected_group_revision: group.revision,
    preview_digest: matchingPreview.preview_digest,
    provider_release_id: releaseID,
  });
  expect(confirmBodies[0]?.request_id).toEqual(expect.any(String));
});
