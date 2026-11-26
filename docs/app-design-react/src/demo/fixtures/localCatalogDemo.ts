import {
  createLocalProviderState,
  type LocalProviderRecord,
  type LocalProviderState,
} from "../providers/local-catalog/localProviderModel";

// Clearly demo-only rows for paging the Own Catalog prototype. They are not
// part of the canonical fixture JSON and carry no source provenance.
const demoArtists: LocalProviderRecord[] = [
  {
    id: "local-artist-north-window",
    key: "demo-local-artist-north-window",
    provider: "Local",
    name: "North Window",
    sortName: "North Window",
    disambiguation: "demo artist",
    genres: [],
    tags: "demo",
  },
];

const demoReleases: LocalProviderRecord[] = [
  {
    id: "local-release-pale-harbour",
    key: "demo-local-release-pale-harbour",
    provider: "Local",
    release_group_title: "Pale Harbour",
    title: "Pale Harbour",
    artistIds: ["local-artist-north-window"],
    genres: [],
    release_date: "2024",
    country_code: "",
    label: "",
    catalog_number: "",
    format: "Digital Media",
    disambiguation: "demo edition",
    status: "",
    packaging: "",
    barcode: "",
    primaryType: "Album",
    secondaryTypes: [],
    language: "eng",
    tracks: [
      {
        id: "demo-local-track-pale-harbour-1",
        recordingId: "local-recording-green",
        titleOverride: "",
        durationOverrideMs: "",
      },
    ],
  },
  {
    id: "local-release-static-bloom",
    key: "demo-local-release-static-bloom",
    provider: "Local",
    release_group_title: "Static Bloom",
    title: "Static Bloom",
    artistIds: ["local-artist-north-window"],
    genres: [],
    release_date: "",
    country_code: "",
    label: "",
    catalog_number: "",
    format: "",
    disambiguation: "",
    status: "",
    packaging: "",
    barcode: "",
    primaryType: "EP",
    secondaryTypes: [],
    language: "eng",
    tracks: [
      {
        id: "demo-local-track-static-bloom-1",
        recordingId: "local-recording-mix-night-sketch",
        titleOverride: "",
        durationOverrideMs: "",
      },
    ],
  },
];

export function createLocalCatalogDemoState(): LocalProviderState {
  const state = createLocalProviderState();
  return {
    ...state,
    items: {
      ...state.items,
      artists: [...state.items.artists, ...structuredClone(demoArtists)],
      releases: [...state.items.releases, ...structuredClone(demoReleases)],
    },
  };
}
