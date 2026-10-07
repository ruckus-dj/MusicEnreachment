import { useEffect, useRef } from "react";
import { Link } from "react-aria-components";
import type { SourceAnalysisStepResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { SourceAnalysisSteps } from "./SourceAnalysisSteps";
import { statusLabel } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";
import "./sources.css";

const stepNames = ["sha256", "probe", "fingerprint"] as const;
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
                <dt>Режим источника</dt>
                <dd>Только чтение · in-place</dd>
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
                {operation?.stage === "applying"
                  ? "Сохранение результата этапа анализа."
                  : operation?.stage === "probing"
                    ? "Чтение технических данных ffprobe."
                    : "Этап анализа поставлен в очередь."}
              </p>
            )}
            {streamError && pending && (
              <p role="status">
                Поток событий прерван. Соединение восстановится автоматически;
                показан последний ответ сервера.
              </p>
            )}
          </section>
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
