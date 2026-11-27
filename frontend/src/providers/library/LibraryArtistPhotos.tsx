import { useQueries } from "@tanstack/react-query";
import {
  type ComponentProps,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  LibraryArtistCards,
  type LibraryArtistPhoto,
} from "../../ui/features/library/LibraryArtistCards";
import {
  fetchLibraryArtistPhoto,
  libraryArtistPhotoQueryKey,
} from "./libraryArtistPhoto";

type ArtistCardsProps = ComponentProps<typeof LibraryArtistCards>;

const PHOTO_QUERY_STALE_TIME = 5 * 60_000;
const PHOTO_QUERY_CACHE_TIME = 30 * 60_000;
const PHOTO_VIEWPORT_MARGIN = "200px 0px";

/**
 * Production-only artist leaf wrapper. Cards register their mounted art target
 * through the portable `onPhotoTarget` slot; this actor observes those targets
 * and requests photos only after a card approaches the viewport. The portable
 * `LibraryArtistCards` view never fetches.
 */
export function ProductionLibraryArtistCards(props: ArtistCardsProps) {
  const idsKey = props.artists
    .filter((artist) => artist.photoEligible)
    .map((artist) => artist.id)
    .join("\u0000");
  const { onPhotoTarget, photos } = useLibraryArtistPhotos(idsKey);
  return (
    <LibraryArtistCards
      {...props}
      artists={props.artists.map((artist) => {
        const photo = photos.get(artist.id);
        return photo ? { ...artist, photo } : artist;
      })}
      onPhotoTarget={onPhotoTarget}
    />
  );
}

/**
 * Callback-ref based lazy lookup. Registration happens as each portable card
 * mounts, so React Aria collection internals are never queried. Only eligible
 * artists enter the query list, and React Query caches each artist separately
 * across pages and locale changes. Pending requests never block the list.
 */
function useLibraryArtistPhotos(idsKey: string): {
  onPhotoTarget: (artistId: string, element: HTMLElement | null) => void;
  photos: ReadonlyMap<string, LibraryArtistPhoto>;
} {
  const ids = useMemo(
    () => new Set(idsKey ? idsKey.split("\u0000") : []),
    [idsKey],
  );
  const [visibleIDs, setVisibleIDs] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const observerRef = useRef<IntersectionObserver | null>(null);
  const elementsRef = useRef(new Map<string, HTMLElement>());
  const elementIDsRef = useRef(new Map<Element, string>());

  const onPhotoTarget = useCallback(
    (artistId: string, element: HTMLElement | null) => {
      const previous = elementsRef.current.get(artistId);
      if (previous === element) return;
      if (previous) {
        observerRef.current?.unobserve(previous);
        elementIDsRef.current.delete(previous);
        elementsRef.current.delete(artistId);
      }
      if (!element || !ids.has(artistId)) return;
      elementsRef.current.set(artistId, element);
      elementIDsRef.current.set(element, artistId);
      observerRef.current?.observe(element);
    },
    [ids],
  );

  useEffect(() => {
    if (typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entering = entries.flatMap((entry) => {
          if (!entry.isIntersecting) return [];
          const id = elementIDsRef.current.get(entry.target);
          return id ? [id] : [];
        });
        if (!entering.length) return;
        setVisibleIDs((current) => {
          let changed = false;
          const next = new Set(current);
          for (const id of entering) {
            if (next.has(id)) continue;
            next.add(id);
            changed = true;
          }
          return changed ? next : current;
        });
      },
      { rootMargin: PHOTO_VIEWPORT_MARGIN },
    );
    observerRef.current = observer;
    for (const element of elementsRef.current.values()) {
      observer.observe(element);
    }
    return () => {
      observer.disconnect();
      observerRef.current = null;
    };
  }, []);

  useEffect(() => {
    setVisibleIDs((current) => {
      const next = new Set([...current].filter((id) => ids.has(id)));
      return next.size === current.size ? current : next;
    });
    for (const [id, element] of elementsRef.current) {
      if (ids.has(id)) continue;
      observerRef.current?.unobserve(element);
      elementsRef.current.delete(id);
      elementIDsRef.current.delete(element);
    }
  }, [ids]);

  const requestedIDs = useMemo(
    () => [...ids].filter((id) => visibleIDs.has(id)),
    [ids, visibleIDs],
  );
  const queries = useQueries({
    queries: requestedIDs.map((id) => ({
      queryKey: libraryArtistPhotoQueryKey(id),
      queryFn: ({ signal }) => fetchLibraryArtistPhoto(id, signal),
      staleTime: PHOTO_QUERY_STALE_TIME,
      gcTime: PHOTO_QUERY_CACHE_TIME,
      retry: false,
    })),
  });

  const photos = new Map<string, LibraryArtistPhoto>();
  requestedIDs.forEach((id, index) => {
    const photo = queries[index]?.data;
    if (photo) photos.set(id, photo);
  });
  return { onPhotoTarget, photos };
}
