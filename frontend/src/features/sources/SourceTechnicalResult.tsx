import { Button, Disclosure, DisclosurePanel } from "react-aria-components";
import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { numberFormat, sourceMessage } from "./sourceMessages";

function measured(
  value: number | undefined,
  unit: string,
  locale: "en" | "ru",
  unknown: string,
) {
  const number = numberFormat(locale, { maximumFractionDigits: 3 });
  return value === undefined ? unknown : `${number.format(value)} ${unit}`;
}

function duration(
  milliseconds: number | undefined,
  unknown: string,
  locale: "en" | "ru",
) {
  if (milliseconds === undefined) return unknown;
  const seconds = Math.round(milliseconds / 1000);
  const format = (value: number) =>
    numberFormat(locale, {
      minimumIntegerDigits: 2,
      useGrouping: false,
    }).format(value);
  return `${format(Math.floor(seconds / 3600))}:${format(Math.floor(seconds / 60) % 60)}:${format(seconds % 60)}`;
}

export function SourceTechnicalResult({
  result,
}: {
  readonly result: SourceTechnicalResultResponse;
}) {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const unknown = t("unknown");
  const date = new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(new Date(result.inspected_at));
  return (
    <>
      <section className="sources-panel" aria-labelledby="technical-title">
        <h2 id="technical-title">{t("savedTechnicalResult")}</h2>
        <p className="sources-note">
          {t("analysisAt", { date })} ·{" "}
          <span title={result.ffprobe_version}>
            {result.ffprobe_version.split("\n")[0]}
          </span>{" "}
          · {t("policy")} {result.analysis_policy_version}
        </p>
        <dl className="sources-summary">
          <div>
            <dt>{t("container")}</dt>
            <dd>{result.container.name ?? unknown}</dd>
          </div>
          <div>
            <dt>{t("containerDescription")}</dt>
            <dd>{result.container.long_name ?? unknown}</dd>
          </div>
          <div>
            <dt>{t("duration")}</dt>
            <dd>{duration(result.container.duration_ms, unknown, locale)}</dd>
          </div>
          <div>
            <dt>{t("containerBitrate")}</dt>
            <dd>
              {measured(
                result.container.bit_rate,
                t("bitsPerSecond"),
                locale,
                unknown,
              )}
            </dd>
          </div>
        </dl>
      </section>
      <section className="sources-panel" aria-labelledby="streams-title">
        <h2 id="streams-title">{t("audioStreams")}</h2>
        {(result.streams ?? []).map((stream, position) => (
          <section
            className="sources-stream"
            key={stream.index ?? `unknown-${position}`}
            aria-label={t("audioStream", { index: stream.index ?? unknown })}
          >
            <h3>{t("stream", { index: stream.index ?? unknown })}</h3>
            <dl className="sources-summary">
              <div>
                <dt>{t("codecProfile")}</dt>
                <dd>
                  {stream.codec_name ?? unknown} / {stream.profile ?? unknown}
                </dd>
              </div>
              <div>
                <dt>{t("duration")}</dt>
                <dd>{duration(stream.duration_ms, unknown, locale)}</dd>
              </div>
              <div>
                <dt>{t("bitrate")}</dt>
                <dd>
                  {measured(
                    stream.bit_rate,
                    t("bitsPerSecond"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("sampleRate")}</dt>
                <dd>
                  {measured(
                    stream.sample_rate_hz,
                    t("hertzUnit"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("sampleFormat")}</dt>
                <dd>{stream.sample_format ?? unknown}</dd>
              </div>
              <div>
                <dt>{t("bitDepth")}</dt>
                <dd>
                  {measured(
                    stream.bits_per_sample,
                    t("bitsUnit"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("channelsLayout")}</dt>
                <dd>
                  {stream.channels ?? unknown} /{" "}
                  {stream.channel_layout ?? unknown}
                </dd>
              </div>
            </dl>
          </section>
        ))}
        {!result.streams?.length && (
          <p className="sources-note">{t("noAudioStreams")}</p>
        )}
      </section>
      <section className="sources-panel" aria-labelledby="tags-title">
        <h2 id="tags-title">{t("sourceTags")}</h2>
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
          <p className="sources-note">{t("noSourceTags")}</p>
        )}
      </section>
      <Disclosure className="sources-panel sources-raw">
        <Button slot="trigger">{t("rawFfprobe")}</Button>
        <DisclosurePanel>
          <section aria-label={t("rawFfprobe")}>
            <pre>{JSON.stringify(result.raw_json, null, 2)}</pre>
          </section>
        </DisclosurePanel>
      </Disclosure>
    </>
  );
}
