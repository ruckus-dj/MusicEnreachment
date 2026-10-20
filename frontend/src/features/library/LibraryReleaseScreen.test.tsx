import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { LibraryReleaseScreen } from "./LibraryReleaseScreen";

afterEach(() => vi.unstubAllGlobals());

it("shows read-back tags and submits revision-fenced primary artist selections", async () => {
  const primaryBodies: unknown[] = [];
  let selectedArtist = "";
  let positionRevision = 4;
  let releaseRevision = 7;
  server.use(
    http.get("*/api/library/releases/credits", () =>
      HttpResponse.json({
        id: "credits",
        title: "Album",
        metadata: {},
        metadata_revision: releaseRevision,
        metadata_version: 1,
        primary_artist: { selected_artist_id: selectedArtist },
        artists: [
          { artist_id: "artist-a", credit_name: "Artist A" },
          { artist_id: "artist-b", credit_name: "Artist B" },
          { artist_id: "artist-a", credit_name: "Duplicate Artist A" },
        ],
        tracks: [
          {
            id: "position-a",
            local_track_id: "track-a",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 4,
            metadata_version: 1,
            track_metadata_revision: 3,
            primary_artist: { selected_artist_id: selectedArtist },
            recording_id: "recording",
            artists: [
              { artist_id: "artist-a", credit_name: "Artist A" },
              { artist_id: "artist-b", credit_name: "Artist B" },
              { artist_id: "artist-a", credit_name: "Duplicate Artist A" },
            ],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/credits/positions/position-a/metadata",
      () =>
        HttpResponse.json({
          revision: positionRevision,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/credits/positions/position-a/publication",
      () =>
        HttpResponse.json({
          release_id: "credits",
          position_id: "position-a",
          active_epoch: "epoch",
          metadata_revision: 4,
          can_apply_manual_lyrics: false,
          current_metadata: {
            version: 1,
            tags: { ARTIST: ["Read back artist"], TITLE: ["Read back song"] },
          },
        }),
    ),
    http.put(
      "*/api/library/releases/credits/positions/position-a/primary-artist",
      async ({ request }) => {
        primaryBodies.push(await request.json());
        selectedArtist = "artist-b";
        positionRevision += 1;
        return HttpResponse.json({
          revision: positionRevision,
          publication_applied: false,
        });
      },
    ),
    http.put(
      "*/api/library/releases/credits/primary-artist",
      async ({ request }) => {
        const body = (await request.json()) as { artist_id: string };
        selectedArtist = body.artist_id;
        releaseRevision += 1;
        return HttpResponse.json({
          revision: releaseRevision,
          publication_applied: false,
        });
      },
    ),
  );
  render(<LibraryReleaseScreen releaseId="credits" />);
  expect(await screen.findByText(/ARTIST: Read back artist/)).toBeTruthy();
  const positionSelect = screen.getByLabelText(
    "Основной исполнитель композиции 1",
  );
  expect(positionSelect).toHaveValue("");
  expect(positionSelect.querySelectorAll("option")).toHaveLength(3);
  fireEvent.change(positionSelect, {
    target: { value: "artist-b" },
  });
  await waitFor(() =>
    expect(primaryBodies).toEqual([
      {
        artist_id: "artist-b",
        expected_revision: 4,
        expected_track_revision: 3,
      },
    ]),
  );
  await waitFor(() => expect(positionSelect).toHaveValue("artist-b"));
  fireEvent.change(screen.getByLabelText("Основной исполнитель релиза"), {
    target: { value: "artist-a" },
  });
  await waitFor(() =>
    expect(screen.getByLabelText("Основной исполнитель релиза")).toHaveValue(
      "artist-a",
    ),
  );
});

it("ignores a pending primary-artist mutation after navigating to another release", async () => {
  let finishMutation: (() => void) | undefined;
  let releaseAReads = 0;
  server.use(
    http.get("*/api/library/releases/:releaseId", ({ params }) => {
      if (params.releaseId === "release-a") releaseAReads += 1;
      return HttpResponse.json({
        id: params.releaseId,
        title: String(params.releaseId),
        metadata: {},
        metadata_revision: 2,
        metadata_version: 1,
        primary_artist: { selected_artist_id: "artist-a" },
        artists: [
          { artist_id: "artist-a", credit_name: "Artist A" },
          { artist_id: "artist-b", credit_name: "Artist B" },
        ],
        tracks: [],
      });
    }),
    http.put(
      "*/api/library/releases/release-a/primary-artist",
      () =>
        new Promise((resolve) => {
          finishMutation = () =>
            resolve(
              HttpResponse.json({ revision: 3, publication_applied: false }),
            );
        }),
    ),
  );
  const view = render(<LibraryReleaseScreen releaseId="release-a" />);
  fireEvent.change(
    await screen.findByLabelText("Основной исполнитель релиза"),
    { target: { value: "artist-b" } },
  );
  await waitFor(() => expect(finishMutation).toBeDefined());
  view.rerender(<LibraryReleaseScreen releaseId="release-b" />);
  expect(
    await screen.findByRole("heading", { name: "release-b" }),
  ).toBeTruthy();
  const readCountAtNavigation = releaseAReads;
  await act(async () => finishMutation?.());
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(releaseAReads).toBe(readCountAtNavigation);
  expect(screen.getByRole("heading", { name: "release-b" })).toBeTruthy();
});

it("keeps the second release selection pending when an earlier A request finishes after A-B-A", async () => {
  const finishers: Array<() => void> = [];
  server.use(
    http.get("*/api/library/releases/:releaseId", ({ params }) =>
      HttpResponse.json({
        id: params.releaseId,
        title: String(params.releaseId),
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: { selected_artist_id: "artist-a" },
        artists: [
          { artist_id: "artist-a", credit_name: "Artist A" },
          { artist_id: "artist-b", credit_name: "Artist B" },
        ],
        tracks: [],
      }),
    ),
    http.put(
      "*/api/library/releases/release-a/primary-artist",
      () =>
        new Promise((resolve) => {
          finishers.push(() =>
            resolve(
              HttpResponse.json({ revision: 2, publication_applied: false }),
            ),
          );
        }),
    ),
  );
  const view = render(<LibraryReleaseScreen releaseId="release-a" />);
  fireEvent.change(
    await screen.findByLabelText("Основной исполнитель релиза"),
    {
      target: { value: "artist-b" },
    },
  );
  await waitFor(() => expect(finishers).toHaveLength(1));
  view.rerender(<LibraryReleaseScreen releaseId="release-b" />);
  await screen.findByRole("heading", { name: "release-b" });
  view.rerender(<LibraryReleaseScreen releaseId="release-a" />);
  const selector = await screen.findByLabelText("Основной исполнитель релиза");
  await waitFor(() => expect(selector).not.toBeDisabled());
  fireEvent.change(selector, { target: { value: "artist-b" } });
  await waitFor(() => expect(finishers).toHaveLength(2));
  expect(selector).toBeDisabled();
  await act(async () => finishers[0]());
  expect(selector).toBeDisabled();
  await act(async () => finishers[1]());
  await waitFor(() => expect(selector).not.toBeDisabled());
});

it("retains the release screen and lyrics draft when release primary artist selection fails", async () => {
  server.use(
    http.get("*/api/library/releases/release-artist-failure", () =>
      HttpResponse.json({
        id: "release-artist-failure",
        title: "Album",
        metadata: {},
        metadata_revision: 2,
        metadata_version: 1,
        primary_artist: { selected_artist_id: "artist-a" },
        artists: [
          { artist_id: "artist-a", credit_name: "Artist A" },
          { artist_id: "artist-b", credit_name: "Artist B" },
        ],
        tracks: [
          {
            id: "position-artist-failure",
            local_track_id: "track-artist-failure",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 4,
            metadata_version: 1,
            track_metadata_revision: 3,
            primary_artist: { selected_artist_id: "artist-a" },
            recording_id: "recording-artist-failure",
            artists: [{ artist_id: "artist-a", credit_name: "Artist A" }],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-artist-failure/positions/position-artist-failure/metadata",
      () =>
        HttpResponse.json({
          revision: 4,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-artist-failure/positions/position-artist-failure/publication",
      () =>
        HttpResponse.json({
          release_id: "release-artist-failure",
          position_id: "position-artist-failure",
          metadata_revision: 4,
          can_apply_manual_lyrics: false,
        }),
    ),
    http.put(
      "*/api/library/releases/release-artist-failure/primary-artist",
      () =>
        HttpResponse.json(
          { detail: "library metadata revision is stale" },
          { status: 409 },
        ),
    ),
  );

  render(<LibraryReleaseScreen releaseId="release-artist-failure" />);
  const editor = await screen.findByLabelText("Сохранённый текст песни");
  fireEvent.change(editor, { target: { value: "Unsaved draft" } });
  const selector = screen.getByLabelText("Основной исполнитель релиза");
  expect(selector).toHaveValue("artist-a");
  fireEvent.change(selector, { target: { value: "artist-b" } });

  expect(
    await screen.findByText("library metadata revision is stale"),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Медиатека" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Album" })).toBeInTheDocument();
  expect(screen.getByLabelText("Основной исполнитель релиза")).toHaveValue(
    "artist-a",
  );
  expect(screen.getByLabelText("Основной исполнитель релиза")).toBeEnabled();
  expect(screen.getByLabelText("Сохранённый текст песни")).toHaveValue(
    "Unsaved draft",
  );
});

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  listeners = new Map<string, Array<() => void>>();
  closed = false;
  constructor() {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: EventListener) {
    this.listeners.set(type, [
      ...(this.listeners.get(type) ?? []),
      listener as () => void,
    ]);
  }
  removeEventListener() {}
  close() {
    this.closed = true;
  }
  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }
}

it("saves explicit manual lyrics before applying with the current publication revisions", async () => {
  const patchBodies: unknown[] = [];
  const applyBodies: unknown[] = [];
  let savedLyrics: Record<string, unknown> | undefined;
  let metadataRevision = 4;
  server.use(
    http.get("*/api/library/releases/release-1", () =>
      HttpResponse.json({
        id: "release-1",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-1",
            local_track_id: "track-1",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 3,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-1",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-1/positions/position-1/metadata",
      () =>
        HttpResponse.json({
          revision: metadataRevision,
          publication_applied: false,
          envelope: {
            scope: "position",
            version: 1,
            desired: savedLyrics ? { lyrics: savedLyrics } : {},
          },
        }),
    ),
    http.get(
      "*/api/library/releases/release-1/positions/position-1/publication",
      () =>
        HttpResponse.json({
          release_id: "release-1",
          position_id: "position-1",
          active_epoch: "epoch-1",
          current_epoch: "epoch-1",
          current_generation: "generation-1",
          metadata_revision: metadataRevision,
          can_apply_manual_lyrics: true,
          current_path: "/music/song.flac",
          manual_lyrics: { pending: false, state: "missing" },
        }),
    ),
    http.patch(
      "*/api/library/releases/release-1/positions/position-1/metadata",
      async ({ request }) => {
        patchBodies.push(await request.json());
        const body = patchBodies.at(-1) as {
          changes: { lyrics: Record<string, unknown> };
        };
        savedLyrics = body.changes.lyrics;
        metadataRevision += 1;
        return HttpResponse.json({
          revision: metadataRevision,
          publication_applied: false,
          envelope: {
            scope: "position",
            version: 1,
            desired: { lyrics: savedLyrics },
          },
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-1/positions/position-1/publication/manual-lyrics",
      async ({ request }) => {
        applyBodies.push(await request.json());
        return HttpResponse.json({ id: "operation-1" }, { status: 202 });
      },
    ),
    http.get("*/api/operations/operation-1", () =>
      HttpResponse.json({
        id: "operation-1",
        kind: "publication",
        state: "running",
        stage: "writing",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
  );
  vi.stubGlobal("EventSource", FakeEventSource);

  render(<LibraryReleaseScreen releaseId="release-1" />);
  const editor = await screen.findByLabelText("Сохранённый текст песни");
  fireEvent.change(editor, { target: { value: "Words of the song" } });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить текст" }));
  await screen.findByText(
    "Текст сохранён. Примените его отдельно к текущей публикации.",
  );
  expect(patchBodies[0]).toEqual({
    expected_revision: 4,
    changes: {
      lyrics: {
        authority: "manual",
        provenance: { kind: "manual" },
        state: "value",
        value: "Words of the song",
      },
    },
  });

  fireEvent.click(
    screen.getByRole("button", { name: "Применить сохранённый текст" }),
  );
  await waitFor(() => expect(applyBodies).toHaveLength(1));
  expect(applyBodies[0]).toEqual({
    expected_epoch: "epoch-1",
    expected_generation: "generation-1",
    expected_metadata_revision: 5,
  });
});

it("preserves lyrics edited while the save request is pending", async () => {
  let finishPatch: ((response: Response) => void) | undefined;
  let patchStarted: (() => void) | undefined;
  const started = new Promise<void>((resolve) => {
    patchStarted = resolve;
  });
  server.use(
    http.get("*/api/library/releases/release-edit", () =>
      HttpResponse.json({
        id: "release-edit",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-edit",
            local_track_id: "track-edit",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 2,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-edit",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-edit/positions/position-edit/metadata",
      () =>
        HttpResponse.json({
          revision: 1,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-edit/positions/position-edit/publication",
      () =>
        HttpResponse.json({
          release_id: "release-edit",
          position_id: "position-edit",
          active_epoch: "epoch-edit",
          metadata_revision: 1,
          can_apply_manual_lyrics: false,
        }),
    ),
    http.patch(
      "*/api/library/releases/release-edit/positions/position-edit/metadata",
      async () => {
        patchStarted?.();
        return await new Promise<Response>((resolve) => {
          finishPatch = resolve;
        });
      },
    ),
  );

  render(<LibraryReleaseScreen releaseId="release-edit" />);
  const editor = await screen.findByLabelText("Сохранённый текст песни");
  fireEvent.change(editor, { target: { value: "Submitted version" } });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить текст" }));
  await started;
  fireEvent.change(editor, { target: { value: "Newer unsaved edit" } });
  const resolvePatch = finishPatch;
  if (!resolvePatch) throw new Error("PATCH handler did not start");
  resolvePatch(
    HttpResponse.json({
      revision: 2,
      publication_applied: false,
      envelope: {
        scope: "position",
        version: 1,
        desired: {
          lyrics: {
            authority: "manual",
            provenance: { kind: "manual" },
            state: "value",
            value: "Submitted version",
          },
        },
      },
    }),
  );
  await screen.findByText(
    "Текст сохранён. Примените его отдельно к текущей публикации.",
  );
  expect(screen.getByLabelText("Сохранённый текст песни")).toHaveValue(
    "Newer unsaved edit",
  );
});

it("ignores a pre-save publication read that returns after the successful PATCH refresh", async () => {
  FakeEventSource.instances = [];
  let metadataReads = 0;
  let releaseOldRead: (() => void) | undefined;
  let savedLyrics: Record<string, unknown> | undefined;
  server.use(
    http.get("*/api/library/releases/release-race", () =>
      HttpResponse.json({
        id: "release-race",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-race",
            local_track_id: "track-race",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 2,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-race",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-race/positions/position-race/metadata",
      async () => {
        metadataReads += 1;
        if (metadataReads === 2) {
          await new Promise<void>((resolve) => {
            releaseOldRead = resolve;
          });
          return HttpResponse.json({
            revision: 1,
            publication_applied: false,
            envelope: {
              scope: "position",
              version: 1,
              desired: {
                lyrics: {
                  authority: "provider",
                  provenance: { kind: "provider" },
                  state: "value",
                  value: "Stale old value",
                },
              },
            },
          });
        }
        return HttpResponse.json({
          revision: metadataReads > 2 ? 2 : 1,
          publication_applied: false,
          envelope: {
            scope: "position",
            version: 1,
            desired: savedLyrics ? { lyrics: savedLyrics } : {},
          },
        });
      },
    ),
    http.get(
      "*/api/library/releases/release-race/positions/position-race/publication",
      () =>
        HttpResponse.json({
          release_id: "release-race",
          position_id: "position-race",
          active_epoch: "epoch-race",
          metadata_revision: 1,
          can_apply_manual_lyrics: false,
          active_attempt: {
            id: "operation-race",
            state: "running",
            stage: "rewriting",
          },
        }),
    ),
    http.get("*/api/operations/operation-race", () =>
      HttpResponse.json({
        id: "operation-race",
        kind: "publication",
        state: "running",
        stage: "rewriting",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.patch(
      "*/api/library/releases/release-race/positions/position-race/metadata",
      async ({ request }) => {
        const body = (await request.json()) as {
          changes: { lyrics: Record<string, unknown> };
        };
        savedLyrics = body.changes.lyrics;
        return HttpResponse.json({
          revision: 2,
          publication_applied: false,
          envelope: {
            scope: "position",
            version: 1,
            desired: { lyrics: savedLyrics },
          },
        });
      },
    ),
  );
  vi.stubGlobal("EventSource", FakeEventSource);

  render(<LibraryReleaseScreen releaseId="release-race" />);
  const editor = await screen.findByLabelText("Сохранённый текст песни");
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  FakeEventSource.instances[0].emit("open");
  await waitFor(() => expect(metadataReads).toBe(2));
  fireEvent.change(editor, { target: { value: "Saved current value" } });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить текст" }));
  await screen.findByText(
    "Текст сохранён. Примените его отдельно к текущей публикации.",
  );
  const releaseRead = releaseOldRead;
  if (!releaseRead) throw new Error("Background metadata read did not start");
  releaseRead();
  await waitFor(() => expect(metadataReads).toBeGreaterThan(2));
  expect(screen.getByLabelText("Сохранённый текст песни")).toHaveValue(
    "Saved current value",
  );
});

it("monitors an already-active attempt, preserves a dirty draft on wake-up refresh, and closes SSE on unmount", async () => {
  FakeEventSource.instances = [];
  let operationReads = 0;
  server.use(
    http.get("*/api/library/releases/release-2", () =>
      HttpResponse.json({
        id: "release-2",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-2",
            local_track_id: "track-2",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 2,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-2",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-2/positions/position-2/metadata",
      () =>
        HttpResponse.json({
          revision: 2,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-2/positions/position-2/publication",
      () =>
        HttpResponse.json({
          release_id: "release-2",
          position_id: "position-2",
          active_epoch: "epoch-2",
          metadata_revision: 2,
          can_apply_manual_lyrics: false,
          active_attempt: {
            id: "operation-running",
            state: "running",
            stage: "rewriting",
          },
        }),
    ),
    http.get("*/api/operations/operation-running", () => {
      operationReads += 1;
      return HttpResponse.json({
        id: "operation-running",
        kind: "publication",
        state: "running",
        stage: "rewriting",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      });
    }),
  );
  vi.stubGlobal("EventSource", FakeEventSource);

  const view = render(<LibraryReleaseScreen releaseId="release-2" />);
  const editor = await screen.findByLabelText("Сохранённый текст песни");
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  fireEvent.change(editor, { target: { value: "Unsaved draft" } });
  FakeEventSource.instances[0].emit("open");
  await waitFor(() => expect(operationReads).toBeGreaterThan(0));
  await waitFor(() =>
    expect(screen.getByLabelText("Сохранённый текст песни")).toHaveValue(
      "Unsaved draft",
    ),
  );
  view.unmount();
  expect(FakeEventSource.instances[0].closed).toBe(true);
});

it("recovers a failed publication refresh with a read-only refresh action", async () => {
  FakeEventSource.instances = [];
  let publicationReads = 0;
  let applyRequests = 0;
  server.use(
    http.get("*/api/library/releases/release-3", () =>
      HttpResponse.json({
        id: "release-3",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-3",
            local_track_id: "track-3",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 2,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-3",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-3/positions/position-3/metadata",
      () =>
        HttpResponse.json({
          revision: 2,
          publication_applied: false,
          envelope: {
            scope: "position",
            version: 1,
            desired: {
              lyrics: {
                authority: "manual",
                provenance: { kind: "manual" },
                state: "value",
                value: "Saved",
              },
            },
          },
        }),
    ),
    http.get(
      "*/api/library/releases/release-3/positions/position-3/publication",
      () => {
        publicationReads += 1;
        if (publicationReads === 2)
          return HttpResponse.json(
            { detail: "temporary unavailable" },
            { status: 503 },
          );
        return HttpResponse.json({
          release_id: "release-3",
          position_id: "position-3",
          active_epoch: "epoch-3",
          current_epoch: "epoch-3",
          current_generation: "generation-3",
          metadata_revision: 2,
          can_apply_manual_lyrics: true,
          current_path: "/music/song.flac",
          active_attempt: {
            id: "operation-wake",
            state: "running",
            stage: "writing",
          },
          manual_lyrics: { pending: true, state: "value" },
        });
      },
    ),
    http.get("*/api/operations/operation-wake", () =>
      HttpResponse.json({
        id: "operation-wake",
        kind: "publication",
        state: "running",
        stage: "writing",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      }),
    ),
    http.post(
      "*/api/library/releases/release-3/positions/position-3/publication/manual-lyrics",
      () => {
        applyRequests += 1;
        return HttpResponse.json({ id: "new-op" }, { status: 202 });
      },
    ),
  );
  vi.stubGlobal("EventSource", FakeEventSource);
  const view = render(<LibraryReleaseScreen releaseId="release-3" />);
  await screen.findByLabelText("Сохранённый текст песни");
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  FakeEventSource.instances[0].emit("open");
  await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Обновить данные" }));
  await waitFor(() => expect(publicationReads).toBe(3));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Применить сохранённый текст" }),
  ).toBeDisabled();
  view.unmount();
  expect(applyRequests).toBe(0);
});

it("keeps monitoring an admitted first publication when the immediate publication refresh fails", async () => {
  FakeEventSource.instances = [];
  let publicationReads = 0;
  let operationReads = 0;
  server.use(
    http.get("*/api/library/releases/release-first", () =>
      HttpResponse.json({
        id: "release-first",
        title: "Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [
          {
            id: "position-first",
            local_track_id: "track-first",
            title: "Song",
            displayed_number: "1",
            medium: 1,
            position: 1,
            metadata: {},
            metadata_revision: 2,
            metadata_version: 1,
            track_metadata_revision: 1,
            primary_artist: {},
            recording_id: "recording-first",
            artists: [],
            sources: [],
          },
        ],
      }),
    ),
    http.get(
      "*/api/library/releases/release-first/positions/position-first/metadata",
      () =>
        HttpResponse.json({
          revision: 2,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-first/positions/position-first/publication",
      () => {
        publicationReads += 1;
        if (publicationReads === 2)
          return HttpResponse.json(
            { detail: "temporarily unavailable" },
            { status: 503 },
          );
        return HttpResponse.json({
          release_id: "release-first",
          position_id: "position-first",
          active_epoch: "epoch-first",
          current_epoch: publicationReads > 2 ? "epoch-first" : undefined,
          current_generation:
            publicationReads > 2 ? "generation-first" : undefined,
          current_path:
            publicationReads > 2 ? "/music/Album/Song.mka" : undefined,
          metadata_revision: 2,
          can_apply_manual_lyrics: false,
        });
      },
    ),
    http.get(
      "*/api/library/releases/release-first/positions/position-first/publication/preview",
      () =>
        HttpResponse.json({
          release_id: "release-first",
          position_id: "position-first",
          epoch: "epoch-first",
          context_digest: "a".repeat(64),
          selected_source: {
            root_id: "root",
            location_id: "location",
            media_variant_id: "variant",
            stream_index: 0,
            codec: "flac",
          },
          transform: {
            action: "copy",
            output_container: "mka",
            extension: "mka",
          },
          path_components: ["Album", "Song.mka"],
          resolved_path_components: ["Album", "Song.mka"],
          metadata_revisions: {},
          warnings: [],
          blockers: [],
          native_status: "native verification required",
        }),
    ),
    http.post(
      "*/api/library/releases/release-first/positions/position-first/publication",
      () => HttpResponse.json({ id: "first-operation" }, { status: 202 }),
    ),
    http.get("*/api/operations/first-operation", () => {
      operationReads += 1;
      return HttpResponse.json({
        id: "first-operation",
        kind: "library_publication",
        state: "succeeded",
        stage: "completed",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      });
    }),
  );
  vi.stubGlobal("EventSource", FakeEventSource);
  render(<LibraryReleaseScreen releaseId="release-first" />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  await waitFor(() => expect(publicationReads).toBe(2));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "temporarily unavailable",
  );
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
  FakeEventSource.instances[0].emit("open");
  await waitFor(() => expect(operationReads).toBeGreaterThan(0));
  await waitFor(() => expect(publicationReads).toBe(3));
  expect(
    await screen.findByText(/\/music\/Album\/Song\.mka/),
  ).toBeInTheDocument();
  await waitFor(() => expect(FakeEventSource.instances[0].closed).toBe(true));
});

it("ignores a deferred read-only retry failure after navigating to another release", async () => {
  FakeEventSource.instances = [];
  let releaseRetry: (() => void) | undefined;
  let oldPublicationReads = 0;
  let newPublicationReads = 0;
  const track = {
    id: "position-shared",
    local_track_id: "track-shared",
    title: "Song",
    displayed_number: "1",
    medium: 1,
    position: 1,
    metadata: {},
    metadata_revision: 2,
    metadata_version: 1,
    track_metadata_revision: 1,
    primary_artist: {},
    recording_id: "recording-shared",
    artists: [],
    sources: [],
  };
  server.use(
    http.get("*/api/library/releases/release-old", () =>
      HttpResponse.json({
        id: "release-old",
        title: "Old Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [track],
      }),
    ),
    http.get(
      "*/api/library/releases/release-old/positions/position-shared/metadata",
      () =>
        HttpResponse.json({
          revision: 2,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-old/positions/position-shared/publication",
      async () => {
        oldPublicationReads += 1;
        if (oldPublicationReads === 1)
          return HttpResponse.json(
            { detail: "old release unavailable" },
            { status: 503 },
          );
        await new Promise<void>((resolve) => {
          releaseRetry = resolve;
        });
        return HttpResponse.error();
      },
    ),
    http.get("*/api/library/releases/release-new", () =>
      HttpResponse.json({
        id: "release-new",
        title: "New Album",
        metadata: {},
        metadata_revision: 1,
        metadata_version: 1,
        primary_artist: {},
        artists: [],
        tracks: [track],
      }),
    ),
    http.get(
      "*/api/library/releases/release-new/positions/position-shared/metadata",
      () =>
        HttpResponse.json({
          revision: 1,
          publication_applied: false,
          envelope: { scope: "position", version: 1, desired: {} },
        }),
    ),
    http.get(
      "*/api/library/releases/release-new/positions/position-shared/publication",
      () => {
        newPublicationReads += 1;
        return HttpResponse.json({
          release_id: "release-new",
          position_id: "position-shared",
          active_epoch: "epoch-new",
          metadata_revision: 1,
          can_apply_manual_lyrics: false,
          current_path: "/music/New/Song.flac",
        });
      },
    ),
  );
  vi.stubGlobal("EventSource", FakeEventSource);

  const view = render(<LibraryReleaseScreen releaseId="release-old" />);
  expect(
    await screen.findByText("old release unavailable"),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Обновить данные" }));
  await waitFor(() => expect(oldPublicationReads).toBe(2));

  view.rerender(<LibraryReleaseScreen releaseId="release-new" />);
  expect(await screen.findByText("New Album")).toBeInTheDocument();
  expect(
    await screen.findByText(/\/music\/New\/Song\.flac/),
  ).toBeInTheDocument();
  await waitFor(() => expect(newPublicationReads).toBe(1));

  const retry = releaseRetry;
  if (!retry) throw new Error("Deferred publication retry did not start");
  await act(async () => {
    retry();
    await Promise.resolve();
  });

  expect(screen.queryByText("old release unavailable")).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Обновить данные" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("Сохранённый текст песни")).toHaveValue("");
});
