import { Button, Disclosure, DisclosurePanel } from "react-aria-components";
import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";

const unknown = "Неизвестно";
const number = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 3 });
function measured(value: number | undefined, unit: string) {
  return value === undefined ? unknown : `${number.format(value)} ${unit}`;
}

function duration(milliseconds: number | undefined) {
  if (milliseconds === undefined) return unknown;
  const seconds = Math.round(milliseconds / 1000);
  return `${Math.floor(seconds / 3600)}:${String(Math.floor(seconds / 60) % 60).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}

export function SourceTechnicalResult({
  result,
}: {
  readonly result: SourceTechnicalResultResponse;
}) {
  return (
    <>
      <section className="sources-panel" aria-labelledby="technical-title">
        <h2 id="technical-title">Сохранённый технический результат</h2>
        <p className="sources-note">
          Анализ от {new Date(result.inspected_at).toLocaleString()} ·{" "}
          <span title={result.ffprobe_version}>
            {result.ffprobe_version.split("\n")[0]}
          </span>{" "}
          · Политика {result.analysis_policy_version}
        </p>
        <dl className="sources-summary">
          <div>
            <dt>Контейнер</dt>
            <dd>{result.container.name ?? unknown}</dd>
          </div>
          <div>
            <dt>Описание</dt>
            <dd>{result.container.long_name ?? unknown}</dd>
          </div>
          <div>
            <dt>Длительность</dt>
            <dd>{duration(result.container.duration_ms)}</dd>
          </div>
          <div>
            <dt>Битрейт контейнера</dt>
            <dd>{measured(result.container.bit_rate, "бит/с")}</dd>
          </div>
        </dl>
      </section>
      <section className="sources-panel" aria-labelledby="streams-title">
        <h2 id="streams-title">Аудиопотоки</h2>
        {(result.streams ?? []).map((stream, position) => (
          <section
            className="sources-stream"
            key={stream.index ?? `unknown-${position}`}
            aria-label={`Аудиопоток ${stream.index ?? unknown}`}
          >
            <h3>Поток {stream.index ?? unknown}</h3>
            <dl className="sources-summary">
              <div>
                <dt>Кодек / профиль</dt>
                <dd>
                  {stream.codec_name ?? unknown} / {stream.profile ?? unknown}
                </dd>
              </div>
              <div>
                <dt>Длительность</dt>
                <dd>{duration(stream.duration_ms)}</dd>
              </div>
              <div>
                <dt>Битрейт</dt>
                <dd>{measured(stream.bit_rate, "бит/с")}</dd>
              </div>
              <div>
                <dt>Частота дискретизации</dt>
                <dd>{measured(stream.sample_rate_hz, "Гц")}</dd>
              </div>
              <div>
                <dt>Формат сэмплов</dt>
                <dd>{stream.sample_format ?? unknown}</dd>
              </div>
              <div>
                <dt>Разрядность</dt>
                <dd>{measured(stream.bits_per_sample, "бит")}</dd>
              </div>
              <div>
                <dt>Каналы / схема</dt>
                <dd>
                  {stream.channels ?? unknown} /{" "}
                  {stream.channel_layout ?? unknown}
                </dd>
              </div>
            </dl>
          </section>
        ))}
        {!result.streams?.length && (
          <p className="sources-note">Аудиопотоки не указаны.</p>
        )}
      </section>
      <section className="sources-panel" aria-labelledby="tags-title">
        <h2 id="tags-title">Исходные теги</h2>
        <dl className="sources-summary">
          {Object.entries(result.tags).map(([key, values]) => (
            <div key={key}>
              <dt>
                <code>{key}</code>
              </dt>
              <dd>
                {values?.length
                  ? values.map((value) => <p key={value}>{value}</p>)
                  : unknown}
              </dd>
            </div>
          ))}
        </dl>
        {!Object.keys(result.tags).length && (
          <p className="sources-note">Исходные теги отсутствуют.</p>
        )}
      </section>
      <Disclosure className="sources-panel sources-raw">
        <Button slot="trigger">Исходный JSON ffprobe</Button>
        <DisclosurePanel>
          <section aria-label="Исходный JSON ffprobe">
            <pre>{JSON.stringify(result.raw_json, null, 2)}</pre>
          </section>
        </DisclosurePanel>
      </Disclosure>
    </>
  );
}
