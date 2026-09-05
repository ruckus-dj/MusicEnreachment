// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Detail, Tags } from "../types";
import { TrackDetail } from "./TrackDetail";

afterEach(cleanup);

const detail: Detail = {
  record_id: "record-1",
  source_state: "present",
  processing_state: "complete",
  match_state: "matched",
  publication_state: "current",
  metadata_state: "final",
  sources: [
    {
      source_id: "source-a",
      path: "/incoming/old.flac",
      sha256: "a".repeat(64),
      state: "present",
      format: "flac",
      tag_observations: [
        { name: "TITLE", value: "Track", format: "FLAC" },
        { name: "ARTIST", value: "Source Artist", format: "FLAC" },
        { name: "ALBUM", value: "Source Album", format: "FLAC" },
      ],
    },
    {
      source_id: "source-b",
      path: "/incoming/new.flac",
      sha256: "b".repeat(64),
      state: "present",
      format: "flac",
      tag_observations: [{ name: "TITLE", value: "Track", format: "FLAC" }],
    },
  ],
  publications: [],
  states: {
    source: "present",
    processing: "complete",
    match: "matched",
    publication: "current",
    metadata: "final",
  },
  events: [],
};

function renderDetail(overrides: Partial<Parameters<typeof TrackDetail>[0]> = {}) {
  const props = {
    detail,
    sourceId: "source-a",
    draft: { TITLE: "Track" } satisfies Tags,
    setDraft: vi.fn(),
    saving: false,
    reprocessing: false,
    musicbrainzHost: null,
    onSave: vi.fn(async () => true),
    onSelectCandidate: vi.fn(),
    onSelectEffectiveSource: vi.fn(),
    effectiveSourceId: "source-a",
    effectiveSourceError: "",
    effectiveSourceSuccess: "",
    recordingCorrectionError: "",
    recordingCorrectionReview: "",
    ...overrides,
  };
  return render(<TrackDetail {...props} />);
}

describe("TrackDetail effective source", () => {
  it("shows the complete source path instead of the technical record id", () => {
    renderDetail({
      detail: {
        ...detail,
        sources: [{ ...detail.sources[0], path: "/data/music/NoizeMC/Albums/ABCD/file.flac" }],
      },
      draft: {},
    });

    expect(screen.getByTestId("source-path").textContent).toBe(
      "/data/music/NoizeMC/Albums/ABCD/file.flac",
    );
    expect(screen.getAllByText("file.flac").length).toBeGreaterThan(0);
    expect(screen.queryByText("record-1")).toBeNull();
  });

  it("shows the fingerprint duration in minutes and seconds", () => {
    renderDetail({
      detail: {
        ...detail,
        sources: [
          {
            ...detail.sources[0],
            fingerprints: [
              {
                state: "available",
                fingerprint: "fingerprint",
                duration_seconds: 245.4,
                tool_version: "fpcalc 1.5.1",
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(screen.getByText("Длительность")).toBeTruthy();
    expect(screen.getByText("4:05")).toBeTruthy();
  });

  it("shows the automatically matched AcousticID recording as selected", () => {
    renderDetail({
      detail: {
        ...detail,
        musicbrainz_recording_id: "47d13484-9eed-4460-babd-bca3a19fcd77",
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "47d13484-9eed-4460-babd-bca3a19fcd77",
                evidence: {
                  provider: "acoustid",
                  recording_mbid: "47d13484-9eed-4460-babd-bca3a19fcd77",
                  artist: "Fixture Artist",
                  release: "Fixture Release",
                  title: "Fixture Track",
                  album: "Fixture Release",
                  score: 0.99,
                  tags: { TRACKNUMBER: "3", TRACKTOTAL: "18" },
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(
      screen.getByRole("button", { name: /Recording \+ Release.*Выбрано/ }).textContent,
    ).toContain("Выбрано");
  });

  it("shows one unified row per MusicBrainz candidate", () => {
    renderDetail({
      detail: {
        ...detail,
        musicbrainz_recording_id: "recording-a",
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "release-a:recording-a",
                evidence: {
                  provider: "musicbrainz",
                  entity: "recording_release",
                  recording_mbid: "recording-a",
                  release_mbid: "release-a",
                  artist: "Fixture Artist",
                  release: "Fixture Album",
                  title: "Fixture Track",
                  album: "Fixture Album",
                  score: 0.9,
                  duration_seconds: 245,
                  score_components: {
                    artist: 0.8,
                    release: 0.7,
                    duration: 0.9,
                    title: 0.95,
                    track: 1,
                    title_match: 0.95,
                    track_number_match: 1,
                  },
                  tags: {
                    TITLE: "Candidate Track",
                    ARTIST: "Candidate Artist",
                    ALBUM: "Candidate Album",
                    TRACKNUMBER: "3",
                    TRACKTOTAL: "18",
                  },
                },
              },
              {
                candidate_key: "release-b:recording-b",
                evidence: {
                  provider: "musicbrainz",
                  entity: "recording_release",
                  recording_mbid: "recording-b",
                  release_mbid: "release-b",
                  artist: "Fixture Artist",
                  release: "Other Album",
                  title: "Other Track",
                  album: "Other Album",
                  score: 0.4,
                  tags: {},
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    fireEvent.click(screen.getByRole("button", { name: /Recording \+ Release/ }));

    const comparisonHas = (label: string, value: string) =>
      screen.getAllByText(label).some((item) => item.parentElement?.textContent?.includes(value));
    expect(screen.getByRole("article", { name: /Fixture Track/ }).className).toContain(
      "candidate-card-selected",
    );
    expect(screen.getByText("Позиция: 3/18")).toBeTruthy();
    expect(screen.getByText("95.00% (95.00%)")).toBeTruthy();
    expect(screen.getByRole("article", { name: /Fixture Track/ }).textContent).not.toContain(
      "Название TITLE",
    );
    expect(comparisonHas("Источник:", "Track")).toBe(true);
    expect(comparisonHas("Кандидат:", "Candidate Track")).toBe(true);
    expect(comparisonHas("Источник:", "Source Artist")).toBe(true);
    expect(comparisonHas("Кандидат:", "Candidate Artist")).toBe(true);
    expect(comparisonHas("Источник:", "Source Album")).toBe(true);
    expect(comparisonHas("Кандидат:", "Candidate Album")).toBe(true);
    expect(screen.getAllByText("Релиз")).not.toHaveLength(0);
    expect(screen.getAllByText("Запись")).not.toHaveLength(0);
    expect(screen.getAllByRole("article")).toHaveLength(2);
  });

  it("marks the unified candidate selected when its release is selected", () => {
    renderDetail({
      detail: {
        ...detail,
        musicbrainz_release_id: "release-a",
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "release-a:recording-a",
                evidence: {
                  provider: "musicbrainz",
                  entity: "recording_release",
                  recording_mbid: "recording-a",
                  release_mbid: "release-a",
                  artist: "Fixture Artist",
                  release: "Fixture Album",
                  score: 0.9,
                  tags: { MUSICBRAINZ_ALBUMID: "release-a" },
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    fireEvent.click(screen.getByRole("button", { name: /Recording \+ Release/ }));
    expect(screen.getByRole("article", { name: /Fixture Album/ }).className).toContain(
      "candidate-card-selected",
    );
  });

  it("opens MusicBrainz candidates on the configured host", () => {
    renderDetail({
      musicbrainzHost: "https://musicbrainz.internal",
      detail: {
        ...detail,
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
                evidence: {
                  provider: "musicbrainz",
                  recording_mbid: "recording-result",
                  artist: "Fixture Artist",
                  release: "Fixture Release",
                  title: "Fixture Track",
                  album: "Fixture Release",
                  score: 0.99,
                  tags: { TITLE: "Fixture Track" },
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(
      screen
        .getByRole("link", { name: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a" })
        .getAttribute("href"),
    ).toBe("https://musicbrainz.internal/release/f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a");
    expect(
      screen
        .getAllByRole("link", { name: "recording-result" })
        .map((link) => link.getAttribute("href")),
    ).toContain("https://musicbrainz.internal/recording/recording-result");
  });

  it("opens AcousticID recording candidates on the configured host", () => {
    renderDetail({
      musicbrainzHost: "https://musicbrainz.internal/",
      detail: {
        ...detail,
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "acoustid-result",
                evidence: {
                  provider: "acoustid",
                  recording_mbid: "47d13484-9eed-4460-babd-bca3a19fcd77",
                  artist: "Fixture Artist",
                  release: "Fixture Release",
                  title: "Fixture Track",
                  album: "Fixture Release",
                  score: 0.99,
                  tags: {},
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(
      screen
        .getByRole("link", { name: "47d13484-9eed-4460-babd-bca3a19fcd77" })
        .getAttribute("href"),
    ).toBe("https://musicbrainz.internal/recording/47d13484-9eed-4460-babd-bca3a19fcd77");
  });

  it("keeps identical recording MBIDs distinct by provider", () => {
    renderDetail({
      detail: {
        ...detail,
        sources: [
          {
            ...detail.sources[0],
            candidates: [
              {
                candidate_key: "shared-release:shared-recording",
                evidence: {
                  provider: "musicbrainz",
                  entity: "recording_release",
                  recording_mbid: "shared-recording",
                  release_mbid: "shared-release",
                  artist: "Fixture Artist",
                  release: "Fixture Release",
                  title: "Fixture Track",
                  album: "Fixture Release",
                  score: 0.99,
                  acoustid_score: 0.99,
                  musicbrainz_score: 0.98,
                  tags: { TITLE: "Fixture Track" },
                },
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(screen.getByText("AcousticID: 99%")).toBeTruthy();
    expect(screen.getByText("MusicBrainz: 98%")).toBeTruthy();
    expect(screen.getByText("Наш скоринг")).toBeTruthy();
    expect(screen.queryByText("Источник: AcousticID")).toBeNull();
    expect(screen.getAllByRole("article")).toHaveLength(1);
  });

  it("shows every provider attempt with its actual provider and outcome", () => {
    renderDetail({
      detail: {
        ...detail,
        sources: [
          {
            ...detail.sources[0],
            provider_attempts: [
              {
                provider: "acoustid",
                outcome: "acoustidmatch",
                snapshot_sha256: "a".repeat(64),
                created_at: "2026-08-13T15:36:00Z",
              },
              {
                provider: "musicbrainz",
                outcome: "unavailable",
                snapshot_sha256: "b".repeat(64),
                created_at: "2026-08-13T15:37:00Z",
              },
            ],
          },
          detail.sources[1],
        ],
      },
    });

    expect(screen.getByLabelText("Попытки провайдеров").textContent).toContain(
      "musicbrainz: unavailable",
    );
    expect(screen.getByLabelText("Попытки провайдеров").textContent).toContain(
      "acoustid: acoustidmatch",
    );
  });

  it("exposes source choices and sends the selected source through the callback", () => {
    const onSelectEffectiveSource = vi.fn();
    renderDetail({ onSelectEffectiveSource });

    fireEvent.change(screen.getByTestId("effective-source-choice"), {
      target: { value: "source-b" },
    });

    expect(onSelectEffectiveSource).toHaveBeenCalledWith("source-b");
    expect(screen.getByTestId("effective-source-current").textContent).toContain("old.flac");
  });

  it("renders accessible selection success and error feedback", () => {
    renderDetail({
      effectiveSourceId: "source-b",
      effectiveSourceSuccess: "Источник выбран для публикации",
      effectiveSourceError: "Источник недоступен для выбора",
    });

    expect(screen.getByTestId("effective-source-success").getAttribute("role")).toBe("status");
    expect(screen.getByTestId("effective-source-error").getAttribute("role")).toBe("alert");
    expect(screen.getByTestId("effective-source-current").textContent).toContain("new.flac");
  });

  it("renders the current publication evidence and lifecycle dimensions", () => {
    renderDetail({
      detail: {
        ...detail,
        metadata_revisions: [
          {
            id: 17,
            source_id: "source-a",
            layer: "final",
            revision: 4,
            tags: { TITLE: "Track" },
          },
        ],
        publications: [
          {
            publication_id: "publication-1",
            path: "/library/Artist/Track.flac",
            source_id: "source-a",
            metadata_revision_id: 17,
            sha256: "c".repeat(64),
            state: "current",
            created_at: "2026-08-12T10:00:00Z",
          },
        ],
      },
    });

    expect(screen.getByTestId("output-status").textContent).toContain("Актуальна");
    expect(screen.getByTestId("effective-source-current").textContent).toContain("old.flac");
    expect(screen.getByTestId("output-path").textContent).toContain("/library/Artist/Track.flac");
    expect(screen.getByTestId("output-hash").textContent).toContain("c".repeat(64));
    expect(screen.getByTestId("output-revision").textContent).toContain("4");
    expect(screen.getByTestId("source-lifecycle").textContent).toContain("Источник: На месте");
    expect(screen.getByTestId("source-lifecycle").textContent).toContain("Обработка: Завершена");
    expect(screen.getByTestId("source-lifecycle").textContent).toContain(
      "Сопоставление: Подтверждено",
    );
    expect(screen.getByRole("status").textContent).toContain("Публикация актуальна");
    expect(document.querySelector(".workflow-banner")?.className).toContain("ready");
  });

  it("renders review state and an explicit no-output state", () => {
    renderDetail({
      detail: {
        ...detail,
        processing_state: "needs_review",
        publication_state: "absent",
        states: {
          ...detail.states,
          processing: "needs_review",
          publication: "absent",
        },
        publications: [],
      },
    });

    expect(screen.getByTestId("output-status").textContent).toContain("Нет публикации");
    expect(screen.getByTestId("no-output").textContent).toContain("Выходной файл ещё не создан");
    expect(screen.getByTestId("output-path").textContent).toContain("Не создан");
    expect(screen.getByTestId("output-hash").textContent).toContain("Не рассчитана");
    expect(screen.getByTestId("output-revision").textContent).toContain("Нет данных");
    expect(screen.getByTestId("source-lifecycle").textContent).toContain("Проверка оператора");
    expect(document.querySelector(".workflow-banner")?.className).not.toContain("ready");
  });

  it("marks a disappeared published source as unavailable while keeping its evidence visible", () => {
    renderDetail({
      detail: {
        ...detail,
        sources: [
          { ...detail.sources[0], state: "disappeared", disappeared_at: "2026-08-12T10:00:00Z" },
        ],
        states: { ...detail.states, source: "disappeared" },
        publications: [
          {
            publication_id: "publication-1",
            path: "/library/Artist/Track.flac",
            source_id: "source-a",
            sha256: "c".repeat(64),
            state: "current",
          },
        ],
      },
    });

    expect(screen.getByTestId("source-unavailable").textContent).toContain(
      "Исходный файл не найден",
    );
    expect(screen.getByTestId("source-lifecycle").textContent).toContain(
      "Источник: Файл отсутствует",
    );
    expect(screen.getByTestId("source-unavailable").getAttribute("role")).toBe("status");
  });

  it("does not show green ready tone when publication state is current but no actual publication exists", () => {
    renderDetail({
      detail: {
        ...detail,
        publications: [],
        states: {
          ...detail.states,
          publication: "current",
        },
      },
    });

    expect(screen.getByTestId("output-status").textContent).not.toContain("Актуальна");
    expect(document.querySelector(".workflow-banner")?.className).not.toContain("ready");
  });

  it.each([
    ["missing hash", undefined],
    ["placeholder hash", "pending"],
  ])("keeps workflow pending and marks %s output hash unavailable", (_label, sha256) => {
    renderDetail({
      detail: {
        ...detail,
        publications: [
          {
            publication_id: "publication-1",
            path: "/library/Artist/Track.flac",
            source_id: "source-a",
            sha256,
            state: "current",
            created_at: "2026-08-12T10:00:00Z",
          },
        ],
      },
    });

    expect(document.querySelector(".workflow-banner")?.className).not.toContain("ready");
    expect(screen.getByTestId("output-hash").textContent).toBe("Не рассчитана");
  });
});

describe("TrackDetail recording correction", () => {
  it("keeps manual recording MBID correction available without provider evidence", () => {
    const onOverrideRecording = vi.fn();
    renderDetail({ onOverrideRecording });

    expect(screen.getByText("Провайдеры не вернули варианты для этого трека")).toBeTruthy();
    expect(screen.queryByText("Выберите варианты, чтобы продолжить")).toBeNull();

    fireEvent.click(screen.getByText("Выбрать recording MBID вручную"));
    fireEvent.change(screen.getByTestId("recording-mbid-input"), {
      target: { value: "  f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a  " },
    });
    fireEvent.click(screen.getByTestId("recording-correction-submit"));

    expect(onOverrideRecording).toHaveBeenCalledWith({
      recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
    });
  });

  it("submits only the trimmed recording MBID", () => {
    const onOverrideRecording = vi.fn();
    renderDetail({
      onOverrideRecording,
      detail: { ...detail, musicbrainz_recording_id: "old-recording" },
    });

    fireEvent.change(screen.getByTestId("recording-mbid-input"), {
      target: { value: "  f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a  " },
    });
    fireEvent.click(screen.getByTestId("recording-correction-submit"));

    expect(onOverrideRecording).toHaveBeenCalledWith({
      recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
    });
  });

  it("exposes conflict review feedback as accessible state", () => {
    renderDetail({
      detail: { ...detail, musicbrainz_recording_id: "old-recording" },
      recordingCorrectionError:
        "Исправление конфликтует с сохранёнными свидетельствами провайдера.",
      recordingCorrectionReview: "Требуется проверка исправления записи.",
    });

    expect(screen.getByTestId("recording-correction-error").getAttribute("role")).toBe("alert");
    expect(screen.getByTestId("recording-review-status").getAttribute("role")).toBe("status");
  });
});
