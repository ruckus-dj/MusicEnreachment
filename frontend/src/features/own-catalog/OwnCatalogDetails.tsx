import { Button } from "react-aria-components";
import type { LocalProviderItem } from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";
import { formatLocalDuration } from "../local-release/localReleaseFormModel";
import { muted, panel, primary, rows } from "../local-release/localReleaseUi";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";
import {
  type OwnCatalogType,
  ownCatalogArtistCredit,
  ownCatalogGenreNames,
  ownCatalogReleaseTitle,
} from "./ownCatalogModel";

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="border-b border-[var(--line)] py-[9px]">
      <dt className="text-[var(--muted)]">{label}</dt>
      <dd className="m-0 wrap-anywhere">{value || "—"}</dd>
    </div>
  );
}

export function OwnCatalogDetails({
  locale,
  type,
  item,
  loading,
  error,
  onRetry,
  onBack,
  onEdit,
}: {
  locale: Locale;
  type: OwnCatalogType;
  item?: LocalProviderItem;
  loading: boolean;
  error?: string;
  onRetry: () => void;
  onBack: () => void;
  onEdit?: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const title = item
    ? type === "artists"
      ? item.name || "—"
      : type === "recordings"
        ? item.title || "—"
        : ownCatalogReleaseTitle(item) || "—"
    : m("recordDetails");
  const edition = item?.edition;
  const artistCredit = item ? ownCatalogArtistCredit(item) : "";
  return (
    <OwnCatalogEditorShell
      eyebrow={m("currentDataNoHistory")}
      title={title}
      backLabel={m("back")}
      onBack={onBack}
      actions={
        item && onEdit ? (
          <Button className={primary} onPress={onEdit}>
            {m("edit")}
          </Button>
        ) : undefined
      }
    >
      {loading ? (
        <p className={muted} role="status">
          {m("loading")}
        </p>
      ) : error ? (
        <div className="grid justify-items-start gap-2">
          <p
            className="m-0 text-xs font-medium text-[var(--error)]"
            role="alert"
          >
            {error}
          </p>
          <Button className={primary} onPress={onRetry}>
            {m("retry")}
          </Button>
        </div>
      ) : item ? (
        <section className={`${panel} grid gap-3`}>
          <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-3">
            <Fact label={m("providerKey")} value={item.provider_key} />
            {type === "artists" && (
              <>
                <Fact label={m("name")} value={item.name ?? ""} />
                <Fact label={m("sortName")} value={item.sort_name ?? ""} />
              </>
            )}
            {type === "recordings" && (
              <>
                <Fact label={m("recordingArtists")} value={artistCredit} />
                <Fact
                  label={m("duration")}
                  value={formatLocalDuration(item.duration_ms)}
                />
              </>
            )}
            {type === "releases" && (
              <>
                <Fact
                  label={m("releaseGroup")}
                  value={item.release_group_title ?? ""}
                />
                <Fact label={m("releaseTitle")} value={item.title ?? ""} />
                <Fact label={m("releaseArtists")} value={artistCredit} />
                <Fact
                  label={m("primaryType")}
                  value={item.release_group_primary_type ?? ""}
                />
                <Fact
                  label={m("secondaryTypes")}
                  value={(item.release_group_secondary_types ?? []).join(", ")}
                />
                <Fact label={m("language")} value={item.language ?? ""} />
                <Fact label={m("script")} value={item.script ?? ""} />
                <Fact
                  label={m("releaseDate")}
                  value={edition?.release_date ?? ""}
                />
                <Fact label={m("country")} value={edition?.country ?? ""} />
                <Fact label={m("format")} value={edition?.format ?? ""} />
                <Fact label={m("label")} value={edition?.label ?? ""} />
                <Fact
                  label={m("catalogNumber")}
                  value={edition?.catalog_number ?? ""}
                />
                <Fact
                  label={m("disambiguation")}
                  value={edition?.disambiguation ?? ""}
                />
              </>
            )}
            <Fact
              label={m("genres")}
              value={ownCatalogGenreNames(item).join(", ")}
            />
            {type !== "releases" && (
              <Fact label={m("tags")} value={(item.tags ?? []).join(", ")} />
            )}
          </dl>
          {type === "releases" && (
            <div>
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] pb-2">
                <h2 className="m-0 text-base font-semibold">
                  {m("tracklist")}
                </h2>
                <small className={muted}>{m("positions")}</small>
              </div>
              {item.tracks?.length ? (
                <ol className={`${rows} mt-2`}>
                  {item.tracks.map((track, index) => {
                    const trackTitle =
                      track.title_override ||
                      track.recording_title ||
                      m("recordingNotSelected");
                    const duration =
                      track.duration_override_ms ?? track.recording_duration_ms;
                    return (
                      <li
                        className="flex min-w-0 items-baseline gap-2 py-2"
                        key={track.id || `${index}`}
                      >
                        <span className="w-6 shrink-0 text-right text-xs text-[var(--muted)] tabular-nums">
                          {track.displayed_number || index + 1}.
                        </span>
                        <div className="min-w-0">
                          <strong>{trackTitle}</strong>
                          <p className={muted}>
                            {[
                              track.provider_code,
                              formatLocalDuration(duration),
                            ]
                              .filter(Boolean)
                              .join(" · ")}
                          </p>
                        </div>
                      </li>
                    );
                  })}
                </ol>
              ) : (
                <p className={muted}>{m("noPositions")}</p>
              )}
            </div>
          )}
        </section>
      ) : null}
    </OwnCatalogEditorShell>
  );
}
