import { useRef, useState } from "react";
import { libraryReleaseMessage } from "../../../ui/features/library/libraryReleaseMessages";
import type { TrackProviderRefreshModel } from "../../../ui/features/library/TrackScreenView";
import type { Locale } from "../../../ui/i18n/types";

type RefreshResult =
  | { state: "succeeded" }
  | { state: "failed"; safeError?: string };

/** Supplied provider results by exact local edition. No entry means no capability. */
export type DemoLibraryProviderRefresh = Readonly<
  Record<string, (() => Promise<RefreshResult>) | undefined>
>;

/** The host retains each admitted read across edition and position navigation. */
export function useDemoLibraryProviderRefresh(
  input: DemoLibraryProviderRefresh | undefined,
  locale: Locale,
) {
  const pending = useRef(new Set<string>());
  const [results, setResults] = useState<
    Record<string, RefreshResult | "pending" | "rejected">
  >({});
  return (releaseId: string): TrackProviderRefreshModel | undefined => {
    const refresh = input?.[releaseId];
    if (!refresh) return undefined;
    const result = results[releaseId];
    const busy = result === "pending";
    const publish = (value: (typeof results)[string]) =>
      setResults((current) => ({ ...current, [releaseId]: value }));
    return {
      action: {
        label: libraryReleaseMessage(
          locale,
          busy ? "refreshMusicBrainzRunning" : "refreshMusicBrainz",
        ),
        disabled: busy,
        onPress: () => {
          if (pending.current.has(releaseId)) return;
          pending.current.add(releaseId);
          publish("pending");
          void Promise.resolve()
            .then(refresh)
            .then(
              (outcome) => publish(outcome),
              () => publish("rejected"),
            )
            .finally(() => pending.current.delete(releaseId));
        },
        pending: busy
          ? libraryReleaseMessage(locale, "waitingMusicBrainz")
          : undefined,
        notice:
          typeof result === "object" && result.state === "succeeded"
            ? libraryReleaseMessage(locale, "musicBrainzUpdated")
            : undefined,
        error:
          result === "rejected"
            ? libraryReleaseMessage(locale, "refreshReleaseFailed")
            : typeof result === "object" && result.state === "failed"
              ? (result.safeError ??
                libraryReleaseMessage(locale, "musicBrainzFailed", {
                  state: result.state,
                }))
              : undefined,
      },
    };
  };
}
