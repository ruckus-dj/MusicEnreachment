import { useEffect, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import { listInstallations } from "../../api/generated/client";
import type {
  InstallationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import "./SetupSummary.css";

function formatName(format: string) {
  if (format === "source") return "Исходный формат";
  if (format === "mka") return "MKA";
  if (format === "m4a") return "M4A";
  return "Не выбран";
}

function formatExplanation(format: string) {
  if (format === "source")
    return "Сохраняется исходный контейнер и аудиопоток.";
  if (format === "mka") return "Перепаковка в MKA без перекодирования аудио.";
  if (format === "m4a")
    return "AAC и ALAC копируются без перекодирования; другие lossless-кодеки преобразуются в ALAC, остальные — в AAC с потерями. Возможность обработки зависит от поддерживаемых FFmpeg кодеков и параметров исходного аудио.";
  return "";
}

function ToolStatus({
  id,
  installations,
  loading,
}: {
  id: string;
  installations: InstallationResponse[];
  loading: boolean;
}) {
  const tool = installations.find((item) => item.id === id);
  const version = tool?.version;
  const ready =
    tool?.active === true &&
    tool.state === "ready" &&
    Boolean(tool.verified_at) &&
    Boolean(version);
  if (loading) return <span>Загружается…</span>;
  if (!tool) return <span>Состояние неизвестно — обновите проверку</span>;
  if (!ready)
    return <span>Не подтверждён как готовый (состояние: {tool.state})</span>;
  return <span>Готов · {version}</span>;
}

export function SetupSummary({ state }: { state: SetupStateBody }) {
  const [installations, setInstallations] = useState<InstallationResponse[]>(
    [],
  );
  const [loading, setLoading] = useState(true);
  const [inventoryError, setInventoryError] = useState("");
  const ffmpegID = state.settings.active_ffmpeg_installation_id || "";
  const fpcalcID = state.settings.active_fpcalc_installation_id || "";

  useEffect(() => {
    let current = true;
    setLoading(true);
    setInventoryError("");
    setInstallations([]);
    void listInstallations(undefined, { cache: "no-store" })
      .then((response) => {
        if (response.status !== 200) throw apiResponseError(response);
        if (current) setInstallations(response.data.installations || []);
      })
      .catch((reason: unknown) => {
        if (current)
          setInventoryError(
            errorMessage(
              reason,
              "Не удалось загрузить состояние инструментов.",
            ),
          );
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
  }, []);

  const healthy = state.configuration_health.healthy;
  return (
    <div className="setup-summary-content">
      <h3>{healthy ? "Готово к работе" : "Требует внимания"}</h3>
      {!healthy && (
        <div role="alert">
          <p>Настройка пока не готова:</p>
          {state.configuration_health.problems?.length ? (
            <ul>
              {state.configuration_health.problems.map((problem) => (
                <li key={problem}>{problem}</li>
              ))}
            </ul>
          ) : (
            <p>Сервер сообщил, что конфигурация не готова.</p>
          )}
        </div>
      )}
      <section aria-labelledby="summary-publication">
        <h4 id="summary-publication">Публикация</h4>
        <dl>
          <div>
            <dt>Каталог на сервере</dt>
            <dd>{state.settings.output_directory || "Не указан"}</dd>
          </div>
          <div>
            <dt>Формат</dt>
            <dd>{formatName(state.settings.publication_format)}</dd>
          </div>
          <div>
            <dt>Что будет происходить</dt>
            <dd>{formatExplanation(state.settings.publication_format)}</dd>
          </div>
          <div>
            <dt>Исходники после успешной публикации</dt>
            <dd>
              {state.settings.publication_remove_sources
                ? "Удалять"
                : "Сохранять"}
            </dd>
          </div>
        </dl>
      </section>
      <section aria-labelledby="summary-tools">
        <h4 id="summary-tools">Инструменты</h4>
        {inventoryError && (
          <p role="status">
            Состояние инструментов недоступно: {inventoryError}
          </p>
        )}
        <dl>
          <div>
            <dt>FFmpeg</dt>
            <dd>
              <ToolStatus
                id={ffmpegID}
                installations={installations}
                loading={loading}
              />
            </dd>
          </div>
          <div>
            <dt>fpcalc</dt>
            <dd>
              <ToolStatus
                id={fpcalcID}
                installations={installations}
                loading={loading}
              />
            </dd>
          </div>
        </dl>
      </section>
      <section aria-labelledby="summary-metadata">
        <h4 id="summary-metadata">Метаданные и тексты</h4>
        <dl>
          <div>
            <dt>MusicBrainz</dt>
            <dd>
              {state.settings.musicbrainz_verified_at
                ? `Подключение проверено · ${state.settings.musicbrainz_mode === "self-hosted" ? state.settings.musicbrainz_base_url : "публичный сервис"}`
                : "Подключение не проверено"}
            </dd>
          </div>
          <div>
            <dt>Тексты LRCLIB</dt>
            <dd>
              {state.settings.lrclib_enabled
                ? "Включены — доступны для поиска"
                : "Выключены"}
            </dd>
          </div>
          <div>
            <dt>AcoustID</dt>
            <dd>
              {state.settings.has_acoustid_application_key
                ? "Ключ задан"
                : "Ключ не задан (необязательно)"}
            </dd>
          </div>
        </dl>
      </section>
      <details>
        <summary>Технические сведения</summary>
        <dl>
          <div>
            <dt>Платформа</dt>
            <dd>
              {state.platform.goos}/{state.platform.goarch}
            </dd>
          </div>
          <div>
            <dt>Каталог инструментов</dt>
            <dd>{state.settings.tools_directory || "Не указан"}</dd>
          </div>
          <div>
            <dt>Последняя проверка MusicBrainz</dt>
            <dd>{state.settings.musicbrainz_verified_at || "Нет данных"}</dd>
          </div>
          <div>
            <dt>Последняя проверка FFmpeg</dt>
            <dd>
              {installations.find((item) => item.id === ffmpegID)
                ?.verified_at || "Нет данных"}
            </dd>
          </div>
          <div>
            <dt>Последняя проверка fpcalc</dt>
            <dd>
              {installations.find((item) => item.id === fpcalcID)
                ?.verified_at || "Нет данных"}
            </dd>
          </div>
        </dl>
      </details>
      <p>
        Следующий шаг: добавьте источник музыки и запустите сканирование. Импорт
        не запускается автоматически.
      </p>
    </div>
  );
}
