import { useEffect, useRef, useState } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  confirmLibraryDeletion,
  previewLibraryArtistDeletion,
  previewLibraryPublicationDeletion,
  previewLibraryReleaseDeletion,
  retryOperation,
} from "../../api/generated/client";
import type {
  LibraryDeletionPreviewResponse,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";

type DeletionTarget =
  | { type: "artist"; artistId: string }
  | { type: "release"; releaseId: string }
  | { type: "publication"; releaseId: string; positionId: string };

type Props = {
  target: DeletionTarget;
  releaseTitle: string;
  showButton?: boolean;
  targetIdentity?: string;
  positionLabel?: string;
  positionLabels?: ReadonlyMap<string, string>;
  onSnapshot?: (snapshot: OperationResponse) => void;
};

type DeletionState = {
  scopeGeneration: number;
  preview?: LibraryDeletionPreviewResponse;
  previewError?: string;
  previewPending: boolean;
  confirmationPending: boolean;
  confirmationUncertain: boolean;
  operation?: OperationResponse;
  actionError?: string;
};

function initialState(scopeGeneration: number): DeletionState {
  return {
    scopeGeneration,
    previewPending: false,
    confirmationPending: false,
    confirmationUncertain: false,
  };
}

type TargetScope = { key: string; generation: number };

const defaultInitialState: Omit<DeletionState, "scopeGeneration"> = {
  previewPending: false,
  confirmationPending: false,
  confirmationUncertain: false,
};

function responseError(
  data: { detail?: string },
  status: number,
  locale: string,
) {
  return redactIdentifiers(
    apiResponseError(
      { status, data },
      locale === "ru"
        ? `Запрос не выполнен (HTTP ${status}).`
        : `Request failed (HTTP ${status}).`,
    ).message,
    locale,
  );
}

function redactIdentifiers(value: string, locale = "ru") {
  return value.replace(
    /\b[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b/gi,
    locale === "ru" ? "идентификатор" : "identifier",
  );
}

function blockerLabel(code: string, message: string, locale: string) {
  const russian: Record<string, string> = {
    active_operation: "Для объекта уже выполняется операция.",
    publication_active: "Для позиции выполняется публикация.",
    deletion_active: "Удаление уже выполняется.",
    unavailable: "Объект сейчас нельзя удалить.",
  };
  const english: Record<string, string> = {
    active_operation: "An operation is already running for this object.",
    publication_active: "A publication is running for this position.",
    deletion_active: "A deletion is already in progress.",
    unavailable: "This object cannot currently be deleted.",
  };
  return (
    (locale === "ru" ? russian : english)[code] ??
    redactIdentifiers(message, locale)
  );
}

function deletionStageLabel(stage: string, locale: string) {
  const labels: Record<string, { ru: string; en: string }> = {
    queued: { ru: "В очереди", en: "Queued" },
    preparing: {
      ru: "Проверка области удаления",
      en: "Checking deletion scope",
    },
    deleting_publications: {
      ru: "Удаление публикаций",
      en: "Deleting publications",
    },
    deleting_library_rows: {
      ru: "Завершение удаления записей",
      en: "Finalizing library records",
    },
    succeeded: { ru: "Завершено", en: "Completed" },
    failed: { ru: "Ошибка", en: "Failed" },
  };
  return (
    labels[stage]?.[locale === "ru" ? "ru" : "en"] ??
    (locale === "ru" ? "Обработка удаления" : "Processing deletion")
  );
}

export function LibraryDeletionControl({
  target,
  releaseTitle,
  showButton = true,
  targetIdentity,
  positionLabel,
  positionLabels,
  onSnapshot,
}: Props) {
  const { locale } = useI18n();
  const targetKey =
    target.type === "artist"
      ? `artist:${target.artistId}`
      : target.type === "release"
        ? `release:${target.releaseId}`
        : `publication:${target.releaseId}:${target.positionId}`;
  const targetScope = useRef<TargetScope>({ key: targetKey, generation: 0 });
  if (targetScope.current.key !== targetKey) {
    targetScope.current = {
      key: targetKey,
      generation: targetScope.current.generation + 1,
    };
  }
  const requestScope = targetScope.current;
  const mounted = useRef(false);
  const [openScope, setOpenScope] = useState<TargetScope>();
  const open = openScope === requestScope;
  const setOpen = (nextOpen: boolean) =>
    setOpenScope(nextOpen ? requestScope : undefined);
  const [storedState, setStoredState] = useState(() =>
    initialState(requestScope.generation),
  );
  const state =
    storedState.scopeGeneration === requestScope.generation
      ? storedState
      : { ...defaultInitialState, scopeGeneration: requestScope.generation };
  const operationTargetIdentity = useRef(targetIdentity);
  const isCurrentScope = (scope: TargetScope) =>
    mounted.current && targetScope.current === scope;
  const updateState = (
    scope: TargetScope,
    update: (current: DeletionState) => DeletionState,
  ) => {
    if (!isCurrentScope(scope)) return;
    setStoredState((current) => {
      if (!isCurrentScope(scope)) return current;
      const base =
        current.scopeGeneration === scope.generation
          ? current
          : initialState(scope.generation);
      return update(base);
    });
  };
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (
      !targetIdentity ||
      targetIdentity === operationTargetIdentity.current ||
      state.operation?.state !== "succeeded" ||
      state.confirmationUncertain
    )
      return;
    operationTargetIdentity.current = targetIdentity;
    setStoredState((current) => {
      if (
        current.scopeGeneration !== requestScope.generation ||
        current.operation?.state !== "succeeded" ||
        current.confirmationUncertain
      )
        return current;
      return initialState(requestScope.generation);
    });
  }, [
    requestScope,
    state.confirmationUncertain,
    state.operation?.state,
    targetIdentity,
  ]);
  const operationId = state.operation?.id;
  const follower = useOperationFollower(operationId, {
    enabled: Boolean(
      operationId &&
        !["succeeded", "failed", "cancelled"].includes(
          state.operation?.state ?? "",
        ),
    ),
    initialSnapshot: state.operation,
    refreshOnStreamError: true,
    onSnapshot: (snapshot) => {
      if (!isCurrentScope(requestScope)) return;
      updateState(requestScope, (current) => ({
        ...current,
        operation: snapshot,
      }));
      onSnapshot?.(snapshot);
    },
    onError: (error) => {
      updateState(requestScope, (current) => ({
        ...current,
        actionError: sharedErrorMessage(
          error,
          locale === "ru"
            ? "Не удалось проверить состояние удаления."
            : "Could not check deletion status.",
        ),
      }));
    },
  });

  const text =
    locale === "ru"
      ? {
          deleteRelease: "Удалить релиз",
          deleteArtist: "Удалить артиста",
          deletePublication: "Удалить текущую публикацию",
          title:
            target.type === "artist"
              ? "Удаление артиста"
              : target.type === "release"
                ? "Удаление релиза"
                : "Удаление публикации",
          target:
            target.type === "publication"
              ? `${releaseTitle} · ${positionLabel ?? "Позиция"}`
              : releaseTitle,
          scopeArtist:
            "Будут удалены основные релизы артиста, их позиции и текущие публикации. Релизы с соисполнителями отмечены отдельно; ссылки на артиста в других объектах сохраняются как скрытые технические данные.",
          scopeRelease:
            "Будут удалены релиз, его позиции, текущие публикации и композиции, потерявшие последнюю связь с релизом. Композиции в других релизах, записи провайдера и исходники сохраняются.",
          scopePublication:
            "Будет удалена только текущая публикация. Позиция, релиз, композиция, записи провайдера, исходники и другие публикации сохраняются.",
          prepare: "Загрузить точный предпросмотр",
          refreshPreview: "Обновить предпросмотр",
          loading: "Подготовка предпросмотра…",
          cancel: "Отмена",
          confirm: "Подтвердить удаление",
          confirmAgain: "Повторить подтверждение того же предпросмотра",
          retry: "Повторить неудавшееся удаление",
          previewError: "Не удалось подготовить предпросмотр.",
          blocked: "Подтверждение недоступно: устраните блокирующие условия.",
          positions: "Позиции",
          artistReleases: "Основные релизы, включённые в удаление",
          coartistWarnings: "Релизы с соисполнителями",
          coartistNames: "Соисполнители",
          recordings: "Композиции, удаляемые из медиатеки",
          files: "Файлы текущих публикаций",
          outputs: "Будут затронуты файлы",
          blockers: "Блокирующие условия",
          capturable: "Файл доступен для безопасного удаления",
          notCapturable: "Файл не подтверждён как принадлежащий приложению",
          noOutputs: "Текущих публикаций для удаления нет.",
          frozen:
            "Предпросмотр и его подтверждение зафиксированы. Повтор не создаст новый предпросмотр и не изменит область удаления.",
          uncertain:
            "Ответ на подтверждение не получен. Результат неизвестен; повторите подтверждение того же предпросмотра, чтобы получить ту же операцию.",
          running:
            "Удаление выполняется. Обновления операции только пробуждают чтение актуального снимка.",
          partial:
            "Успешно удалённые публикации отражаются сразу; ошибка следующего файла не отменяет уже выполненное. Релиз остаётся до завершения удаления.",
          artistPartial:
            "Успешно удалённые публикации отражаются сразу. Контекст артиста и остаток операции сохранены; после устранения причины можно повторить удаление.",
          stage: "Этап",
          progress: "Обработано байт",
          succeeded: "Удаление завершено.",
          failed:
            "Удаление завершилось с ошибкой. Остаток сохранён; можно явно повторить эту же операцию.",
          queued: "Удаление в очереди.",
          cancelled: "Операция удаления отменена.",
        }
      : {
          deleteRelease: "Delete release",
          deleteArtist: "Delete artist",
          deletePublication: "Delete current publication",
          title:
            target.type === "artist"
              ? "Delete artist"
              : target.type === "release"
                ? "Delete release"
                : "Delete publication",
          target:
            target.type === "publication"
              ? `${releaseTitle} · ${positionLabel ?? "Position"}`
              : releaseTitle,
          scopeArtist:
            "The artist's primary releases, their positions, and current publications will be deleted. Releases with coartists are listed separately; references from other records remain as hidden technical data.",
          scopeRelease:
            "The release, its positions, current publications, and recordings losing their last release link will be deleted. Recordings in other releases, provider records, and source files are retained.",
          scopePublication:
            "Only the current publication will be deleted. The position, release, recording, provider records, sources, and other publications are retained.",
          prepare: "Load exact preview",
          refreshPreview: "Refresh preview",
          loading: "Preparing preview…",
          cancel: "Cancel",
          confirm: "Confirm deletion",
          confirmAgain: "Retry confirmation of the same preview",
          retry: "Retry failed deletion",
          previewError: "Could not prepare the preview.",
          blocked: "Confirmation is unavailable; resolve the blockers first.",
          positions: "Positions",
          artistReleases: "Primary releases included in deletion",
          coartistWarnings: "Releases with coartists",
          coartistNames: "Coartists",
          recordings: "Recordings removed from the library",
          files: "Current publication files",
          outputs: "Files affected",
          blockers: "Blockers",
          capturable: "File is verified as safe to delete",
          notCapturable: "File is not verified as application-owned",
          noOutputs: "There are no current publications to delete.",
          frozen:
            "The preview and confirmation are frozen. Retrying will not create a new preview or change the deletion scope.",
          uncertain:
            "No confirmation response was received. The result is unknown; retry this same preview to retrieve the same operation.",
          running:
            "Deletion is running. Operation notifications only trigger a fresh read of the authoritative snapshot.",
          partial:
            "Successfully deleted publications are reflected immediately; a later file error does not undo them. The release remains until deletion completes.",
          artistPartial:
            "Successfully deleted publications are reflected immediately. The artist context and residual operation are retained; retry the deletion after resolving the cause.",
          stage: "Stage",
          progress: "Bytes processed",
          succeeded: "Deletion completed.",
          failed:
            "Deletion failed. Remaining items are retained; you can explicitly retry this same operation.",
          queued: "Deletion queued.",
          cancelled: "Deletion was cancelled.",
        };

  async function preparePreview() {
    if (
      state.confirmationUncertain ||
      state.operation ||
      state.previewPending ||
      state.confirmationPending
    )
      return;
    const actionScope = requestScope;
    updateState(actionScope, (current) => ({
      ...current,
      previewPending: true,
      previewError: undefined,
      actionError: undefined,
    }));
    try {
      const response =
        target.type === "artist"
          ? await previewLibraryArtistDeletion(target.artistId, {
              cache: "no-store",
            })
          : target.type === "release"
            ? await previewLibraryReleaseDeletion(target.releaseId, {
                cache: "no-store",
              })
            : await previewLibraryPublicationDeletion(
                target.releaseId,
                target.positionId,
                { cache: "no-store" },
              );
      if (response.status !== 200)
        throw new Error(responseError(response.data, response.status, locale));
      if (!isCurrentScope(actionScope)) return;
      updateState(actionScope, (current) => ({
        ...current,
        preview: response.data,
        previewPending: false,
      }));
    } catch (error) {
      updateState(actionScope, (current) => ({
        ...current,
        previewPending: false,
        previewError: sharedErrorMessage(error, text.previewError),
      }));
    }
  }

  async function confirmPreview() {
    const preview = state.preview;
    if (
      !preview ||
      preview.blockers?.length ||
      preview.outputs?.some((output) => !output.capturable) ||
      state.previewPending ||
      state.confirmationPending ||
      state.operation
    )
      return;
    operationTargetIdentity.current = targetIdentity;
    const actionScope = requestScope;
    updateState(actionScope, (current) => ({
      ...current,
      confirmationPending: true,
      actionError: undefined,
    }));
    try {
      const response = await confirmLibraryDeletion({
        confirmation_digest: preview.confirmation_digest,
      });
      if (!isCurrentScope(actionScope)) return;
      if (response.status !== 202) {
        updateState(actionScope, (current) => ({
          ...current,
          confirmationPending: false,
          actionError: responseError(response.data, response.status, locale),
        }));
        return;
      }
      updateState(actionScope, (current) => ({
        ...current,
        confirmationPending: false,
        confirmationUncertain: false,
        operation: response.data,
      }));
      setOpen(false);
      onSnapshot?.(response.data);
    } catch (error) {
      updateState(actionScope, (current) => ({
        ...current,
        confirmationPending: false,
        confirmationUncertain: true,
        actionError: sharedErrorMessage(error, text.uncertain),
      }));
    }
  }

  async function retryFailedOperation() {
    const operation = state.operation;
    if (!operation || operation.state !== "failed") return;
    operationTargetIdentity.current = targetIdentity;
    const actionScope = requestScope;
    updateState(actionScope, (current) => ({
      ...current,
      confirmationPending: true,
      actionError: undefined,
    }));
    try {
      const response = await retryOperation(operation.id);
      if (!isCurrentScope(actionScope)) return;
      if (response.status !== 200)
        throw new Error(responseError(response.data, response.status, locale));
      if (response.data.id !== operation.id)
        throw new Error(
          locale === "ru"
            ? "Сервер вернул другую операцию; повтор остановлен."
            : "The server returned a different operation; retry was stopped.",
        );
      updateState(actionScope, (current) => ({
        ...current,
        confirmationPending: false,
        operation: response.data,
      }));
      follower.acceptSnapshot(response.data, "action", true);
      onSnapshot?.(response.data);
    } catch (error) {
      updateState(actionScope, (current) => ({
        ...current,
        confirmationPending: false,
        actionError: sharedErrorMessage(
          error,
          locale === "ru"
            ? "Не удалось повторить операцию удаления."
            : "Could not retry the deletion operation.",
        ),
      }));
    }
  }

  const preview = state.preview;
  const isBlocked = Boolean(
    preview?.blockers?.length ||
      preview?.outputs?.some((output) => !output.capturable),
  );
  const terminal =
    state.operation &&
    ["succeeded", "failed", "cancelled"].includes(state.operation.state);
  const buttonLabel =
    target.type === "artist"
      ? text.deleteArtist
      : target.type === "release"
        ? text.deleteRelease
        : text.deletePublication;

  return (
    <>
      {!state.operation && showButton ? (
        <AppButton
          className="border-[var(--error)] text-[var(--error)] hover:bg-[var(--error-soft)]"
          onPress={() => setOpen(true)}
        >
          {buttonLabel}
        </AppButton>
      ) : null}
      {state.operation ? (
        <section
          className="mt-3 space-y-2 rounded border border-[var(--line)] p-3"
          aria-label={text.title}
        >
          <p role="status">
            {state.operation.state === "succeeded"
              ? text.succeeded
              : state.operation.state === "failed"
                ? text.failed
                : state.operation.state === "cancelled"
                  ? text.cancelled
                  : state.operation.state === "queued"
                    ? text.queued
                    : text.running}
          </p>
          {target.type !== "publication" &&
          state.operation.state !== "succeeded" ? (
            <p>
              {target.type === "artist" ? text.artistPartial : text.partial}
            </p>
          ) : null}
          {!terminal ? (
            <dl className="grid gap-x-3 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
              <dt className="text-[var(--muted)]">{text.stage}</dt>
              <dd className="break-words">
                {deletionStageLabel(state.operation.stage, locale)}
              </dd>
              <dt className="text-[var(--muted)]">{text.progress}</dt>
              <dd className="space-y-1">
                <span>
                  {state.operation.bytes_completed.toLocaleString(locale)}
                  {state.operation.bytes_total == null
                    ? ""
                    : ` / ${state.operation.bytes_total.toLocaleString(locale)}`}
                </span>
                {state.operation.bytes_total != null &&
                state.operation.bytes_total > 0 ? (
                  <progress
                    className="block w-full"
                    aria-label={text.progress}
                    max={state.operation.bytes_total}
                    value={Math.min(
                      state.operation.bytes_completed,
                      state.operation.bytes_total,
                    )}
                  />
                ) : null}
              </dd>
            </dl>
          ) : null}
          {state.operation.state === "failed" ? (
            <>
              {state.operation.safe_error ? (
                <p role="alert">
                  {redactIdentifiers(state.operation.safe_error, locale)}
                </p>
              ) : null}
              <AppButton
                isDisabled={state.confirmationPending}
                onPress={() => void retryFailedOperation()}
              >
                {state.confirmationPending ? text.loading : text.retry}
              </AppButton>
            </>
          ) : null}
          {state.actionError ? <p role="alert">{state.actionError}</p> : null}
        </section>
      ) : null}
      <ModalOverlay
        isOpen={open}
        onOpenChange={(nextOpen) => {
          if (
            !state.confirmationPending &&
            !(state.confirmationUncertain && nextOpen === false)
          )
            setOpen(nextOpen);
        }}
        isDismissable={
          !state.confirmationPending && !state.confirmationUncertain
        }
        className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/45 p-3 sm:p-6"
      >
        <Modal className="flex max-h-[min(70dvh,700px)] w-full max-w-xl flex-col overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_16px_48px_#0003]">
          <Dialog
            aria-labelledby="library-deletion-title"
            className="flex min-h-0 min-w-0 flex-1 flex-col wrap-anywhere outline-none"
          >
            <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-6">
              <Heading
                slot="title"
                id="library-deletion-title"
                className="text-xl font-semibold"
              >
                {text.title}
              </Heading>
              <p className="wrap-anywhere font-medium">{text.target}</p>
              <p>
                {target.type === "release"
                  ? text.scopeRelease
                  : target.type === "artist"
                    ? text.scopeArtist
                    : text.scopePublication}
              </p>
              {state.confirmationUncertain ? (
                <p role="alert">{text.uncertain}</p>
              ) : null}
              {preview ? (
                <div className="space-y-3 rounded border border-[var(--line)] p-3">
                  <p className="wrap-anywhere">{preview.title}</p>
                  {target.type === "artist" &&
                  preview.artist_releases?.length ? (
                    <section aria-label={text.artistReleases}>
                      <h3 className="font-semibold">{text.artistReleases}</h3>
                      <ul className="list-disc pl-5">
                        {preview.artist_releases.map((release) => (
                          <li key={release.title}>{release.title}</li>
                        ))}
                      </ul>
                    </section>
                  ) : null}
                  {target.type === "artist" &&
                  preview.coartist_warnings?.length ? (
                    <section aria-label={text.coartistWarnings} role="note">
                      <h3 className="font-semibold">{text.coartistWarnings}</h3>
                      <ul className="list-disc space-y-1 pl-5">
                        {preview.coartist_warnings.map((warning) => (
                          <li
                            key={`${warning.release_title}:${(warning.artist_names ?? []).join("\u0000")}`}
                          >
                            <span className="font-medium">
                              {warning.release_title}
                            </span>
                            {warning.artist_names?.length ? (
                              <span>
                                {` — ${text.coartistNames}: ${warning.artist_names.join(", ")}`}
                              </span>
                            ) : null}
                          </li>
                        ))}
                      </ul>
                    </section>
                  ) : null}
                  <dl className="grid gap-x-3 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
                    <dt>{text.positions}</dt>
                    <dd>
                      {(preview.position_ids ?? []).length.toLocaleString(
                        locale,
                      )}
                    </dd>
                    <dt>{text.files}</dt>
                    <dd>
                      {(preview.outputs ?? []).length.toLocaleString(locale)}
                    </dd>
                    {target.type !== "publication" ? (
                      <>
                        <dt>{text.recordings}</dt>
                        <dd>
                          {preview.recording_count.toLocaleString(locale)}
                        </dd>
                      </>
                    ) : null}
                  </dl>
                  {preview.outputs?.length ? (
                    <>
                      <h3 className="font-semibold">{text.outputs}</h3>
                      <ul className="max-h-56 space-y-2 overflow-y-auto pl-5">
                        {preview.outputs.map((output) => (
                          <li
                            className="break-all"
                            key={`${output.position_id}:${output.relative_path}`}
                          >
                            <span className="font-medium">
                              {positionLabels?.get(output.position_id) ??
                                (target.type === "publication" &&
                                output.position_id === target.positionId
                                  ? positionLabel
                                  : undefined) ??
                                (locale === "ru" ? "Позиция" : "Position")}
                            </span>
                            <code className="block text-sm">
                              {output.relative_path}
                            </code>
                            <span className="text-sm text-[var(--muted)]">
                              {output.capturable
                                ? text.capturable
                                : text.notCapturable}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </>
                  ) : (
                    <p>{text.noOutputs}</p>
                  )}
                  {preview.blockers?.length ? (
                    <div role="alert">
                      <h3 className="font-semibold">{text.blockers}</h3>
                      <ul className="list-disc pl-5">
                        {preview.blockers.map((blocker, index) => (
                          <li
                            key={`${blocker.code}:${blocker.position_id ?? index}`}
                          >
                            {blocker.position_id
                              ? `${positionLabels?.get(blocker.position_id) ?? (locale === "ru" ? "Позиция" : "Position")}: `
                              : ""}
                            {blockerLabel(
                              blocker.code,
                              blocker.message,
                              locale,
                            )}
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {state.confirmationUncertain ? <p>{text.frozen}</p> : null}
                </div>
              ) : null}
              {state.previewError ? (
                <p role="alert">{state.previewError}</p>
              ) : null}
              {state.actionError && !state.confirmationUncertain ? (
                <p role="alert">{state.actionError}</p>
              ) : null}
              {isBlocked ? <p role="status">{text.blocked}</p> : null}
            </div>
            <div className="sticky bottom-0 z-10 flex shrink-0 flex-col-reverse flex-wrap gap-2 border-t border-[var(--line)] bg-[var(--surface)] p-4 sm:flex-row sm:justify-end sm:px-6">
              <AppButton
                isDisabled={
                  state.confirmationPending || state.confirmationUncertain
                }
                onPress={() => setOpen(false)}
              >
                {text.cancel}
              </AppButton>
              {!preview ? (
                <AppButton
                  isDisabled={
                    state.previewPending || state.confirmationUncertain
                  }
                  onPress={() => void preparePreview()}
                >
                  {state.previewPending ? text.loading : text.prepare}
                </AppButton>
              ) : (
                <>
                  {!state.operation && !state.confirmationUncertain ? (
                    <AppButton
                      isDisabled={
                        state.previewPending || state.confirmationPending
                      }
                      onPress={() => void preparePreview()}
                    >
                      {state.previewPending
                        ? text.loading
                        : text.refreshPreview}
                    </AppButton>
                  ) : null}
                  <AppButton
                    className="border-[var(--error)] text-[var(--error)] hover:bg-[var(--error-soft)]"
                    isDisabled={
                      isBlocked ||
                      state.previewPending ||
                      state.confirmationPending
                    }
                    onPress={() => void confirmPreview()}
                  >
                    {state.confirmationPending
                      ? text.loading
                      : state.confirmationUncertain
                        ? text.confirmAgain
                        : text.confirm}
                  </AppButton>
                </>
              )}
            </div>
          </Dialog>
        </Modal>
      </ModalOverlay>
    </>
  );
}
