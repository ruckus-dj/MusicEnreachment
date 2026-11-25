import { Button, Disclosure, DisclosurePanel } from "react-aria-components";
import { TagSnapshotView } from "../library/TagSnapshotView";
import { sourceInspectorMessage } from "./sourceInspectorMessages";
import {
  type SourceInspectorLocale,
  type SourceInspectorTechnicalResult,
  sourceInspectorDateTime,
  sourceInspectorDuration,
  sourceInspectorMeasured,
} from "./sourceInspectorModel";

const panel =
  "grid min-w-0 gap-1 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2.5";
const heading = "m-0 text-sm font-semibold";
const note = "m-0 text-[11px] text-[var(--muted)] wrap-anywhere";
const facts = "m-0 grid text-[13px]";
const fact =
  "grid min-w-0 gap-3 border-b border-[var(--line)] py-1.5 last:border-b-0 min-[600px]:grid-cols-[minmax(120px,1fr)_minmax(0,2fr)] max-[599px]:gap-0.5";
const term = "m-0 text-[var(--muted)]";
const detail = "m-0 min-w-0 wrap-anywhere";
const code = "font-mono text-[11px] wrap-anywhere";
const rawTrigger =
  "min-h-8 w-fit cursor-pointer rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-left text-[13px] font-semibold text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]";

export function SourceTechnicalResult({
  locale,
  result,
}: {
  readonly locale: SourceInspectorLocale;
  readonly result: SourceInspectorTechnicalResult;
}) {
  const t = (
    key: Parameters<typeof sourceInspectorMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceInspectorMessage(locale, key, values);
  const unknown = t("unknown");
  const date = sourceInspectorDateTime(result.inspectedAt, locale);
  return (
    <>
      <section className={panel} aria-labelledby="technical-title">
        <h2 id="technical-title" className={heading}>
          {t("savedTechnicalResult")}
        </h2>
        <p className={note}>
          {t("analysisAt", { date })} ·{" "}
          <span title={result.ffprobeVersion}>
            {result.ffprobeVersion.split("\n")[0]}
          </span>{" "}
          · {t("policy")} {result.analysisPolicyVersion}
        </p>
        <dl className={facts}>
          <div className={fact}>
            <dt className={term}>{t("container")}</dt>
            <dd className={detail}>{result.container.name ?? unknown}</dd>
          </div>
          <div className={fact}>
            <dt className={term}>{t("containerDescription")}</dt>
            <dd className={detail}>{result.container.longName ?? unknown}</dd>
          </div>
          <div className={fact}>
            <dt className={term}>{t("duration")}</dt>
            <dd className={detail}>
              {sourceInspectorDuration(
                result.container.durationMs,
                locale,
                unknown,
              )}
            </dd>
          </div>
          <div className={fact}>
            <dt className={term}>{t("containerBitrate")}</dt>
            <dd className={detail}>
              {sourceInspectorMeasured(
                result.container.bitRate,
                t("bitsPerSecond"),
                locale,
                unknown,
              )}
            </dd>
          </div>
        </dl>
      </section>
      <section className={panel} aria-labelledby="streams-title">
        <h2 id="streams-title" className={heading}>
          {t("audioStreams")}
        </h2>
        {result.streams.map((stream, position) => (
          <section
            className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
            key={stream.index ?? `unknown-${position}`}
            aria-label={t("audioStream", { index: stream.index ?? unknown })}
          >
            <h3 className={heading}>
              {t("stream", { index: stream.index ?? unknown })}
            </h3>
            <dl className={facts}>
              <div className={fact}>
                <dt className={term}>{t("codecProfile")}</dt>
                <dd className={detail}>
                  {stream.codecName ?? unknown} / {stream.profile ?? unknown}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("duration")}</dt>
                <dd className={detail}>
                  {sourceInspectorDuration(stream.durationMs, locale, unknown)}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("bitrate")}</dt>
                <dd className={detail}>
                  {sourceInspectorMeasured(
                    stream.bitRate,
                    t("bitsPerSecond"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("sampleRate")}</dt>
                <dd className={detail}>
                  {sourceInspectorMeasured(
                    stream.sampleRateHz,
                    t("hertzUnit"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("sampleFormat")}</dt>
                <dd className={detail}>{stream.sampleFormat ?? unknown}</dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("bitDepth")}</dt>
                <dd className={detail}>
                  {sourceInspectorMeasured(
                    stream.bitsPerSample,
                    t("bitsUnit"),
                    locale,
                    unknown,
                  )}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("channelsLayout")}</dt>
                <dd className={detail}>
                  {stream.channels ?? unknown} /{" "}
                  {stream.channelLayout ?? unknown}
                </dd>
              </div>
            </dl>
          </section>
        ))}
        {!result.streams.length && (
          <p className={note}>{t("noAudioStreams")}</p>
        )}
      </section>
      <TagSnapshotView
        title={t("sourceTags")}
        rows={result.tags}
        empty={t("noSourceTags")}
        unknown={unknown}
      />
      <Disclosure className={panel}>
        <Button slot="trigger" className={rawTrigger}>
          {t("rawFfprobe")}
        </Button>
        <DisclosurePanel>
          <section aria-label={t("rawFfprobe")}>
            <pre
              className={`m-0 max-w-full whitespace-pre-wrap rounded bg-[var(--raised)] p-2 ${code}`}
            >
              {JSON.stringify(result.rawJson, null, 2)}
            </pre>
          </section>
        </DisclosurePanel>
      </Disclosure>
    </>
  );
}
