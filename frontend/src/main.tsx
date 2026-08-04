import { useEffect, useMemo, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

type Tags = Record<string, string>
type Track = { track_id: string; position: number; title: string; relative_path?: string; layers?: Record<string, Tags>; final_revision?: number }
type SourceSummary = { source_id: string; path: string; format: string; sha256: string; state: string; origin?: string; disappeared_at?: string | null }
type PublicationSummary = { publication_id: string; source_id: string; path: string; format: string; sha256: string; metadata_revision_id?: number | null; state: string; created_at?: string }
type LibraryRecordView = { record_id: string; title: string; incoming_folder: string; publication: { state: string }; states: { source: string; processing: string; match: string; publication: string; metadata: string }; attention: boolean; sources: SourceSummary[]; publications: PublicationSummary[]; tracks: Track[]; audit: Array<{ action: string; actor: string; details: Record<string, unknown> }> }
type QueueItem = { record_id: string; title: string; artist: string; album: string; incoming_folder: string; source_state: string; processing_state: string; match_state: string; publication_state: string; metadata_state: string; source: SourceSummary | null; publication: PublicationSummary | null; attention: boolean; reason: string; nextAction: string }
type LibraryRecordSummary = { record_id: string; source_state: string; processing_state: string; match_state: string; publication_state: string; metadata_state: string; sources: SourceSummary[]; publications: PublicationSummary[] }
type LibraryRecordDetail = { record_id: string; states: { source: string; processing: string; match: string; publication: string; metadata: string }; sources: SourceSummary[]; publications: PublicationSummary[]; metadata_revisions: Array<{ source_id: string; layer: string; revision: number; tags: Tags; actor: string }>; events: Array<{ kind: string; state: string; reason: string | null; details: Record<string, unknown>; created_at: string }> }
type ScanResult = { added: number; changed: number; removed: number; moved: number; unchanged: number; queued_jobs: number }
type MatchingSettings = { confidence_threshold: number }
type Layer = 'final' | 'original' | 'analyzed'

function recordTitle(record: LibraryRecordDetail | LibraryRecordSummary): string {
  const source = record.sources[0]?.path
  return source?.split('/').filter(Boolean).at(-1) || record.record_id
}

function recordView(record: LibraryRecordDetail): LibraryRecordView {
  const revisionsBySource = new Map<string, Record<string, Tags>>()
  record.metadata_revisions.forEach((revision) => {
    const layers = revisionsBySource.get(revision.source_id) ?? {}
    layers[revision.layer] = revision.tags
    revisionsBySource.set(revision.source_id, layers)
  })
  return {
    record_id: record.record_id,
    title: recordTitle(record),
    incoming_folder: record.sources[0]?.path || 'Источник не указан',
    publication: { state: record.states.publication },
    states: record.states,
    attention: record.states.match !== 'matched' || record.states.publication !== 'current',
    sources: record.sources,
    publications: record.publications,
    tracks: record.sources.map((source, index) => ({
      track_id: source.source_id,
      position: index + 1,
      title: source.path.split('/').filter(Boolean).at(-1) || source.source_id,
      relative_path: source.path,
      layers: revisionsBySource.get(source.source_id),
      final_revision: record.metadata_revisions.find((revision) => revision.source_id === source.source_id && revision.layer === 'final')?.revision,
    })),
    audit: record.events.map((event) => ({ action: event.kind, actor: 'system', details: event.details })),
  }
}

const TAG_FIELDS = ['TITLE', 'ARTIST', 'ALBUM', 'ALBUMARTIST', 'DATE', 'ORIGINALDATE', 'GENRE', 'TRACKNUMBER', 'TRACKTOTAL', 'DISCNUMBER', 'DISCTOTAL', 'MUSICBRAINZ_TRACKID', 'MUSICBRAINZ_ALBUMID', 'MUSICBRAINZ_RELEASEGROUPID', 'ISRC']

type Section = 'overview' | 'library' | 'sources' | 'publications' | 'attention' | 'metadata'

const NAV_ITEMS: Array<{ id: Section; label: string; icon: 'library' | 'source' | 'publication' | 'attention' | 'metadata' | 'settings' }> = [
  { id: 'overview', label: 'Состояние', icon: 'library' },
  { id: 'library', label: 'Медиатека', icon: 'library' },
  { id: 'sources', label: 'Исходники', icon: 'source' },
  { id: 'publications', label: 'Публикации', icon: 'publication' },
  { id: 'attention', label: 'Требуют внимания', icon: 'attention' },
  { id: 'metadata', label: 'Метаданные', icon: 'metadata' },
]

const SECTION_COPY: Record<Section, { eyebrow: string; title: string; description: string }> = {
  overview: { eyebrow: 'Рабочее состояние', title: 'Состояние', description: 'Текущая работа, источники и публикации в одном спокойном обзоре.' },
  library: { eyebrow: 'Стабильная медиатека', title: 'Медиатека', description: 'Находите композиции по исполнителю, пути, hash или стабильному ID.' },
  sources: { eyebrow: 'Неизменяемые входы', title: 'Исходники', description: 'Файлы остаются на месте. Здесь видны их версии, происхождение и наблюдения.' },
  publications: { eyebrow: 'Управляемые выходы', title: 'Публикации', description: 'Следите за текущими, устаревшими и историческими версиями медиатеки.' },
  attention: { eyebrow: 'Нужна проверка', title: 'Требуют внимания', description: 'Причины собраны рядом с одной следующей операцией.' },
  metadata: { eyebrow: 'История метаданных', title: 'Метаданные', description: 'Original, Analyzed и Final остаются отдельными слоями и версиями.' },
}

function RelationshipStrip({ section, record }: { section: Section; record: LibraryRecordView }) {
  const source = record.sources[0]
  const publication = record.publications.find((item) => item.state === 'current') ?? record.publications.at(-1)
  const sectionNote = section === 'attention'
    ? record.states.match !== 'matched' ? 'Сопоставить запись с MusicBrainz' : record.states.publication === 'stale' ? 'Перепубликовать из выбранного источника' : 'Выбрать доступную версию источника'
    : section === 'sources' ? 'Источник остаётся неизменяемым свидетельством' : section === 'publications' ? 'Выход связан с source_id и ревизией метаданных' : 'Одна стабильная запись связывает обе версии'
  return <div className="relationship-strip relationship-linked"><div className="relationship-side"><span className="eyebrow">Версия источника</span><strong>{source?.source_id || 'Источник отсутствует'}</strong><small>{source?.path || 'Путь не наблюдался'} · {source?.sha256.slice(0, 12) || '—'}…</small></div><span className="relationship-arrow" aria-hidden="true">→</span><div className="relationship-side"><span className="eyebrow">Версия публикации</span><strong>{publication?.publication_id || 'Публикация отсутствует'}</strong><small>{publication?.path || 'Управляемый файл отсутствует'} · ревизия {publication?.metadata_revision_id || '—'} · {publication?.sha256.slice(0, 12) || '—'}…</small></div><small className="relationship-note">{sectionNote}</small></div>
}

function OverviewPanel({ items, record }: { items: QueueItem[]; record: LibraryRecordView | null }) {
  const active = items.filter((item) => item.processing_state !== 'complete')
  const events = record?.audit.slice(-3).reverse() ?? []
  return <section className="overview-grid" aria-label="Рабочее состояние"><article className="overview-card"><p className="eyebrow">Текущая работа</p><h3>{active.length ? `${active.length} записи требуют обработки` : 'Активных задач нет'}</h3>{active.length ? active.slice(0, 3).map((item) => <div className="overview-row" key={item.record_id}><span className="overview-dot warning" /><div><strong>{item.record_id}</strong><small>{item.processing_state} · {item.nextAction}</small></div></div>) : <p className="overview-muted">Очередь обработки спокойна. Новые версии источников появятся после сканирования.</p>}</article><article className="overview-card"><p className="eyebrow">Последние события</p><h3>{events.length ? 'История выбранной записи' : 'События появятся здесь'}</h3>{events.length ? events.map((event, index) => <div className="overview-row" key={`${event.action}-${index}`}><span className="overview-dot" /><div><strong>{event.action.replaceAll('_', ' ')}</strong><small>{event.actor} · история стабильной записи</small></div></div>) : <p className="overview-muted">Инспекции, совпадения и публикации сохраняются в неизменяемой временной линии.</p>}</article></section>
}

function Icon({ name }: { name: 'library' | 'disc' | 'folder' | 'search' | 'settings' | 'chevron' | 'save' | 'history' | 'music' | 'source' | 'publication' | 'attention' | 'metadata' }) {
  const paths: Record<string, string> = {
    library: 'M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5v-13ZM7 8h10M7 12h10M7 16h6',
    disc: 'M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16Zm0 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6Zm0 2.25v1.5',
    folder: 'm3.5 7 2-2h4l1.5 2h9.5v10.5H3.5V7Z',
    search: 'm20 20-4.5-4.5M10.75 17a6.25 6.25 0 1 0 0-12.5 6.25 6.25 0 0 0 0 12.5Z',
    settings: 'M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Zm0-5v2m0 15v2M3.5 12h2m13 0h2M5.99 5.99l1.42 1.42m9.18 9.18 1.42 1.42m0-12.02-1.42 1.42M7.41 16.59l-1.42 1.42',
    chevron: 'm9 5 7 7-7 7',
    save: 'M5 4h11l3 3v13H5V4Zm3 0v5h7V4M8 20v-7h8v7',
    history: 'M4 12a8 8 0 1 0 2.34-5.66L4 8.68M4 4v4.68h4.68',
    music: 'M9 18V6l10-2v12M9 18a3 3 0 1 1-3-3 3 3 0 0 1 3 3Zm10-2a3 3 0 1 1-3-3 3 3 0 0 1 3 3Z',
    source: 'M4 6.5h6l2 2H20v9H4v-11Zm4 4h8m-8 3h5',
    publication: 'M5 4h10l4 4v12H5V4Zm10 0v5h4M8 13h8m-8 3h5',
    attention: 'M12 4 21 20H3L12 4Zm0 5v5m0 3h.01',
    metadata: 'M5 5h14v14H5V5Zm3 4h8m-8 3h8m-8 3h5',
  }
  return <svg aria-hidden="true" viewBox="0 0 24 24" className="icon"><path d={paths[name]} /></svg>
}

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, { headers: { 'content-type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(typeof payload.detail === 'string' ? payload.detail : 'Не удалось выполнить запрос')
  return payload as T
}

function App() {
  const [queue, setQueue] = useState<QueueItem[]>([])
  const [release, setRelease] = useState<LibraryRecordView | null>(null)
  const [selectedReleaseId, setSelectedReleaseId] = useState('')
  const [selectedTrackId, setSelectedTrackId] = useState('')
  const [query, setQuery] = useState('')
  const [view, setView] = useState<Section>('overview')
  const [group, setGroup] = useState<'folder' | 'artist' | 'album'>('folder')
  const [layer, setLayer] = useState<Layer>('final')
  const [tags, setTags] = useState<Tags>({})
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [scanLoading, setScanLoading] = useState(false)
  const [threshold, setThreshold] = useState(0.7)
  const [thresholdDraft, setThresholdDraft] = useState(0.7)
  const [thresholdSaving, setThresholdSaving] = useState(false)

  async function loadQueue() {
    setLoading(true)
    try {
      const payload = await api<{ items: LibraryRecordSummary[] }>('/api/library/records')
      const items = payload.items.map((record) => {
        const source = record.sources[0] ?? null
        const publication = record.publications.find((item) => item.state === 'current') ?? record.publications.at(-1) ?? null
        const reason = record.match_state !== 'matched' ? 'Идентичность не подтверждена' : record.source_state !== 'present' ? 'Источник недоступен' : record.publication_state === 'stale' ? 'Публикация устарела' : 'Обработка завершена'
        const nextAction = record.match_state !== 'matched' ? 'Сопоставить запись' : record.source_state !== 'present' ? 'Выбрать источник' : record.publication_state === 'stale' ? 'Перепубликовать' : 'Открыть историю'
        return {
          record_id: record.record_id,
          title: view === 'sources' ? source?.source_id || recordTitle(record) : view === 'publications' ? publication?.publication_id || recordTitle(record) : recordTitle(record),
          artist: view === 'attention' ? `${record.match_state} · ${reason}` : record.match_state,
          album: view === 'publications' ? `${publication?.path || 'Путь отсутствует'} · ${publication?.source_id || 'source_id —'} · ревизия ${publication?.metadata_revision_id || '—'}` : record.publication_state,
          incoming_folder: view === 'sources' ? `${source?.path || 'Источник не указан'} · ${source?.sha256.slice(0, 12) || '—'}… · ${source?.origin || 'origin —'}` : source?.path || 'Источник не указан',
          source_state: record.source_state,
          processing_state: record.processing_state,
          match_state: record.match_state,
          publication_state: record.publication_state,
          metadata_state: record.metadata_state,
          source,
          publication,
          attention: record.match_state !== 'matched' || record.publication_state !== 'current',
          reason,
          nextAction,
        }
      })
      setQueue(items)
      if (!selectedReleaseId) {
        const first = items.find((item) => view === 'attention' ? item.attention : true)
        if (first) setSelectedReleaseId(first.record_id)
      }
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось загрузить медиатеку') }
    finally { setLoading(false) }
  }

  async function loadRelease(id: string) {
    setSelectedReleaseId(id)
    setNotice('')
    try {
      const payload = await api<LibraryRecordDetail>(`/api/library/records/${id}`)
      const view = recordView(payload)
      setRelease(view)
      const tracks = view.tracks
      const track = tracks.find((item) => item.track_id === selectedTrackId) ?? tracks[0]
      if (track) chooseTrack(track)
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось открыть запись') }
  }

  async function scanIncoming() {
    setScanLoading(true)
    setNotice('Сканирование входящей библиотеки…')
    try {
      const result = await api<ScanResult>('/api/reconciliation/scan', { method: 'POST' })
      setNotice(`Сканирование завершено: +${result.added}, изменено ${result.changed}, удалено ${result.removed}, перемещено ${result.moved}`)
      await loadQueue()
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось просканировать библиотеку') }
    finally { setScanLoading(false) }
  }

  async function saveThreshold() {
    setThresholdSaving(true)
    try {
      const payload = await api<MatchingSettings>('/api/settings/matching', {
        method: 'PUT',
        body: JSON.stringify({ confidence_threshold: thresholdDraft }),
      })
      setThreshold(payload.confidence_threshold)
      setThresholdDraft(payload.confidence_threshold)
      setNotice('Порог совпадения сохранён без перезапуска')
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось сохранить порог') }
    finally { setThresholdSaving(false) }
  }

  useEffect(() => { void loadQueue() }, [view])
  useEffect(() => {
    void api<MatchingSettings>('/api/settings/matching').then((payload) => {
      setThreshold(payload.confidence_threshold)
      setThresholdDraft(payload.confidence_threshold)
    }).catch((error: unknown) => setNotice(error instanceof Error ? error.message : 'Не удалось загрузить настройки'))
  }, [])
  useEffect(() => { if (selectedReleaseId) void loadRelease(selectedReleaseId) }, [selectedReleaseId])

  const visibleQueue = useMemo(() => queue.filter((item) => {
    const haystack = `${item.incoming_folder} ${item.record_id} ${item.title} ${item.artist} ${item.album}`.toLowerCase()
    const scoped = view === 'attention'
      ? item.attention
      : view === 'publications'
        ? item.publication_state !== 'absent'
        : true
    return haystack.includes(query.toLowerCase()) && scoped
  }), [queue, query, view])
  const groups = useMemo(() => {
    const map = new Map<string, QueueItem[]>()
    visibleQueue.forEach((item) => {
      const parts = item.incoming_folder.split('/').filter(Boolean)
      const key = view === 'attention' ? item.reason : group === 'folder' ? parts.at(-1) || 'Корень' : group === 'album' ? item.album || item.title : item.artist || 'Неизвестный исполнитель'
      map.set(key, [...(map.get(key) ?? []), item])
    })
    return [...map.entries()]
  }, [visibleQueue, group, view])
  const selectedTrack = release?.tracks.find((item) => item.track_id === selectedTrackId) ?? null
  const artist = selectedTrack?.layers?.final?.ARTIST || 'Неизвестный исполнитель'
  const album = selectedTrack?.layers?.final?.ALBUM || release?.title || 'Без альбома'
  const visibleTags = layer === 'final' ? tags : (selectedTrack?.layers?.[layer] ?? {})
  const canEdit = false
  const listTitle = view === 'sources' ? 'Версии источников' : view === 'publications' ? 'Версии публикаций' : view === 'attention' ? 'Причины для действия' : view === 'metadata' ? 'Записи с метаданными' : 'Каталог записей'
  const listDescription = view === 'sources' ? 'Неизменяемые входные файлы и их последние наблюдения.' : view === 'publications' ? 'Управляемые outputs, связанные с точным source_id.' : view === 'attention' ? 'Каждая строка сохраняет причину и следующий безопасный шаг.' : view === 'metadata' ? 'Слои и ревизии доступны без изменения исходного файла.' : 'Стабильные записи, а не пути или webhook-события.'

  function chooseTrack(track: Track) {
    setSelectedTrackId(track.track_id)
    setTags({ ...(track.layers?.final ?? {}) })
    setLayer('final')
  }

  async function saveTrack() {
    if (!selectedTrack || !canEdit) return
    setNotice('Метаданные доступны только для чтения в stable library API')
  }

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark"><Icon name="music" /></div><div><strong>Music Ingest</strong><span>LOCAL LIBRARY</span></div></div>
      <nav className="nav" aria-label="Основная навигация">
        {NAV_ITEMS.map((item) => <button key={item.id} className={view === item.id ? 'nav-item active' : 'nav-item'} onClick={() => setView(item.id)}><Icon name={item.icon} /><span className="nav-label">{item.label}</span>{item.id === 'library' && <span>{queue.length}</span>}{item.id === 'attention' && <span className="nav-alert">{queue.filter((entry) => entry.attention).length}</span>}</button>)}
      </nav><span className="nav-scroll-hint" aria-hidden="true">ещё →</span>
        <div className="sidebar-bottom"><div className="storage"><div className="storage-label"><span>Хранилище</span><b>FLAC</b></div><div className="progress"><i /></div><small>Источники не изменяются</small></div><button className="nav-item quiet" onClick={() => setView('metadata')}><Icon name="settings" /><span className="nav-label">Настройки</span></button></div>
    </aside>
    <main className="workspace">
      <header className="topbar"><div className="breadcrumbs"><span>Music Ingest</span><Icon name="chevron" /><strong>{SECTION_COPY[view].title}</strong></div><div className="topbar-actions"><span className="live-dot">Синхронизировано</span><button className="avatar" aria-label="Профиль">MI</button></div></header>
      <div className="content">
        <section className="hero"><div><p className="eyebrow">{SECTION_COPY[view].eyebrow}</p><h1>{SECTION_COPY[view].title}</h1><p className="hero-copy">{SECTION_COPY[view].description}</p></div><div className="hero-stat"><strong>{queue.length.toString().padStart(2, '0')}</strong><span>стабильных<br />записей</span></div></section>{view === 'overview' && <OverviewPanel items={queue} record={release} />}{release && selectedTrack && <RelationshipStrip section={view} record={release} />}
        <section className="status-strip" aria-label="Сводка состояния"><div><span>Источники</span><strong>{queue.filter((item) => item.incoming_folder !== 'Источник не указан').length}</strong><small>наблюдаются</small></div><div><span>Публикации</span><strong>{queue.filter((item) => item.publication_state !== 'absent').length}</strong><small>управляемых выходов</small></div><div className={queue.some((item) => item.attention) ? 'status-cell warning' : 'status-cell'}><span>Внимание</span><strong>{queue.filter((item) => item.attention).length}</strong><small>следующих действий</small></div><div><span>Идентичность</span><strong>{queue.filter((item) => item.artist === 'matched').length}</strong><small>с MusicBrainz</small></div></section>
         <div className="toolbar"><label className="search"><span className="sr-only">Поиск по медиатеке</span><Icon name="search" /><input aria-label="Поиск по медиатеке" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Поиск по папке, исполнителю или альбому" /></label><div className="toolbar-controls"><span>Группировать:</span><button aria-pressed={group === 'folder'} className={group === 'folder' ? 'seg active' : 'seg'} onClick={() => setGroup('folder')}><Icon name="folder" /> Папки</button><button aria-pressed={group === 'artist'} className={group === 'artist' ? 'seg active' : 'seg'} onClick={() => setGroup('artist')}><Icon name="music" /> Исполнители</button><button aria-pressed={group === 'album'} className={group === 'album' ? 'seg active' : 'seg'} onClick={() => setGroup('album')}><Icon name="disc" /> Альбомы</button></div></div><div className="mobile-threshold-control"><div className="threshold-heading"><label htmlFor="mobile-confidence-threshold">Порог совпадения</label><output>{Math.round(thresholdDraft * 100)}%</output></div><input id="mobile-confidence-threshold" type="range" min="0" max="1" step="0.01" value={thresholdDraft} onChange={(event) => setThresholdDraft(Number(event.target.value))} /><button className="threshold-save" disabled={thresholdSaving || thresholdDraft === threshold} onClick={() => void saveThreshold()}>{thresholdSaving ? 'Сохранение…' : 'Сохранить порог'}</button><small>Меняется без перезапуска</small></div>
         <div className="library-grid"><section className="release-panel"><div className="section-heading"><div><p className="eyebrow">{visibleQueue.length} результатов</p><h2>{listTitle}</h2><p className="section-description">{listDescription}</p></div><div className="section-actions"><button className="refresh" disabled={scanLoading} onClick={() => void scanIncoming()}>{scanLoading ? 'Сканирование…' : 'Сканировать файлы'}</button><button className="refresh" onClick={() => void loadQueue()}>Обновить</button></div></div>{loading ? <div className="empty-state">Загрузка каталога…</div> : groups.length ? groups.map(([key, items]) => <div className="group" key={key}><div className="group-title"><Icon name={group === 'folder' ? 'folder' : group === 'album' ? 'disc' : 'music'} /><span>{key}</span><small>{items.length}</small></div>{items.map((item) => <button className={item.record_id === selectedReleaseId ? 'release-row selected' : 'release-row'} key={item.record_id} onClick={() => setSelectedReleaseId(item.record_id)}><span className="cover"><Icon name={view === 'sources' ? 'source' : view === 'publications' ? 'publication' : 'disc'} /></span><span className="release-copy"><strong>{item.title || item.record_id}</strong><small>{item.record_id}</small><span>{view === 'sources' ? `${item.source_state} · ${item.incoming_folder}` : view === 'publications' ? `${item.publication_state} · ${item.album}` : view === 'attention' ? `${item.match_state} · ${item.processing_state}` : `${item.artist} · ${item.incoming_folder}`}</span></span><span className={item.attention ? 'badge warning' : 'badge'}>{item.attention ? 'Проверить' : item.publication_state === 'current' ? 'Текущая' : 'История'}</span><Icon name="chevron" /></button>)}</div>) : <div className="empty-state"><Icon name="library" /><strong>Записи не найдены</strong><span>Измените запрос или дождитесь публикации новых файлов.</span></div>}</section>
          <section className="detail-panel">{release && selectedTrack ? <><div className="detail-header"><div><p className="eyebrow">{artist}</p><h2>{album}</h2><span className="path">{release.incoming_folder}</span></div><span className="status"><i /> {release.publication.state}</span></div><div className="relationship-strip">{view === 'sources' ? <><span className="eyebrow">Неизменяемый источник</span><strong>{release.sources[0]?.sha256.slice(0, 16)}…</strong><small>{release.sources[0]?.origin || 'manual'} · {release.sources[0]?.state} · {release.sources[0]?.disappeared_at ? 'исчез' : 'последнее наблюдение сохранено'}</small></> : view === 'publications' ? <><span className="eyebrow">Текущая публикация</span><strong>{release.publications.find((publication) => publication.state === 'current')?.path || 'Публикация отсутствует'}</strong><small>source_id: {release.publications.find((publication) => publication.state === 'current')?.source_id || '—'} · revision {release.publications.find((publication) => publication.state === 'current')?.metadata_revision_id || '—'}</small></> : view === 'attention' ? <><span className="eyebrow">Причина и следующий шаг</span><strong>{release.states.match !== 'matched' ? 'Идентичность не подтверждена' : release.states.publication === 'stale' ? 'Публикация устарела' : 'Источник недоступен'}</strong><small>{release.states.match !== 'matched' ? 'Сопоставить запись с MusicBrainz' : release.states.publication === 'stale' ? 'Перепубликовать из выбранного источника' : 'Выбрать доступную версию источника'}</small></> : <><span className="eyebrow">Стабильная запись</span><strong>{release.record_id}</strong><small>{release.sources.length} версии источников · {release.publications.length} версии публикаций · {release.states.metadata}</small></>}</div><div className="track-list"><div className="track-list-head"><span>Источник</span><span>Файл</span><span>Состояние</span></div>{release.tracks.map((track) => <button className={track.track_id === selectedTrack.track_id ? 'track-row selected' : 'track-row'} key={track.track_id} onClick={() => chooseTrack(track)}><span className="track-name"><b>{String(track.position).padStart(2, '0')}</b><strong>{track.title || track.layers?.final?.TITLE || 'Без названия'}</strong></span><span>{track.relative_path?.split('/').at(-1) || 'FLAC'}</span><span className="track-state"><i /> {track.final_revision === undefined ? 'несколько файлов' : `ревизия ${track.final_revision}`}</span></button>)}</div><div className={view === 'metadata' ? 'editor' : 'editor editor-context'}><div className="editor-heading"><div><p className="eyebrow">Редактор слоёв · только чтение</p><h3>{selectedTrack.title || tags.TITLE || 'Выбранный источник'}</h3></div><span className="revision">v{selectedTrack.final_revision ?? '—'}</span></div><div className="layer-tabs"><button aria-pressed={layer === 'final'} className={layer === 'final' ? 'active' : ''} onClick={() => setLayer('final')}>Final · только чтение</button><button aria-pressed={layer === 'original'} className={layer === 'original' ? 'active' : ''} onClick={() => setLayer('original')}>Original · только чтение</button><button aria-pressed={layer === 'analyzed'} className={layer === 'analyzed' ? 'active' : ''} onClick={() => setLayer('analyzed')}>Analyzed · кандидат</button></div><div className="tag-grid">{TAG_FIELDS.map((field) => <label key={field}><span>{field}</span><input readOnly={!canEdit} value={visibleTags[field] ?? ''} onChange={(event) => setTags({ ...tags, [field]: event.target.value })} placeholder="Не задано" /></label>)}</div><div className="editor-footer"><span className="notice" role="status">{notice || (!selectedTrack.layers ? 'У этого источника несколько файлов: выберите конкретную версию.' : layer !== 'final' ? 'Слой доступен только для просмотра.' : 'Final-слой доступен только для чтения в текущем API.')}</span><button disabled={!canEdit} className="primary" onClick={() => void saveTrack()}><Icon name="save" /> Редактирование недоступно</button></div></div><div className="history"><div className="section-heading"><div><p className="eyebrow">Прозрачность</p><h3>История записи</h3></div><Icon name="history" /></div>{release.audit.length ? release.audit.slice(-3).reverse().map((entry, index) => <div className="history-row" key={`${entry.action}-${index}`}><span className="history-dot" /><div><strong>{entry.action.replaceAll('_', ' ')}</strong><small>{entry.actor}</small></div></div>) : <span className="muted">Изменений пока нет.</span>}</div></> : <div className="detail-empty"><div className="empty-disc"><Icon name="disc" /></div><p className="eyebrow">Детали записи</p><h2>Выберите запись</h2><p>Здесь появятся источники, публикации и история метаданных.</p></div>}</section></div>
      </div>
    </main>
  </div>
}

createRoot(document.getElementById('root')!).render(<App />)
