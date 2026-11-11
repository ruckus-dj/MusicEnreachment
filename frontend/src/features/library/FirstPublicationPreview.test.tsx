import "@testing-library/jest-dom/vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useCallback, useRef, useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { LibraryFirstPublicationPreviewResponse } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { AggregatePublicationPreview } from "./AggregatePublicationPreview";

const api = vi.hoisted(() => ({
  getLibraryFirstPublicationPreview: vi.fn(),
  startLibraryFirstPublication: vi.fn(),
}));

vi.mock("../../api/generated/client", () => api);

import {
  FirstPublicationPreview,
  type PublicationPreviewController,
  type PublicationPreviewState,
} from "./FirstPublicationPreview";

const preview = {
  blockers: [],
  context_digest: "frozen-digest",
  epoch: "epoch-1",
  metadata_revisions: {},
  resolved_path_components: ["Artist", "Release", "01 - Track.flac"],
  selected_source: {
    audio_stream_count: 1,
    codec: "FLAC",
    language: "",
    location_id: "location-1",
    media_variant_id: "variant-1",
    requires_explicit_stream_selection: false,
    root_id: "root-1",
    stream_index: 0,
    title: "Track",
    unsupported_audio_stream_count: 0,
  },
  source_candidates: [],
  desired_tags: { TITLE: [" Track  ", "Second"], GENRE: [] },
  transform: { action: "copy", output_container: "flac" },
} as unknown as LibraryFirstPublicationPreviewResponse;

function component(
  refreshToken: number,
  onAdmitted = vi.fn(),
  comparisonOnly = false,
) {
  return (
    <I18nProvider>
      <FirstPublicationPreview
        releaseId="release-1"
        positionId="position-1"
        refreshToken={refreshToken}
        comparisonOnly={comparisonOnly}
        onAdmitted={onAdmitted}
        onRefresh={vi.fn()}
      />
    </I18nProvider>
  );
}

function AggregatePreviewFixture() {
  const [states, setStates] = useState<
    Record<string, PublicationPreviewState | undefined>
  >({});
  const controllers = useRef(new Map<string, PublicationPreviewController>());
  const [, setControllerVersion] = useState(0);
  const register = useCallback(
    (positionID: string, controller: PublicationPreviewController | null) => {
      if (controller) controllers.current.set(positionID, controller);
      else controllers.current.delete(positionID);
      setControllerVersion((version) => version + 1);
    },
    [],
  );
  const update = useCallback(
    (positionID: string, state: PublicationPreviewState) => {
      setStates((current) => ({ ...current, [positionID]: state }));
    },
    [],
  );
  const firstController = useCallback(
    (controller: PublicationPreviewController | null) =>
      register("first", controller),
    [register],
  );
  const currentController = useCallback(
    (controller: PublicationPreviewController | null) =>
      register("current", controller),
    [register],
  );
  const firstState = useCallback(
    (state: PublicationPreviewState) => update("first", state),
    [update],
  );
  const currentState = useCallback(
    (state: PublicationPreviewState) => update("current", state),
    [update],
  );

  return (
    <>
      <AggregatePublicationPreview
        positions={[
          { id: "first", label: "First position", current: false },
          { id: "current", label: "Current position", current: true },
        ]}
        controllers={controllers.current}
        states={states}
      />
      <FirstPublicationPreview
        releaseId="release-1"
        positionId="first"
        aggregateManaged
        onAdmitted={vi.fn()}
        onRefresh={vi.fn()}
        onController={firstController}
        onStateChange={firstState}
      />
      <FirstPublicationPreview
        releaseId="release-1"
        positionId="current"
        comparisonOnly
        aggregateManaged
        onAdmitted={vi.fn()}
        onRefresh={vi.fn()}
        onController={currentController}
        onStateChange={currentState}
      />
    </>
  );
}

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", storage);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("keeps a pending admission fenced across a metadata refresh and delivers its admitted operation", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: preview,
  });
  let resolveAdmission!: (response: {
    status: number;
    data: { id: string };
  }) => void;
  api.startLibraryFirstPublication.mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveAdmission = resolve;
      }),
  );
  const onAdmitted = vi.fn();
  const view = render(component(0, onAdmitted));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByRole("button", { name: "Опубликовать" });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));

  view.rerender(component(1, onAdmitted));
  expect(
    screen.getByRole("button", { name: "Проверка и запуск…" }),
  ).toBeDisabled();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(1);

  await act(async () =>
    resolveAdmission({ status: 202, data: { id: "operation-1" } }),
  );
  await waitFor(() => expect(onAdmitted).toHaveBeenCalledWith("operation-1"));
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeDisabled();
  expect(api.startLibraryFirstPublication).toHaveBeenCalledTimes(1);
});

it("prepares and confirms through the aggregate controller using its frozen identity", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: preview,
  });
  api.startLibraryFirstPublication.mockResolvedValue({
    status: 202,
    data: { id: "aggregate-operation" },
  });
  const controller: { current: PublicationPreviewController | null } = {
    current: null,
  };
  const state: { current: PublicationPreviewState | undefined } = {
    current: undefined,
  };
  render(
    <I18nProvider>
      <FirstPublicationPreview
        releaseId="release-1"
        positionId="position-1"
        refreshToken={3}
        aggregateManaged
        onAdmitted={vi.fn()}
        onRefresh={vi.fn()}
        onController={(value) => {
          controller.current = value;
        }}
        onStateChange={(value) => {
          state.current = value;
        }}
      />
    </I18nProvider>,
  );

  await waitFor(() => expect(controller.current).not.toBeNull());
  controller.current?.prepare("forced");
  await waitFor(() => expect(state.current?.canConfirm).toBe(true));
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledWith(
    "release-1",
    "position-1",
    { mode: "ordinary" },
    { cache: "no-store" },
  );
  expect(state.current?.identity).toMatchObject({
    digest: "frozen-digest",
    epoch: "epoch-1",
    mode: "ordinary",
    workflow: "first",
    refreshToken: 3,
  });

  const identity = state.current?.identity;
  if (identity) controller.current?.confirm(identity);
  await waitFor(() =>
    expect(api.startLibraryFirstPublication).toHaveBeenCalledWith(
      "release-1",
      "position-1",
      expect.objectContaining({
        preview_digest: "frozen-digest",
        expected_epoch: "epoch-1",
      }),
    ),
  );
});

it("requires a fresh aggregate prepare after mode changes and allows explicit exclusion of unchanged CURRENT", async () => {
  let currentPreviewCount = 0;
  const sourceCandidates = [
    {
      audio_stream_count: 1,
      codec: "FLAC",
      language: "",
      location_id: "location-1",
      media_variant_id: "variant-1",
      requires_explicit_stream_selection: false,
      root_id: "root-1",
      stream_index: 0,
      title: "Candidate One",
      unsupported_audio_stream_count: 0,
    },
    {
      audio_stream_count: 1,
      codec: "FLAC",
      language: "",
      location_id: "location-1",
      media_variant_id: "variant-2",
      requires_explicit_stream_selection: false,
      root_id: "root-1",
      stream_index: 1,
      title: "Candidate Two",
      unsupported_audio_stream_count: 0,
    },
  ];
  api.getLibraryFirstPublicationPreview.mockImplementation(
    async (
      _releaseId: string,
      positionID: string,
      params: { mode?: string },
    ) =>
      positionID === "first"
        ? {
            status: 200,
            data: { ...preview, source_candidates: sourceCandidates },
          }
        : {
            status: 200,
            data: {
              ...preview,
              current_comparison: {
                audio_status: "matches",
                current_components: [],
                generation: "generation-1",
                reason: "same content",
                rendered_components: [],
                rendered_matches: true,
                resolved_components: [],
                resolved_matches: true,
                status: "matches",
                tag_differences: [
                  {
                    key: "TITLE",
                    current_present: true,
                    current: ["Track", "First"],
                    desired_present: true,
                    desired: [" Track  ", "Second"],
                  },
                  {
                    key: "GENRE",
                    current_present: false,
                    current: null,
                    desired_present: true,
                    desired: [],
                  },
                ],
              },
              confirmation_capability: {
                kind: "retained_same_path",
                status: currentPreviewCount++ === 0 ? "ready" : "unchanged",
                reason_codes: [],
                current_generation: "generation-1",
              },
              mode: params.mode ?? "ordinary",
              source_candidates: [],
            },
          },
  );
  api.startLibraryFirstPublication.mockResolvedValue({
    status: 202,
    data: { id: "aggregate-first-operation" },
  });
  render(
    <I18nProvider>
      <AggregatePreviewFixture />
    </I18nProvider>,
  );

  const confirm = screen.getByRole("button", {
    name: "Подтвердить готовые публикации",
  });
  fireEvent.click(screen.getByRole("radio", { name: "Принудительная" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Подготовить предпросмотр" }),
  );
  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(2),
  );
  const aggregate = screen.getByRole("region", {
    name: "Общий предпросмотр публикации",
  });
  await waitFor(() => expect(aggregate).toHaveTextContent(/Обновить\s*1/));
  expect(aggregate).not.toHaveTextContent(/Создать\s*1/);
  fireEvent.click(screen.getByRole("button", { name: "First position" }));
  expect(aggregate).toHaveTextContent("Нет исходной публикации CURRENT");
  expect(aggregate).toHaveTextContent('[" Track ","Second"]');
  const candidateOne = screen.getByRole("radio", { name: /Candidate One/ });
  fireEvent.click(candidateOne);
  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(3),
  );
  await waitFor(() => expect(aggregate).toHaveTextContent(/Создать\s*1/));
  await waitFor(() =>
    expect(screen.getByRole("radio", { name: /Candidate One/ })).toBeChecked(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Current position" }));
  fireEvent.click(screen.getByRole("button", { name: "First position" }));
  expect(screen.getByRole("radio", { name: /Candidate One/ })).toBeChecked();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(3);
  await waitFor(() => expect(confirm).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Current position" }));
  expect(aggregate).toHaveTextContent('["Track","First"]');
  expect(aggregate).toHaveTextContent("отсутствует");
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(3);
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenNthCalledWith(
    1,
    "release-1",
    "first",
    { mode: "ordinary" },
    { cache: "no-store" },
  );
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenNthCalledWith(
    2,
    "release-1",
    "current",
    { workflow: "retained_same_path", mode: "forced" },
    { cache: "no-store" },
  );

  fireEvent.click(screen.getByRole("radio", { name: "Обычная" }));
  expect(confirm).toBeDisabled();
  fireEvent.click(screen.getByRole("radio", { name: "Принудительная" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Подготовить предпросмотр" }),
  );
  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(5),
  );
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "Включить позицию: Current position",
    }),
  );
  await waitFor(() => expect(aggregate).toHaveTextContent(/Пропущено\s*1/));
  fireEvent.click(screen.getByRole("button", { name: "Current position" }));
  expect(aggregate).toHaveTextContent("[]");
  expect(aggregate).toHaveTextContent("отсутствует");
  expect(aggregate).toHaveTextContent(/Пропущено\s*1/);
  expect(aggregate).toHaveTextContent(
    "Выбранный источник не изменит текущую публикацию; действие недоступно.",
  );
  expect(confirm).toBeEnabled();
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "Включить позицию: Current position",
    }),
  );
  expect(confirm).toBeEnabled();
  expect(within(aggregate).getAllByText("Без изменений")).toHaveLength(2);
  fireEvent.click(confirm);
  await waitFor(() =>
    expect(api.startLibraryFirstPublication).toHaveBeenCalledTimes(1),
  );
  await waitFor(() => expect(confirm).toBeDisabled());
  expect(api.startLibraryFirstPublication).toHaveBeenCalledWith(
    "release-1",
    "first",
    expect.objectContaining({ preview_digest: "frozen-digest" }),
  );
});

it("retains the disabled fence after an uncertain admission when metadata refreshes", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: preview,
  });
  api.startLibraryFirstPublication.mockRejectedValue(
    new Error("connection lost"),
  );
  const view = render(component(0));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByRole("button", { name: "Опубликовать" });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();

  view.rerender(component(1));
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(1);
  expect(api.startLibraryFirstPublication).toHaveBeenCalledTimes(1);
});

it("invalidates an unsubmitted preview and reloads its digest when metadata refreshes", async () => {
  api.getLibraryFirstPublicationPreview
    .mockResolvedValueOnce({ status: 200, data: preview })
    .mockResolvedValueOnce({
      status: 200,
      data: { ...preview, context_digest: "new-digest" },
    });
  const view = render(component(0));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByText("План первой публикации");

  view.rerender(component(1));
  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(2),
  );
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeEnabled();
  api.startLibraryFirstPublication.mockResolvedValue({
    status: 202,
    data: { id: "operation-after-refresh" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  await waitFor(() =>
    expect(api.startLibraryFirstPublication).toHaveBeenCalledWith(
      "release-1",
      "position-1",
      expect.objectContaining({ preview_digest: "new-digest" }),
    ),
  );
});

it("requests the forced comparison without offering confirmation", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: {
      ...preview,
      current_comparison: {
        audio_status: "unavailable",
        current_components: null,
        generation: "generation-1",
        reason: "not published",
        rendered_components: [],
        rendered_matches: false,
        resolved_components: null,
        resolved_matches: false,
        status: "absent",
        tag_differences: [],
      },
    },
  });

  render(component(0, vi.fn(), true));
  fireEvent.click(screen.getByRole("radio", { name: "Принудительный" }));

  expect(
    await screen.findByText("Сравнение с текущей публикацией"),
  ).toBeInTheDocument();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(1);
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledWith(
    "release-1",
    "position-1",
    { mode: "forced" },
    { cache: "no-store" },
  );
  for (const label of [
    "Путь текущей публикации",
    "Отображённый путь",
    "Разрешённый путь",
  ]) {
    expect(screen.getByText(`${label}: не определён`)).toBeInTheDocument();
  }
  expect(screen.getByText("Различий тегов нет.")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Опубликовать" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("note")).toHaveTextContent(
    "Сравнение доступно только для чтения",
  );
});

it("submits retained republish workflow and frozen CURRENT identity", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: {
      ...preview,
      warnings: ["output_quality_would_downgrade", "unmapped_warning_code"],
      current_comparison: {
        audio_status: "matches",
        current_components: ["Artist", "Release", "01 - Track.flac"],
        generation: "generation-1",
        reason: "same path",
        rendered_components: ["Artist", "Release", "01 - Track.flac"],
        rendered_matches: true,
        resolved_components: ["Artist", "Release", "01 - Track.flac"],
        resolved_matches: true,
        status: "matches",
        tag_differences: [],
      },
      confirmation_capability: {
        kind: "retained_same_path",
        status: "ready",
        reason_codes: [],
        current_generation: "generation-1",
      },
      mode: "ordinary",
      source_candidates: [],
    },
  });
  api.startLibraryFirstPublication.mockResolvedValue({
    status: 202,
    data: { id: "retained-operation" },
  });

  render(component(0, vi.fn(), true));
  fireEvent.click(
    screen.getByRole("radio", {
      name: "Повторная публикация выбранного источника",
    }),
  );
  await screen.findByRole("button", {
    name: "Повторная публикация выбранного источника",
  });

  expect(
    screen.getByText(
      "Подтверждение заменит CURRENT аудио более низкого качества.",
    ),
  ).toBeInTheDocument();
  expect(screen.getByText("unmapped_warning_code")).toBeInTheDocument();
  expect(api.startLibraryFirstPublication).not.toHaveBeenCalled();

  fireEvent.click(
    screen.getByRole("button", {
      name: "Повторная публикация выбранного источника",
    }),
  );

  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledWith(
      "release-1",
      "position-1",
      { workflow: "retained_same_path", mode: "ordinary" },
      { cache: "no-store" },
    ),
  );
  await waitFor(() =>
    expect(api.startLibraryFirstPublication).toHaveBeenCalledWith(
      "release-1",
      "position-1",
      {
        workflow: "retained_same_path",
        mode: "ordinary",
        preview_digest: "frozen-digest",
        expected_epoch: "epoch-1",
        expected_generation: "generation-1",
      },
    ),
  );
});

it("does not offer confirmation when retained CURRENT content is unchanged", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: {
      ...preview,
      current_comparison: {
        audio_status: "matches",
        current_components: [],
        generation: "generation-1",
        reason: "same content",
        rendered_components: [],
        rendered_matches: true,
        resolved_components: [],
        resolved_matches: true,
        status: "matches",
        tag_differences: [],
      },
      confirmation_capability: {
        kind: "retained_same_path",
        status: "unchanged",
        reason_codes: ["content_unchanged"],
        current_generation: "generation-1",
      },
      mode: "ordinary",
      source_candidates: [],
    },
  });

  render(component(0, vi.fn(), true));
  fireEvent.click(
    screen.getByRole("radio", {
      name: "Повторная публикация выбранного источника",
    }),
  );

  const confirm = await screen.findByRole("button", {
    name: "Повторная публикация выбранного источника",
  });
  expect(confirm).toBeDisabled();
  expect(
    screen.getByText(
      "Выбранный источник не изменит текущую публикацию; действие недоступно.",
    ),
  ).toBeInTheDocument();
  expect(api.startLibraryFirstPublication).not.toHaveBeenCalled();
});
