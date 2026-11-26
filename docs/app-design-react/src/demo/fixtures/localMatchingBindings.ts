/**
 * Explicit demo-only Local catalog bindings. The canonical fixture JSON keeps
 * its original shape; the adapter below labels exact Library positions with
 * their Local catalog identities without deriving anything from order.
 */
const demoLocalMatchingBindings: Record<
  string,
  {
    providerId: string;
    positions: Record<string, string>;
  }
> = {
  mix: {
    providerId: "cbfbb2dc-e452-40bd-9e4d-95df962290b4",
    positions: {
      "mix-t1": "mix-local-road-notes-track-1",
      "mix-t2": "mix-local-road-notes-track-2",
      "mix-t3": "mix-local-road-notes-track-3",
      "mix-t4": "mix-local-road-notes-track-4",
      "mix-t5": "mix-local-road-notes-track-5",
    },
  },
};

type DemoRelease = {
  id: string;
  provider?: string;
  providerId: string | null;
  tracks: { id: string; localProviderTrackId?: string | null }[];
};

export function applyDemoLocalMatchingBindings<T extends DemoRelease>(
  releases: T[],
): T[] {
  return releases.map((release) => {
    const binding = demoLocalMatchingBindings[release.id];
    if (!binding || release.provider !== "Local") return release;
    if (release.providerId !== binding.providerId) return release;
    return {
      ...release,
      tracks: release.tracks.map((track) => {
        const localProviderTrackId = binding.positions[track.id];
        return localProviderTrackId
          ? { ...track, localProviderTrackId }
          : track;
      }),
    };
  });
}
