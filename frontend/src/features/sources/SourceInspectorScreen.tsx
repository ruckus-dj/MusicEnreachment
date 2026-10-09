import { useEffect, useRef } from "react";
import { Link } from "react-aria-components";
import type {
  SourceAnalysisStepResponse,
  SourceStagedArtifactResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { SourceAnalysisSteps } from "./SourceAnalysisSteps";
import { statusLabel } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";
import "./sources.css";

const stepNames = ["sha256", "probe", "fingerprint", "metadata"] as const;
type StepName = (typeof stepNames)[number];

export function SourceInspectorScreen({
  sourceId,
  locationId,
}: {
  readonly sourceId: string;
  readonly locationId: string;
}) {
  const inspector = useSourceInspector(sourceId, locationId);
  const { detail, operation, loading, busy, pending, error, streamError } =
    inspector;
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  const stepsByName = new Map(
    (detail?.steps ?? []).map((current) => [current.name, current]),
  );
  const getStep = (name: StepName): SourceAnalysisStepResponse | undefined =>
    stepsByName.get(name);
  const steps = Object.fromEntries(
    stepNames.map((name) => {
      const current = getStep(name);
      return [
        name,
        {
          // Absent work is not a loading state: the API has no requested step yet.
          state: current?.state ?? "not_requested",
          safeError: current?.safe_error,
          skipReason: current?.skip_reason,
        },
      ];
    }),
  ) as Parameters<typeof SourceAnalysisSteps>[0]["steps"];
  const rootAvailable =
    detail?.root.enabled === true &&
    !detail.root.stale &&
    detail.root.status === "available";
  const active = pending || !!detail?.active_analysis_operation_id;
  const retryAvailable = Object.fromEntries(
    stepNames.map((name) => [
      name,
      rootAvailable && !active && getStep(name)?.state === "failed",
    ]),
  ) as Record<StepName, boolean>;
  const sha256 = getStep("sha256")?.sha256;
  const fingerprint = getStep("fingerprint")?.fingerprint;
  const metadata = getStep("metadata")?.metadata;
  const fingerprintStepState = getStep("fingerprint")?.state;
  // The service only reruns a fingerprint whose latest step succeeded and whose
  // stored result was produced by a different active fpcalc version. Mirror that
  // exactly instead of enabling a rerun for any retained fingerprint.
  const activeFingerprintVersion = detail?.active_fpcalc_version;
  const fingerprintRerunAvailable =
    rootAvailable &&
    !!fingerprint &&
    fingerprintStepState === "succeeded" &&
    !!activeFingerprintVersion &&
    activeFingerprintVersion !== fingerprint.version &&
    !active &&
    !busy &&
    !loading &&
    !error;

  return (
    <section
      className="sources-screen sources-inspector mx-auto max-w-5xl py-3"
      aria-labelledby="inspector-title"
    >
      <nav aria-label="Путь к файлу" className="sources-breadcrumb">
        <Link href="#/sources">Источники</Link>
        <span aria-hidden="true">/</span>
        <Link href={`#/sources/${encodeURIComponent(sourceId)}`}>
          Каталог источника
        </Link>
        <span aria-hidden="true">/</span>
        <span>Файл</span>
      </nav>
      <h1 id="inspector-title" ref={heading} tabIndex={-1}>
        Инспектор файла
      </h1>
      {loading && (
        <p role="status">Загрузка файла и проверка активного анализа…</p>
      )}
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
      {error && (
        <AppButton
          isDisabled={loading || busy}
          onPress={() => void inspector.load()}
        >
          Повторить загрузку
        </AppButton>
      )}
      {detail && (
        <>
          <section className="sources-panel" aria-labelledby="identity-title">
            <h2 id="identity-title">Исходный файл</h2>
            <dl className="sources-summary">
              <div>
                <dt>Относительный путь</dt>
                <dd>
                  <code>{detail.relative_path}</code>
                </dd>
              </div>
              <div>
                <dt>Путь инвентаря</dt>
                <dd>
                  <code>{detail.root.inventory_path ?? "Неизвестно"}</code>
                </dd>
              </div>
              <div>
                <dt>Размер</dt>
                <dd>
                  {new Intl.NumberFormat("ru-RU").format(detail.size_bytes)} Б
                </dd>
              </div>
              <div>
                <dt>Изменён</dt>
                <dd>{new Date(detail.mtime).toLocaleString()}</dd>
              </div>
              <div>
                <dt>Воздействие анализа</dt>
                <dd>Анализ не изменяет исходный файл</dd>
              </div>
              <div>
                <dt>Доступность каталога</dt>
                <dd>{statusLabel(detail.root.status)}</dd>
              </div>
            </dl>
            {!detail.root.enabled && (
              <p className="sources-note">
                Каталог выключен: анализ недоступен.
              </p>
            )}
            {detail.root.stale && (
              <p className="sources-note">
                Инвентарь устарел: сначала сканируйте текущий путь каталога.
              </p>
            )}
            {detail.root.status === "unavailable" && (
              <p className="sources-note">
                Каталог недоступен. Сохранённый результат остаётся доступен.{" "}
                {detail.root.safe_error}
              </p>
            )}
          </section>
          <section className="sources-panel" aria-labelledby="analysis-title">
            <h2 id="analysis-title">Технический анализ</h2>
            {busy && <p role="status">Выполняется запрос…</p>}
            {pending && (
              <p role="status">
                {operation?.state === "queued"
                  ? "Этап анализа поставлен в очередь."
                  : operation?.stage === "applying"
                    ? "Сохранение результата этапа анализа."
                    : "Выполняется этап анализа файла."}
              </p>
            )}
            {streamError && pending && (
              <p role="status">
                Поток событий прерван. Соединение восстановится автоматически;
                показан последний ответ сервера.
              </p>
            )}
          </section>
          <StagedArtifactStatus artifact={detail.staged_artifact} />
          <SourceAnalysisSteps
            steps={steps}
            sha256={
              sha256 && {
                value: sha256.value,
                provenance: getStep("sha256")?.reuse_origin,
              }
            }
            probeResult={detail.result}
            probeProvenance={getStep("probe")?.reuse_origin}
            fingerprint={
              fingerprint && {
                value: fingerprint.value,
                version: fingerprint.version,
                provenance: getStep("fingerprint")?.reuse_origin,
              }
            }
            metadata={
              metadata && {
                tags: metadata.tags,
                provenance: metadata.provenance,
                nativeMatroska: metadata.native_matroska,
              }
            }
            retryAvailable={retryAvailable}
            onRetry={(name) => void inspector.action("retry", name)}
            fingerprintRerunAvailable={fingerprintRerunAvailable}
            onRerunFingerprint={() => void inspector.action("rerun")}
            activeFingerprintVersion={activeFingerprintVersion}
            matching={{
              eligible: detail.matching_eligible,
              reason: !detail.matching_eligible
                ? detail.result?.streams?.length === 0
                  ? "Не поддерживается для файла без аудиопотоков"
                  : detail.result?.streams && detail.result.streams.length > 1
                    ? "Не поддерживается для файла с несколькими аудиопотоками"
                    : undefined
                : undefined,
            }}
          />
        </>
      )}
    </section>
  );
}

function StagedArtifactStatus({
  artifact,
}: {
  readonly artifact?: SourceStagedArtifactResponse;
}) {
  const stateLabels = {
    unknown: "Неизвестно",
    preparation: "Подготовка",
    acquiring: "Получение артефакта",
    ready: "Готов",
    retained: "Сохранён",
    cleanup_eligible: "Ожидает очистки",
    cleanup_failed: "Ошибка очистки",
  } as const;
  // The current server always sends an explicit projection, but legacy or
  // malformed payloads may omit it or leave fields out. Absent registry facts
  // stay explicitly unknown instead of being cast into a shape the server did
  // not send, and nullable requested_steps are read safely.
  const state = artifact?.state ?? "unknown";
  const stateLabel: string = stateLabels[state] ?? stateLabels.unknown;
  const requestedSteps = artifact?.requested_steps ?? [];
  const requestedStepsKnown = artifact?.requested_steps_known === true;
  const reusedFromDifferentOperation =
    !!artifact?.creator_operation_id &&
    !!artifact?.borrower_operation_id &&
    artifact.creator_operation_id !== artifact.borrower_operation_id;

  return (
    <section className="sources-panel" aria-labelledby="staged-artifact-title">
      <h2 id="staged-artifact-title">Промежуточный артефакт анализа</h2>
      <p>Состояние: {stateLabel}</p>
      {state === "ready" && (
        <p className="sources-note">
          Артефакт зарегистрирован как готовый; это не подтверждает наличие
          файла на диске.
        </p>
      )}
      {state === "unknown" && (
        <p className="sources-note">
          Сведения о промежуточном артефакте неизвестны.
        </p>
      )}
      {requestedStepsKnown ? (
        <>
          <p>
            Запрошенные этапы:{" "}
            {requestedSteps.length > 0 ? requestedSteps.join(", ") : "Нет"}
          </p>
          {requestedSteps.length === 0 && (
            <p className="sources-note">
              Пустой список запрошенных этапов не означает, что этапы завершены.
            </p>
          )}
        </>
      ) : (
        <p className="sources-note">
          История запрошенных этапов неизвестна; пустой список не означает, что
          этапы завершены или не запрашивались.
        </p>
      )}
      {reusedFromDifferentOperation && (
        <p className="sources-note">
          Промежуточный артефакт создан другой операцией и используется текущей
          операцией.
        </p>
      )}
      {state === "cleanup_failed" && artifact?.safe_error && (
        <p role="alert">
          Очистить промежуточный артефакт не удалось: {artifact.safe_error}
        </p>
      )}
    </section>
  );
}
