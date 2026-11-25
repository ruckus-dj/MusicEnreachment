import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  createLocalProviderArtist,
  createLocalProviderRecording,
  createLocalProviderRelease,
  listLocalProviderArtists,
  listLocalProviderRecordings,
  listLocalProviderReleases,
  readLocalProviderArtist,
  readLocalProviderRecording,
  readLocalProviderRelease,
  updateLocalProviderArtist,
  updateLocalProviderRecording,
  updateLocalProviderRelease,
} from "../../api/generated/client";
import type {
  LocalProviderItem,
  LocalProviderPage,
  LocalProviderWrite,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { useHashLocation } from "../../app/navigation/hashLocation";
import {
  ApplicationOwnCatalogBinder,
  type ApplicationOwnCatalogModel,
  type ApplicationRouteProviderProps,
} from "../../ui/app/ApplicationRouteOutlet";
import type { LocalCatalogEditorSlot } from "../../ui/features/local-catalog/LocalCatalogScreenView";
import type {
  OwnCatalogListCopy,
  OwnCatalogListRow,
  OwnCatalogListTab,
} from "../../ui/features/local-catalog/OwnCatalogList";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "../../ui/features/local-catalog/ownCatalogMessages";
import type {
  LocalRecordingDraft,
  LocalReleaseArtist,
  LocalReleaseDraft,
  LocalReleaseRecording,
} from "../../ui/features/local-release/localReleaseFormModel";
import {
  findLocalReleaseArtists,
  findSavedLocalReleaseRecordings,
} from "../local-release/localReleaseLookups";
import {
  catalogCreateIdentity,
  writeCatalogRecord,
} from "../local-release/localReleaseWrites";
import {
  OwnCatalogArtistForm,
  OwnCatalogRecordingForm,
} from "./OwnCatalogRecordEditors";
import { OwnCatalogReleaseEditor } from "./OwnCatalogReleaseEditor";
import { ownCatalogDetailsViewProps } from "./ownCatalogDetailModel";
import { saveLocalArtist, saveLocalRecording } from "./ownCatalogLookups";
import {
  type ArtistFormDraft,
  artistDraftToWrite,
  OWN_CATALOG_PAGE_SIZE,
  type OwnCatalogType,
  ownCatalogPageCount,
  ownCatalogRowDetails,
  ownCatalogTitle,
  ownCatalogTypes,
  type RecordingFormDraft,
  recordingDraftToWrite,
  releaseDraftToWrite,
  releaseRecordRecordings,
} from "./ownCatalogModel";
import {
  defaultOwnCatalogRoute,
  ownCatalogRouteHref,
  parseOwnCatalogRoute,
} from "./ownCatalogRoute";

type ListParams = {
  q?: string;
  provider?: string;
  limit?: number;
  offset?: number;
};

type PageData = {
  key: string;
  items: LocalProviderItem[];
  total: number;
};

type DetailState = {
  type?: OwnCatalogType;
  id?: string;
  retry?: number;
  item?: LocalProviderItem;
  loading: boolean;
  error?: string | null;
  full: boolean;
};

type EditorState = EditorDraft & { hash: string };

type EditorDraft =
  | { kind: "create"; type: OwnCatalogType }
  | { kind: "edit"; type: OwnCatalogType; id: string };

type OwnCatalogHistoryState = { ownCatalogDetail?: string };

const emptyDetail: DetailState = { loading: false, full: false };
const catalogID = /^[a-f\d]{8}(?:-[a-f\d]{4}){3}-[a-f\d]{12}$/i;

function hashURL(href: string) {
  return `${window.location.pathname}${window.location.search}${href}`;
}

function publishHashChange() {
  window.dispatchEvent(new HashChangeEvent("hashchange"));
}

function pushCatalogHash(
  href: string,
  state: OwnCatalogHistoryState | null = null,
) {
  if (window.location.hash === href) return;
  window.history.pushState(state, "", hashURL(href));
  publishHashChange();
}

function replaceCatalogHash(href: string) {
  if (window.location.hash === href) return;
  window.history.replaceState(null, "", hashURL(href));
  publishHashChange();
}

const requestOptions = (signal?: AbortSignal) =>
  ({ cache: "no-store", signal }) satisfies RequestInit;

async function listOwnCatalog(
  type: OwnCatalogType,
  params: ListParams,
  options: RequestInit,
): Promise<LocalProviderPage> {
  const response =
    type === "releases"
      ? await listLocalProviderReleases(params, options)
      : type === "recordings"
        ? await listLocalProviderRecordings(params, options)
        : await listLocalProviderArtists(params, options);
  if (response.status !== 200) throw apiResponseError(response);
  return response.data;
}

async function readOwnCatalog(
  type: OwnCatalogType,
  id: string,
  options: RequestInit,
): Promise<LocalProviderItem> {
  if (!catalogID.test(id)) throw new Error();
  const response =
    type === "releases"
      ? await readLocalProviderRelease(id, options)
      : type === "recordings"
        ? await readLocalProviderRecording(id, options)
        : await readLocalProviderArtist(id, options);
  if (response.status !== 200) throw apiResponseError(response);
  if (response.data.id !== id || response.data.provider_code !== "local")
    throw new Error();
  return response.data;
}

function createOwnCatalog(type: OwnCatalogType, body: LocalProviderWrite) {
  if (type === "releases") return createLocalProviderRelease(body);
  if (type === "recordings") return createLocalProviderRecording(body);
  return createLocalProviderArtist(body);
}

function updateOwnCatalog(
  type: OwnCatalogType,
  id: string,
  body: LocalProviderWrite,
) {
  if (type === "releases") return updateLocalProviderRelease(id, body);
  if (type === "recordings") return updateLocalProviderRecording(id, body);
  return updateLocalProviderArtist(id, body);
}

export function ProductionOwnCatalogBinder({
  children,
}: ApplicationRouteProviderProps) {
  const model = useOwnCatalogModel();
  return (
    <ApplicationOwnCatalogBinder model={model}>
      {children}
    </ApplicationOwnCatalogBinder>
  );
}

function useOwnCatalogModel(): ApplicationOwnCatalogModel {
  const { locale } = useI18n();
  const hash = useHashLocation();
  const route = parseOwnCatalogRoute(hash) ?? defaultOwnCatalogRoute;
  const [filterInput, setFilterInput] = useState("");
  const [filter, setFilter] = useState("");
  const [page, setPage] = useState(1);
  const [editorState, setEditor] = useState<EditorState>();
  const editor = editorState?.hash === hash ? editorState : undefined;
  const [pageData, setPageData] = useState<PageData>({
    key: "",
    items: [],
    total: 0,
  });
  const [totals, setTotals] = useState<Partial<Record<OwnCatalogType, number>>>(
    {},
  );
  const [listLoading, setListLoading] = useState(false);
  const [listLoaded, setListLoaded] = useState(false);
  const [listError, setListError] = useState<string>();
  const [listVersion, setListVersion] = useState(0);
  const [detail, setDetail] = useState<DetailState>(emptyDetail);
  const [detailRetry, setDetailRetry] = useState(0);
  const [notice, setNotice] = useState<"created" | "updated">();
  const [artists, setArtists] = useState<LocalReleaseArtist[]>([]);
  const knownArtists = useRef(new Map<string, string>());
  const currentWrites = useRef(0);
  const [saving, setSaving] = useState(false);
  const countGeneration = useRef<Record<OwnCatalogType, number>>({
    releases: 0,
    recordings: 0,
    artists: 0,
  });
  const activeEditor = useRef<EditorState | undefined>(undefined);
  const initialType = useRef(route.type);

  const m = (
    key: OwnCatalogMessage,
    values?: Record<string, string | number>,
  ) => ownCatalogMessage(locale, key, values);
  const loadError = ownCatalogMessage(locale, "loadError");
  const detailLoadError = ownCatalogMessage(locale, "detailLoadError");

  useEffect(() => {
    return () => {
      activeEditor.current = undefined;
    };
  }, []);

  useLayoutEffect(() => {
    if (activeEditor.current?.hash !== hash) activeEditor.current = undefined;
  }, [hash]);

  const [visitHash, setVisitHash] = useState(hash);
  if (visitHash !== hash) {
    setVisitHash(hash);
    setEditor(undefined);
    if (!route.id) setNotice(undefined);
  }

  useEffect(() => {
    const controller = new AbortController();
    for (const kind of ownCatalogTypes) {
      if (kind === initialType.current) continue;
      const generation = countGeneration.current[kind];
      void listOwnCatalog(
        kind,
        { provider: "local", limit: 1, offset: 0 },
        requestOptions(controller.signal),
      )
        .then((result) => {
          if (
            !controller.signal.aborted &&
            generation === countGeneration.current[kind]
          )
            setTotals((current) => ({ ...current, [kind]: result.total }));
        })
        .catch(() => undefined);
    }
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const next = filterInput.trim();
    if (next === filter) return;
    const timer = window.setTimeout(() => {
      setFilter(next);
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [filter, filterInput]);

  const [pageType, setPageType] = useState(route.type);
  if (pageType !== route.type) {
    setPageType(route.type);
    setPage(1);
    setPageData({ key: "", items: [], total: 0 });
    setListLoaded(false);
    setListError(undefined);
  }

  const listKey = JSON.stringify([route.type, filter, page, listVersion]);
  const listRequestGeneration = useRef(0);
  useEffect(() => {
    const controller = new AbortController();
    const generation = ++listRequestGeneration.current;
    const countVersion = countGeneration.current[route.type];
    setListLoading(true);
    setListError(undefined);
    void listOwnCatalog(
      route.type,
      {
        provider: "local",
        ...(filter ? { q: filter } : {}),
        limit: OWN_CATALOG_PAGE_SIZE,
        offset: (page - 1) * OWN_CATALOG_PAGE_SIZE,
      },
      requestOptions(controller.signal),
    )
      .then((result) => {
        if (
          controller.signal.aborted ||
          generation !== listRequestGeneration.current
        )
          return;
        const totalPages = ownCatalogPageCount(result.total);
        if (page > totalPages) {
          setPage(totalPages);
          return;
        }
        setPageData({
          key: listKey,
          items: result.items ?? [],
          total: result.total,
        });
        setListLoaded(true);
        if (!filter && countVersion === countGeneration.current[route.type])
          setTotals((current) =>
            current[route.type] === result.total
              ? current
              : { ...current, [route.type]: result.total },
          );
      })
      .catch((reason: unknown) => {
        if (
          !controller.signal.aborted &&
          generation === listRequestGeneration.current
        )
          setListError(errorMessage(reason, loadError));
      })
      .finally(() => {
        if (
          !controller.signal.aborted &&
          generation === listRequestGeneration.current
        )
          setListLoading(false);
      });
    return () => controller.abort();
  }, [filter, listKey, loadError, page, route.type]);

  const detailRequestGeneration = useRef(0);
  useEffect(() => {
    const generation = ++detailRequestGeneration.current;
    if (!route.id) {
      setDetail(emptyDetail);
      return;
    }
    const controller = new AbortController();
    const detailType = route.type;
    const detailId = route.id;
    setDetail((current) =>
      current.type === detailType && current.id === detailId
        ? {
            ...current,
            retry: detailRetry,
            loading: true,
            error: undefined,
            full: false,
          }
        : {
            type: detailType,
            id: detailId,
            retry: detailRetry,
            loading: true,
            full: false,
          },
    );
    void readOwnCatalog(detailType, detailId, requestOptions(controller.signal))
      .then((item) => {
        if (
          !controller.signal.aborted &&
          generation === detailRequestGeneration.current
        )
          setDetail({
            type: detailType,
            id: detailId,
            retry: detailRetry,
            item,
            loading: false,
            full: true,
          });
      })
      .catch((reason: unknown) => {
        if (
          !controller.signal.aborted &&
          generation === detailRequestGeneration.current
        )
          setDetail({
            type: detailType,
            id: detailId,
            retry: detailRetry,
            loading: false,
            error:
              reason instanceof Error && reason.message ? reason.message : null,
            full: false,
          });
      });
    return () => controller.abort();
  }, [detailRetry, route.id, route.type]);

  const scopedDetail =
    route.id &&
    detail.type === route.type &&
    detail.id === route.id &&
    detail.retry === detailRetry
      ? detail
      : undefined;
  const activeDetail: DetailState =
    scopedDetail ??
    (route.id
      ? { type: route.type, id: route.id, loading: true, full: false }
      : emptyDetail);
  const fullDetailItem: LocalProviderItem | undefined =
    activeDetail.full && !activeDetail.error && !activeDetail.loading
      ? activeDetail.item
      : undefined;

  const openDetails = (kind: OwnCatalogType, item: LocalProviderItem) => {
    setNotice(undefined);
    setDetail({
      type: kind,
      id: item.id,
      retry: detailRetry,
      item,
      loading: true,
      full: false,
    });
    const href = ownCatalogRouteHref({ type: kind, id: item.id });
    pushCatalogHash(href, { ownCatalogDetail: href });
  };

  const closeDetails = () => {
    setNotice(undefined);
    if (!route.id) return;
    const detailHref = ownCatalogRouteHref({
      type: route.type,
      id: route.id,
    });
    const state = window.history.state as OwnCatalogHistoryState | null;
    if (state?.ownCatalogDetail === detailHref) {
      window.history.back();
      return;
    }
    replaceCatalogHash(ownCatalogRouteHref({ type: route.type }));
  };

  const openEditor = (next: EditorDraft) => {
    if (currentWrites.current > 0) return;
    const draft = { ...next, hash };
    activeEditor.current = draft;
    setNotice(undefined);
    setEditor(draft);
  };

  const closeEditor = () => {
    activeEditor.current = undefined;
    setEditor(undefined);
  };

  const backFromEditor = () => {
    if (currentWrites.current > 0) return;
    closeEditor();
  };

  function refreshCatalog(kind: OwnCatalogType) {
    const generation = ++countGeneration.current[kind];
    setListVersion((version) => version + 1);
    void listOwnCatalog(
      kind,
      { provider: "local", limit: 1, offset: 0 },
      requestOptions(),
    )
      .then((result) => {
        if (generation === countGeneration.current[kind])
          setTotals((current) => ({ ...current, [kind]: result.total }));
      })
      .catch(() => undefined);
  }

  async function runWrite<T>(write: () => Promise<T>): Promise<T> {
    currentWrites.current++;
    setSaving(true);
    try {
      return await write();
    } finally {
      if (--currentWrites.current === 0) setSaving(false);
    }
  }

  const commitRecord = (
    kind: OwnCatalogType,
    item: LocalProviderItem,
    created: boolean,
  ) => {
    closeEditor();
    setDetail({
      type: kind,
      id: item.id,
      retry: detailRetry,
      item,
      loading: false,
      full: true,
    });
    setNotice(created ? "created" : "updated");
    refreshCatalog(kind);
    if (created) {
      const href = ownCatalogRouteHref({ type: kind, id: item.id });
      pushCatalogHash(href, { ownCatalogDetail: href });
    }
  };

  const rememberArtists = (found: readonly LocalReleaseArtist[]) => {
    setArtists((current) => {
      const merged = new Map(current.map((artist) => [artist.id, artist]));
      for (const artist of found) merged.set(artist.id, artist);
      return [...merged.values()];
    });
    for (const artist of found)
      knownArtists.current.set(artist.id, artist.name ?? "");
  };

  const findArtists = async (query: string, signal?: AbortSignal) => {
    const found = await findLocalReleaseArtists(query, signal);
    rememberArtists(found);
    return found;
  };

  const findRecordings = (
    query: string,
    signal?: AbortSignal,
  ): Promise<readonly LocalReleaseRecording[]> =>
    findSavedLocalReleaseRecordings(query, signal);

  const createArtist = async (data: { name: string }) => {
    const artist = await runWrite(() => saveLocalArtist(data));
    rememberArtists([artist]);
    refreshCatalog("artists");
    return artist;
  };

  const saveReleaseRecording = async (data: LocalRecordingDraft) => {
    const recording = await runWrite(() => saveLocalRecording(data));
    refreshCatalog("recordings");
    return recording;
  };

  const isActiveEditor = () =>
    editor !== undefined &&
    activeEditor.current === editor &&
    window.location.hash === hash;

  async function saveRecord(
    type: OwnCatalogType,
    kind: "artist" | "recording" | "release",
    record: LocalProviderItem | undefined,
    body: LocalProviderWrite,
    title: string,
  ) {
    try {
      return await runWrite(() =>
        record
          ? writeCatalogRecord(`${kind}:update:${record.id}`, () =>
              updateOwnCatalog(type, record.id, body),
            )
          : writeCatalogRecord(catalogCreateIdentity(kind, title), () =>
              createOwnCatalog(type, body),
            ),
      );
    } finally {
      if (record && !isActiveEditor()) {
        const currentRoute = parseOwnCatalogRoute(window.location.hash);
        if (currentRoute?.type === type && currentRoute.id === record.id)
          setDetailRetry((attempt) => attempt + 1);
      }
    }
  }

  const saveArtistDraft = async (
    draft: ArtistFormDraft,
    record: LocalProviderItem | undefined,
  ) => {
    const body = artistDraftToWrite(draft);
    const item = await saveRecord(
      "artists",
      "artist",
      record,
      body,
      draft.name,
    );
    rememberArtists([
      {
        id: item.id,
        name: item.name ?? draft.name,
        provider: "Local",
      },
    ]);
    if (!isActiveEditor()) {
      refreshCatalog("artists");
      return;
    }
    commitRecord("artists", item, !record);
  };

  const saveRecordingDraft = async (
    draft: RecordingFormDraft,
    record: LocalProviderItem | undefined,
  ) => {
    const body = recordingDraftToWrite(record, draft);
    const item = await saveRecord(
      "recordings",
      "recording",
      record,
      body,
      draft.title,
    );
    if (!isActiveEditor()) {
      refreshCatalog("recordings");
      return;
    }
    commitRecord("recordings", item, !record);
  };

  const saveReleaseDraft = async (
    draft: LocalReleaseDraft,
    record: LocalProviderItem | undefined,
  ) => {
    const body = releaseDraftToWrite(record, draft, knownArtists.current);
    const item = await saveRecord(
      "releases",
      "release",
      record,
      body,
      draft.release_group_title,
    );
    if (!isActiveEditor()) {
      refreshCatalog("releases");
      return;
    }
    commitRecord("releases", item, !record);
  };

  const changeTab = (key: string | number) => {
    const next = ownCatalogTypes.find((kind) => kind === key);
    if (!next || next === route.type) return;
    setNotice(undefined);
    closeEditor();
    pushCatalogHash(ownCatalogRouteHref({ type: next }));
  };

  const changeFilter = (value: string) => {
    setFilterInput(value);
  };

  const clearFilter = () => {
    setFilterInput("");
    setFilter("");
    setPage(1);
  };

  const activePage = pageData.key === listKey ? pageData : undefined;
  const pageCount = ownCatalogPageCount(activePage?.total ?? 0);
  const currentPage = Math.min(Math.max(1, page), pageCount);
  const pagerBusy = listLoading || !activePage || filterInput.trim() !== filter;
  const detailsView =
    route.id && !editor
      ? ownCatalogDetailsViewProps({
          locale,
          type: route.type,
          item: activeDetail.item,
          loading: activeDetail.loading,
          error:
            activeDetail.error === null ? detailLoadError : activeDetail.error,
          onRetry: () => setDetailRetry((attempt) => attempt + 1),
          onBack: closeDetails,
          onEdit:
            fullDetailItem && !saving
              ? () =>
                  openEditor({
                    kind: "edit",
                    type: route.type,
                    id: fullDetailItem.id,
                  })
              : undefined,
        })
      : null;

  let editorView: LocalCatalogEditorSlot | null = null;
  if (editor) {
    const record =
      editor.kind === "edit" && fullDetailItem?.id === editor.id
        ? fullDetailItem
        : undefined;
    if (editor.kind !== "edit" || record) {
      const kind = editor.type;
      const back = backFromEditor;
      const recordArtists: LocalReleaseArtist[] = (record?.credits ?? []).map(
        (credit) => ({
          id: credit.artist_id,
          name: credit.credit_name || credit.artist_name,
          provider:
            credit.provider_code === "local" ? "Local" : credit.provider_code,
        }),
      );
      if (kind === "releases") {
        editorView = {
          kind: "releases",
          element: (
            <OwnCatalogReleaseEditor
              locale={locale}
              record={record}
              artists={[...recordArtists, ...artists]}
              recordings={record ? releaseRecordRecordings(record) : []}
              findRecordings={findRecordings}
              findArtists={findArtists}
              saveRecording={saveReleaseRecording}
              saveArtist={createArtist}
              onSave={(draft) => saveReleaseDraft(draft, record)}
              onBack={back}
            />
          ),
        };
      } else if (kind === "recordings") {
        editorView = {
          kind: "recordings",
          eyebrow: m("eyebrow"),
          title: record ? m("editRecording") : m("createRecording"),
          backLabel: m("back"),
          backDisabled: saving,
          onBack: back,
          element: (
            <OwnCatalogRecordingForm
              locale={locale}
              record={record}
              findArtists={findArtists}
              saveArtist={createArtist}
              onSave={(draft) => saveRecordingDraft(draft, record)}
              onBack={back}
            />
          ),
        };
      } else {
        editorView = {
          kind: "artists",
          eyebrow: m("eyebrow"),
          title: record ? m("editArtist") : m("createArtist"),
          backLabel: m("back"),
          backDisabled: saving,
          onBack: back,
          element: (
            <OwnCatalogArtistForm
              locale={locale}
              record={record}
              onSave={(draft) => saveArtistDraft(draft, record)}
              onBack={back}
            />
          ),
        };
      }
    }
  }

  const emptyMessage: Record<OwnCatalogType, OwnCatalogMessage> = {
    releases: "emptyReleases",
    recordings: "emptyRecordings",
    artists: "emptyArtists",
  };
  const createMessage: Record<OwnCatalogType, OwnCatalogMessage> = {
    releases: "createRelease",
    recordings: "createRecording",
    artists: "createArtist",
  };

  const tabs: OwnCatalogListTab[] = ownCatalogTypes.map((kind) => ({
    id: kind,
    label: m(kind as OwnCatalogMessage),
    count: totals[kind] ?? 0,
  }));
  const rows: OwnCatalogListRow[] = (activePage?.items ?? []).map((item) => ({
    id: item.id,
    title: ownCatalogTitle(route.type, item),
    detail: ownCatalogRowDetails(route.type, item, m),
    onOpen: () => openDetails(route.type, item),
  }));
  const copy: OwnCatalogListCopy = {
    title: m("title"),
    description: m("description"),
    tabListLabel: m("tabListLabel"),
    filterLabel: m("filterLabel"),
    filterPlaceholder: m("filterPlaceholder"),
    createLabel: m(createMessage[route.type]),
    emptyLabel: m(emptyMessage[route.type]),
    loading: m("loading"),
    retry: m("retry"),
    noMatches: m("noMatches"),
    clearFilter: m("clearFilter"),
  };

  return {
    list: {
      locale,
      copy,
      tabs,
      selectedTab: route.type,
      onTabChange: changeTab,
      filter: filterInput,
      hasFilter: Boolean(filter),
      onFilterChange: changeFilter,
      onClearFilter: clearFilter,
      onCreate: () => openEditor({ kind: "create", type: route.type }),
      createDisabled: saving,
      loading: listLoading,
      loaded: listLoaded,
      error: listError,
      page: activePage ? { rows, total: activePage.total } : undefined,
      pageNumber: currentPage,
      pageCount,
      busy: pagerBusy,
      onRetry: () => setListVersion((attempt) => attempt + 1),
      onPageChange: setPage,
    },
    notice: notice ? m(notice) : "",
    details: detailsView,
    editor: editorView,
  };
}
