import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  getOperation,
  listOperations,
  retryOperation,
  startSourceScan,
} from "../../api/generated/client";
import type {
  OperationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { message } from "./sourcesApi";

// The stage text is the whole progress report: bytes_completed is a byte count,
// not a file count, and a scan shows no percentage before the tree is read.
function stageText(operation: OperationResponse): string {
  if (operation.state === "queued") return "Сканирование поставлено в очередь.";
  const stage = operation.stage.replace(/^retry:/, "");
  if (stage === "traversing")
    return "Обход каталога: чтение дерева и проверка аудиопотока файлов.";
  if (stage === "applying")
    return "Применение результатов: подтверждённый инвентарь сохраняется.";
  return `Сканирование выполняется (этап сервера: ${stage}).`;
}

function scanFailure(response: {
  status: number;
  data?: { detail?: string };
}): Error {
  if (response.status === 409)
    return new Error(
      "Сканирование уже выполняется: сервер отклонил повторный запуск. Дождитесь завершения текущего сканирования и повторите.",
    );
  if (response.status === 404)
    return new Error("Каталог не найден: возможно, его удалили в другом окне.");
  if (response.status === 400)
    return new Error(
      "Сервер отклонил сканирование: каталог выключен, недоступен или настройка ещё не завершена.",
    );
  return new Error(
    response.data?.detail || `Сервер вернул ошибку ${response.status}.`,
  );
}

// Opening the root page must adopt the scan the server already runs for it:
// listOperations is the only source that names that operation, and its id is
// the handle the existing SSE stream and REST snapshot attach to. Historical
// scans, other roots and install/move operations are never a match.
const activeScanStates = ["queued", "running"];

function activeScanForRoot(
  operations: OperationResponse[] | null,
  rootId: string,
): OperationResponse | undefined {
  return (operations || []).find(
    (operation) =>
      operation.kind === "scan_source" &&
      operation.target_source_root_id === rootId &&
      activeScanStates.includes(operation.state),
  );
}

export type SourceScanControlProps = {
  root: SourceRootResponse;
  onScanCompleted: () => void | Promise<void>;
};

export function SourceScanControl(props: SourceScanControlProps) {
  // A new root is a new scan: the key resets this control's state and unmounts
  // the previous stream and snapshot request instead of carrying them over.
  return <SourceScanView key={props.root.id} {...props} />;
}

function SourceScanView({ root, onScanCompleted }: SourceScanControlProps) {
  const [operation, setOperation] = useState<OperationResponse>();
  const [starting, setStarting] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [actionError, setActionError] = useState("");
  const [streamError, setStreamError] = useState("");
  const [discovering, setDiscovering] = useState(true);
  const [discoveryError, setDiscoveryError] = useState("");
  const startInFlight = useRef(false);
  const retryInFlight = useRef(false);
  const reportedSuccess = useRef("");
  const completed = useRef(onScanCompleted);
  useEffect(() => {
    completed.current = onScanCompleted;
  });

  const applySnapshot = useCallback((snapshot: OperationResponse) => {
    setOperation(snapshot);
    if (
      snapshot.state === "succeeded" &&
      reportedSuccess.current !== snapshot.id
    ) {
      reportedSuccess.current = snapshot.id;
      void completed.current();
    }
  }, []);

  // Discovery owns the window between opening the page and knowing whether a
  // scan is already active: the Scan button stays disabled until it answers,
  // so no redundant POST can race an operation the server is already running.
  useEffect(() => {
    const request = new AbortController();
    const discover = async () => {
      setDiscovering(true);
      setDiscoveryError("");
      try {
        const response = await listOperations(undefined, {
          signal: request.signal,
          cache: "no-store",
        });
        if (request.signal.aborted) return;
        if (response.status !== 200) {
          setDiscoveryError(
            `Не удалось проверить активное сканирование каталога (код ${response.status}). Повторный запуск сервер отклонит, если сканирование уже идёт.`,
          );
          return;
        }
        const adopted = activeScanForRoot(response.data.operations, root.id);
        if (adopted) applySnapshot(adopted);
      } catch (reason) {
        if (!request.signal.aborted)
          setDiscoveryError(
            `Не удалось проверить активное сканирование каталога: ${message(reason)}`,
          );
      } finally {
        if (!request.signal.aborted) setDiscovering(false);
      }
    };
    void discover();
    return () => request.abort();
  }, [root.id, applySnapshot]);

  const operationId = operation?.id;
  useEffect(() => {
    if (!operationId) return;
    const id = operationId;
    let request: AbortController | undefined;
    const read = async () => {
      request?.abort();
      const current = new AbortController();
      request = current;
      try {
        const response = await getOperation(id, {
          signal: current.signal,
          cache: "no-store",
        });
        if (current.signal.aborted) return;
        if (response.status !== 200) {
          setStreamError(
            `Не удалось перечитать состояние операции (${response.status}).`,
          );
          return;
        }
        applySnapshot(response.data);
      } catch (reason) {
        if (!current.signal.aborted) setStreamError(message(reason));
      }
    };
    // A stream event is a wake-up only; the payload is never a snapshot. A
    // snapshot delivered by the stream is the proof it reconnected, so the
    // error clears on that delivery and not on this control's own re-read.
    const unsubscribe = subscribeToOperation(
      id,
      (snapshot) => {
        setStreamError("");
        applySnapshot(snapshot);
      },
      (reason) => {
        setStreamError(reason.message);
        void read();
      },
    );
    void read();
    return () => {
      unsubscribe();
      request?.abort();
    };
  }, [operationId, applySnapshot]);

  async function startScan() {
    if (startInFlight.current) return;
    startInFlight.current = true;
    setStarting(true);
    setActionError("");
    setDiscoveryError("");
    try {
      const response = await startSourceScan(root.id);
      if (response.status !== 200) throw scanFailure(response);
      applySnapshot(response.data);
    } catch (reason) {
      setActionError(message(reason));
    } finally {
      startInFlight.current = false;
      setStarting(false);
    }
  }

  async function retryScan() {
    if (!operation || retryInFlight.current) return;
    retryInFlight.current = true;
    setRetrying(true);
    setActionError("");
    try {
      const response = await retryOperation(operation.id);
      if (response.status !== 200) throw scanFailure(response);
      applySnapshot(response.data);
    } catch (reason) {
      setActionError(message(reason));
    } finally {
      retryInFlight.current = false;
      setRetrying(false);
    }
  }

  const pending =
    operation?.state === "queued" || operation?.state === "running";
  const busy = starting || retrying;
  const failure =
    actionError ||
    (operation?.state === "failed"
      ? operation.safe_error ||
        "Сканирование не завершилось. Повторите попытку."
      : "");

  return (
    <section aria-labelledby="source-scan-title" className="sources-panel">
      <h2 id="source-scan-title">Сканирование</h2>
      <p className="sources-help">
        Сканирование запускается только вручную. Число файлов заранее
        неизвестно, поэтому показываются этапы сервера, без процентов и
        счётчиков байтов.
      </p>
      {!root.enabled && (
        <p className="sources-note">
          Каталог выключен: новые сканирования запрещены, пока каталог не
          включён.
        </p>
      )}
      <AppButton
        isDisabled={!root.enabled || busy || pending || discovering}
        onPress={() => void startScan()}
      >
        Сканировать
      </AppButton>
      {discovering && (
        <p role="status">Проверяем активное сканирование этого каталога…</p>
      )}
      {pending && operation && <p role="status">{stageText(operation)}</p>}
      {operation?.state === "succeeded" && (
        <p role="status">
          Сканирование завершено: инвентарь обновлён последним успешным обходом.
        </p>
      )}
      {operation?.state === "failed" && (
        <AppButton isDisabled={busy} onPress={() => void retryScan()}>
          Повторить сканирование
        </AppButton>
      )}
      {operation && streamError && (
        <p role="status">
          Обновления по потоку событий прерваны: показано состояние из
          последнего ответа сервера. Соединение восстановится автоматически.
        </p>
      )}
      {discoveryError && <p role="status">{discoveryError}</p>}
      {failure && <p role="alert">{failure}</p>}
    </section>
  );
}
