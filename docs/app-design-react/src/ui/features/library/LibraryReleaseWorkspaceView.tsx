import { type ComponentProps, Fragment } from "react";
import type { Locale } from "../../i18n/types";
import {
  ReleaseOverviewView,
  type ReleaseOverviewViewProps,
} from "./ReleaseOverviewView";
import { TrackReadErrorNoticeView } from "./TrackReadErrorNoticeView";
import {
  type TrackProviderRefreshModel,
  TrackProviderRefreshView,
  type TrackScreenNotFoundView,
  TrackScreenView,
  type TrackScreenViewProps,
} from "./TrackScreenView";

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown
  ? Omit<T, K>
  : never;

/** Fixed release overview model; the workspace supplies the frame locale. */
export type LibraryReleaseOverviewModel = DistributiveOmit<
  ReleaseOverviewViewProps,
  "locale"
>;

/** One keyed position screen; the workspace owns its locale and visibility. */
export type LibraryReleasePositionModel<
  ContextProps extends object = Record<never, never>,
  InlineProps extends object = Record<never, never>,
> = Omit<TrackScreenViewProps<ContextProps, InlineProps>, "locale" | "active">;

/** Typed read-error notice; the workspace supplies the frame locale. */
export type LibraryReleasePositionNoticeModel = Omit<
  ComponentProps<typeof TrackReadErrorNoticeView>,
  "locale"
>;

/** One keyed position entry; the host owns the key and its diagnostics. */
export type LibraryReleasePositionEntry<
  ContextProps extends object = Record<never, never>,
  InlineProps extends object = Record<never, never>,
> = {
  id: string;
  screen: LibraryReleasePositionModel<ContextProps, InlineProps>;
  /** Host-owned read error mounted after this key, when present. */
  notice?: LibraryReleasePositionNoticeModel;
};

/**
 * The edition and its selected position. A requested position that is absent
 * stays in the edition frame, exactly as the existing route did; it never
 * becomes an active position on its own. `unavailable` renders an early
 * standalone overview without the workspace frame.
 */
export type LibraryReleaseWorkspaceSelection =
  | { kind: "edition" }
  | { kind: "position"; positionId: string }
  | { kind: "missing"; notFound: TrackScreenNotFoundView }
  | { kind: "unavailable" };

export type LibraryReleaseWorkspaceViewProps<
  ContextProps extends object = Record<never, never>,
  InlineProps extends object = Record<never, never>,
> = {
  locale: Locale;
  selection: LibraryReleaseWorkspaceSelection;
  /**
   * The retained overview. `null` means this host has no overview to retain,
   * for example a direct demo position; it is unmounted rather than replaced
   * with a host-specific alternative.
   */
  overview: LibraryReleaseOverviewModel | null;
  /**
   * Edition-level provider refresh model. Only the live host supplies it, and
   * only while the edition is visible; the workspace mounts the existing view
   * in the overview's provider-refresh slot.
   */
  overviewProviderRefresh?: TrackProviderRefreshModel;
  /**
   * Every position is keyed and stays mounted. Only the selected key becomes
   * active so the retained publication preview keeps one reconciliation
   * identity across the edition and position routes.
   */
  positions: readonly LibraryReleasePositionEntry<ContextProps, InlineProps>[];
};

/**
 * One frame for the exact edition and its positions. The host supplies typed
 * release facts, keyed position models and the current selection; this view
 * fixes the overview and position composition, matches the existing visibility
 * frame, and keeps editors, previews and diagnostics as their narrow slots.
 */
export function LibraryReleaseWorkspaceView<
  ContextProps extends object = Record<never, never>,
  InlineProps extends object = Record<never, never>,
>({
  locale,
  selection,
  overview,
  overviewProviderRefresh,
  positions,
}: LibraryReleaseWorkspaceViewProps<ContextProps, InlineProps>) {
  if (selection.kind === "unavailable")
    return overview ? (
      <ReleaseOverviewView locale={locale} {...overview} />
    ) : null;
  const activePositionId =
    selection.kind === "position" ? selection.positionId : undefined;
  const showPosition = activePositionId !== undefined;
  return (
    <section
      className={`grid min-w-0 gap-3 text-[13px] leading-[18px] text-ink ${showPosition ? "" : "mx-auto max-w-5xl"}`}
    >
      {overview && (
        <div hidden={showPosition}>
          <ReleaseOverviewView
            locale={locale}
            {...overview}
            metadataEditorVisible={!showPosition}
            {...(overviewProviderRefresh
              ? {
                  providerRefresh: (
                    <TrackProviderRefreshView {...overviewProviderRefresh} />
                  ),
                }
              : {})}
          />
        </div>
      )}
      {selection.kind === "missing" && (
        <TrackScreenView locale={locale} notFound={selection.notFound} />
      )}
      {positions.map(({ id, screen, notice }) => (
        <Fragment key={id}>
          <TrackScreenView<ContextProps, InlineProps>
            locale={locale}
            active={id === activePositionId}
            {...screen}
          />
          {notice && <TrackReadErrorNoticeView locale={locale} {...notice} />}
        </Fragment>
      ))}
    </section>
  );
}
