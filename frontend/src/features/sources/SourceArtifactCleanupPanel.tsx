import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { AppButton } from "../../components/AppButton";
import { useSourceArtifactCleanup } from "./useSourceArtifactCleanup";

const itemStateLabel: Record<string, string> = {
  cleanup_eligible: "Ожидает очистки",
  cleanup_failed: "Предыдущая очистка не удалась",
  deleted: "Удалён",
  missing: "Файл уже отсутствует",
  failed: "Не удалось удалить",
  claimed: "Выполняется",
};

export function SourceArtifactCleanupPanel() {
  const cleanup = useSourceArtifactCleanup();
  const [confirmation, setConfirmation] = useState<string[] | undefined>();
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLElement | null>(null);
  const alert = useRef<HTMLParagraphElement>(null);

  useLayoutEffect(() => {
    if (!confirmation || !dialog.current) return;
    if (!dialog.current.open) dialog.current.showModal();
    dialog.current.querySelector("button")?.focus();
  }, [confirmation]);

  useEffect(() => {
    if (cleanup.error) alert.current?.focus();
  }, [cleanup.error]);

  function closeConfirmation() {
    if (dialog.current?.open) dialog.current.close();
    else {
      setConfirmation(undefined);
      trigger.current?.focus();
      trigger.current = null;
    }
  }

  const operation = cleanup.operation;
  const visibleIds = new Set(
    cleanup.candidates.map(({ artifact_id }) => artifact_id),
  );
  const selectedNotInBatch = cleanup.selected.filter(
    (id) => !visibleIds.has(id),
  );

  return (
    <section aria-labelledby="artifact-cleanup-title" className="sources-panel">
      <h2 id="artifact-cleanup-title">Временные копии анализа</h2>
      <p>
        Здесь показана ограниченная сервером подборка копий, для которых
        разрешена явная очистка. Исходные файлы не затрагиваются.
      </p>

      {cleanup.loading && cleanup.batchCount === undefined && (
        <p role="status">Загрузка доступных копий…</p>
      )}
      {cleanup.error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {cleanup.error}
          </p>
          <AppButton onPress={() => void cleanup.load()}>
            Обновить подборку
          </AppButton>
        </>
      )}
      {cleanup.batchCount !== undefined && (
        <p role="status">
          В текущей подборке: {cleanup.batchCount}. Это количество записей в
          возвращённом пакете, а не общее число копий или лимит удаления.
          Максимум в пакете — 1000.
        </p>
      )}
      {cleanup.loading && cleanup.batchCount !== undefined && (
        <p role="status">Обновление подборки…</p>
      )}

      {cleanup.batchCount !== undefined && cleanup.candidates.length === 0 ? (
        <p>Нет копий, доступных для очистки.</p>
      ) : (
        cleanup.candidates.length > 0 && (
          <>
            <ul aria-label="Копии, доступные для очистки">
              {cleanup.candidates.map((candidate) => (
                <li key={candidate.artifact_id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={cleanup.selected.includes(candidate.artifact_id)}
                      disabled={cleanup.isPending || cleanup.busy}
                      onChange={() => cleanup.toggle(candidate.artifact_id)}
                    />{" "}
                    <code>{candidate.relative_path}</code> —{" "}
                    {itemStateLabel[candidate.state] ?? candidate.state}
                  </label>
                </li>
              ))}
            </ul>
            <AppButton
              isDisabled={
                cleanup.selected.length === 0 ||
                cleanup.isPending ||
                cleanup.busy
              }
              onPress={(event) => {
                trigger.current = event.target as HTMLElement;
                setConfirmation([...cleanup.selected]);
              }}
            >
              Очистить выбранные ({cleanup.selected.length})
            </AppButton>
          </>
        )
      )}

      {selectedNotInBatch.length > 0 && (
        <section aria-label="Выбранные копии вне текущей подборки">
          <p>
            Эти выбранные идентификаторы больше не входят в текущую подборку;
            они не заменены другими копиями:
          </p>
          <ul>
            {selectedNotInBatch.map((id) => (
              <li key={id}>
                <code>{id}</code>{" "}
                <AppButton
                  isDisabled={cleanup.isPending || cleanup.busy}
                  onPress={() => cleanup.toggle(id)}
                >
                  Снять выбор
                </AppButton>
              </li>
            ))}
          </ul>
        </section>
      )}

      {operation && (
        <section aria-labelledby="artifact-cleanup-operation-title">
          <h3 id="artifact-cleanup-operation-title">Очистка копий</h3>
          {cleanup.isPending ? (
            <p role="status">
              Операция {operation.state}: {operation.stage}.
            </p>
          ) : (
            <p role="status">
              Операция завершена: {operation.state}.
              {operation.safe_error ? ` ${operation.safe_error}` : ""}
            </p>
          )}
          {cleanup.streamError && cleanup.isPending && (
            <p role="status">
              Поток обновлений прерван; клиент продолжает ждать актуальный
              снимок операции.
            </p>
          )}
          {cleanup.results && cleanup.results.length > 0 && (
            <ul aria-label="Результаты очистки">
              {cleanup.results.map((result) => (
                <li key={result.artifact_id}>
                  <code>{result.artifact_id}</code>:{" "}
                  {itemStateLabel[result.state] ?? result.state}
                  {result.safe_error ? ` — ${result.safe_error}` : ""}
                </li>
              ))}
            </ul>
          )}
          {!cleanup.isPending && !cleanup.results?.length && (
            <p>
              Сервер не вернул результаты по отдельным копиям; состояние
              удаления неизвестно.
            </p>
          )}
        </section>
      )}

      {confirmation && (
        <dialog
          ref={dialog}
          aria-modal="true"
          aria-labelledby="artifact-cleanup-confirm-title"
          className="sources-dialog"
          onClose={() => {
            setConfirmation(undefined);
            trigger.current?.focus();
            trigger.current = null;
          }}
          onCancel={(event) => {
            event.preventDefault();
            closeConfirmation();
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              closeConfirmation();
            }
          }}
        >
          <h2 id="artifact-cleanup-confirm-title">Очистить выбранные копии?</h2>
          <p>
            Будут отправлены только эти {confirmation.length} идентификатора из
            показанного снимка списка:
          </p>
          <ul>
            {confirmation.map((id) => (
              <li key={id}>
                <code>{id}</code>
              </li>
            ))}
          </ul>
          <p>Файлы источника не удаляются.</p>
          <AppButton onPress={closeConfirmation}>Отмена</AppButton>
          <AppButton
            isDisabled={cleanup.busy}
            onPress={() => {
              const ids = confirmation;
              setConfirmation(undefined);
              if (dialog.current?.open) dialog.current.close();
              void cleanup.start(ids);
            }}
          >
            Подтвердить очистку
          </AppButton>
        </dialog>
      )}
    </section>
  );
}
