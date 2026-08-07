import { useEffect, useMemo, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

type Tags = Record<string, string>
type Revision = { readonly source_id: string; readonly layer: string; readonly revision: number; readonly tags: Tags; readonly actor?: string; readonly created_at?: string }
type Source = { readonly source_id: string; readonly path: string; readonly format?: string; readonly sha256: string; readonly state: string; readonly origin?: string; readonly size_bytes?: number; readonly tag_observations?: readonly { readonly name: string; readonly value: string; readonly format: string }[]; readonly fingerprints?: readonly { readonly state: string; readonly fingerprint: string | null; readonly duration_seconds: number | null; readonly tool_version: string | null }[]; readonly provider_attempts?: readonly { readonly provider: string; readonly outcome: string; readonly snapshot_sha256: string }[] }
type Publication = { readonly publication_id: string; readonly path: string; readonly source_id: string; readonly metadata_revision_id?: number | null; readonly state: string; readonly created_at?: string }
type Summary = { readonly record_id: string; readonly source_state: string; readonly processing_state: string; readonly match_state: string; readonly publication_state: string; readonly metadata_state: string; readonly sources: readonly Source[]; readonly publications: readonly Publication[]; readonly metadata_revisions?: readonly Revision[] }
type Event = { readonly kind: string; readonly state: string; readonly reason: string | null; readonly details: Record<string, unknown>; readonly source_id?: string | null; readonly created_at: string }
type Detail = Summary & { readonly states: { readonly source: string; readonly processing: string; readonly match: string; readonly publication: string; readonly metadata: string }; readonly events: readonly Event[] }
type Screen = 'artists' | 'albums' | 'tracks' | 'track'
type Layer = 'original' | 'analyzed' | 'final'
type Route = { readonly screen: Screen; readonly artist?: string; readonly album?: string; readonly recordId?: string; readonly sourceId?: string }

const TAG_FIELDS = ['TITLE', 'ARTIST', 'ALBUM', 'ALBUMARTIST', 'DATE', 'ORIGINALDATE', 'GENRE', 'TRACKNUMBER', 'TRACKTOTAL', 'DISCNUMBER', 'DISCTOTAL', 'MUSICBRAINZ_TRACKID', 'MUSICBRAINZ_ALBUMID', 'MUSICBRAINZ_RELEASEGROUPID', 'ISRC'] as const

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, { headers: { 'content-type': 'application/json' }, ...options })
  const payload: unknown = await response.json().catch(() => ({}))
  if (!response.ok) {
    const detail = typeof payload === 'object' && payload !== null && 'detail' in payload ? payload.detail : null
    throw new Error(typeof detail === 'string' ? detail : 'Не удалось выполнить запрос')
  }
  return payload as T
}

function icon(path: string) { return <svg aria-hidden="true" className="icon" viewBox="0 0 24 24"><path d={path} /></svg> }
function tagsFor(item: Summary, sourceId: string, layer: Layer): Tags {
  const revision = item.metadata_revisions?.slice().reverse().find((entry) => entry.source_id === sourceId && entry.layer === layer)
  if (revision) return revision.tags
  if (layer !== 'original') return {}
  return Object.fromEntries((item.sources.find((source) => source.source_id === sourceId)?.tag_observations ?? []).map((tag) => [tag.name, tag.value]))
}
function titleFor(item: Summary, sourceId: string): string { return tagsFor(item, sourceId, 'final').TITLE || tagsFor(item, sourceId, 'original').TITLE || item.sources.find((source) => source.source_id === sourceId)?.path.split('/').at(-1) || item.record_id }
function artistFor(item: Summary, sourceId: string): string { return tagsFor(item, sourceId, 'final').ARTIST || tagsFor(item, sourceId, 'original').ARTIST || 'Неизвестный исполнитель' }
function albumFor(item: Summary, sourceId: string): string { return tagsFor(item, sourceId, 'final').ALBUM || tagsFor(item, sourceId, 'original').ALBUM || 'Без альбома' }
function decodeRoutePart(value: string | undefined): string { return value ? decodeURIComponent(value) : '' }
function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean)
  if (parts[0] !== 'library') return { screen: 'artists' }
  if (parts[1] === 'artist' && parts[3] === 'album' && parts[5] === 'track' && parts[7]) return { screen: 'track', artist: decodeRoutePart(parts[2]), album: decodeRoutePart(parts[4]), recordId: decodeRoutePart(parts[6]), sourceId: decodeRoutePart(parts[7]) }
  if (parts[1] === 'artist' && parts[3] === 'album' && parts[5] === 'tracks') return { screen: 'tracks', artist: decodeRoutePart(parts[2]), album: decodeRoutePart(parts[4]) }
  if (parts[1] === 'artist' && parts[3] === 'album') return { screen: 'albums', artist: decodeRoutePart(parts[2]), album: decodeRoutePart(parts[4]) }
  if (parts[1] === 'artist') return { screen: 'albums', artist: decodeRoutePart(parts[2]) }
  if (parts[1] === 'record' && parts[3] === 'source') return { screen: 'track', recordId: decodeRoutePart(parts[2]), sourceId: decodeRoutePart(parts[4]) }
  return { screen: 'artists' }
}
function routePath(route: Route): string {
  if (route.screen === 'track' && route.recordId && route.sourceId && route.artist && route.album) return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}/track/${encodeURIComponent(route.recordId)}/${encodeURIComponent(route.sourceId)}`
  if (route.screen === 'track' && route.recordId && route.sourceId) return `/library/record/${encodeURIComponent(route.recordId)}/source/${encodeURIComponent(route.sourceId)}`
  if (route.screen === 'tracks' && route.artist && route.album) return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}/tracks`
  if (route.screen === 'albums' && route.artist && route.album) return `/library/artist/${encodeURIComponent(route.artist)}/album/${encodeURIComponent(route.album)}`
  if (route.screen === 'albums' && route.artist) return `/library/artist/${encodeURIComponent(route.artist)}`
  return '/'
}

function App() {
  const [items, setItems] = useState<Summary[]>([])
  const [detail, setDetail] = useState<Detail | null>(null)
  const [initialRoute] = useState(() => parseRoute(window.location.pathname))
  const [screen, setScreen] = useState<Screen>(initialRoute.screen)
  const [artist, setArtist] = useState(initialRoute.artist ?? '')
  const [album, setAlbum] = useState(initialRoute.album ?? '')
  const [recordId, setRecordId] = useState(initialRoute.recordId ?? '')
  const [sourceId, setSourceId] = useState(initialRoute.sourceId ?? '')
  const [layer, setLayer] = useState<Layer>('final')
  const [draft, setDraft] = useState<Tags>({})
  const [query, setQuery] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [scanning, setScanning] = useState(false)
  const [saving, setSaving] = useState(false)
  const [reprocessing, setReprocessing] = useState(false)

  async function loadLibrary() {
    setLoading(true)
    try { const payload = await api<{ items?: Summary[] }>('/api/library/records'); setItems(payload.items ?? []) }
    catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось загрузить медиатеку') }
    finally { setLoading(false) }
  }
  function applyRoute(route: Route): void {
    setScreen(route.screen); setArtist(route.artist ?? ''); setAlbum(route.album ?? '')
    setRecordId(route.recordId ?? ''); setSourceId(route.sourceId ?? ''); setDetail(null); setLayer('final')
  }
  function navigate(route: Route): void {
    window.history.pushState({}, '', routePath(route)); applyRoute(route)
  }
  async function loadTrack(item: Summary, source: Source): Promise<void> {
    try { const loaded = await api<Detail>(`/api/library/records/${item.record_id}`); setDetail(loaded); setDraft({ ...tagsFor(loaded, source.source_id, 'final') }) }
    catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось открыть трек') }
  }
  async function scan() {
    setScanning(true); setNotice('Сканирование файлов и постановка анализа в очередь…')
    try { const result = await api<{ added: number; changed: number; removed: number; moved: number; queued_jobs: number }>('/api/reconciliation/scan', { method: 'POST' }); setNotice(`Сканирование завершено: добавлено ${result.added}, изменено ${result.changed}, в анализе ${result.queued_jobs}`); await loadLibrary() }
    catch (error) { setNotice(error instanceof Error ? error.message : 'Сканирование не удалось') }
    finally { setScanning(false) }
  }
  async function retryCurrentSource() {
    if (!recordId || !sourceId) return
    setReprocessing(true)
    try {
      const result = await api<{ queued: boolean }>(`/api/library/records/${recordId}/sources/${sourceId}/provider-retry`, { method: 'POST' })
      setNotice(result.queued ? 'Трек поставлен в очередь повторного анализа' : 'Трек уже находится в очереди анализа')
      await loadLibrary()
      const loaded = await api<Detail>(`/api/library/records/${recordId}`)
      setDetail(loaded)
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось поставить трек в очередь') }
    finally { setReprocessing(false) }
  }
  async function retryFailedProviders() {
    setReprocessing(true)
    try {
      const result = await api<{ queued: number }>('/api/library/providers/retry', { method: 'POST' })
      setNotice(`Поставлено в очередь повторного анализа: ${result.queued}`)
      await loadLibrary()
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось поставить провайдеры в очередь') }
    finally { setReprocessing(false) }
  }
  async function saveMetadata() {
    if (!detail || !sourceId) return
    setSaving(true)
    try { await api(`/api/library/records/${detail.record_id}/metadata`, { method: 'PUT', body: JSON.stringify({ source_id: sourceId, tags: draft }) }); setNotice('Финальные метаданные сохранены новой ревизией'); const loaded = await api<Detail>(`/api/library/records/${detail.record_id}`); setDetail(loaded); setDraft({ ...tagsFor(loaded, sourceId, 'final') }) }
    catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось сохранить метаданные') }
    finally { setSaving(false) }
  }
  useEffect(() => { void loadLibrary() }, [])
  useEffect(() => {
    const onPopState = () => applyRoute(parseRoute(window.location.pathname))
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])
  useEffect(() => {
    if (screen !== 'track' || !recordId || !sourceId || !items.length || detail) return
    const item = items.find((entry) => entry.record_id === recordId)
    const source = item?.sources.find((entry) => entry.source_id === sourceId)
    if (item && source) void loadTrack(item, source)
  }, [items, screen, recordId, sourceId, detail])

  const tracks = useMemo(() => items.flatMap((item) => item.sources.map((source) => ({ item, source }))).filter(({ item, source }) => `${artistFor(item, source.source_id)} ${albumFor(item, source.source_id)} ${titleFor(item, source.source_id)}`.toLowerCase().includes(query.toLowerCase())), [items, query])
  const artists = [...new Set(tracks.map(({ item, source }) => artistFor(item, source.source_id)))].sort()
  const albums = [...new Set(tracks.filter(({ item, source }) => artistFor(item, source.source_id) === artist).map(({ item, source }) => albumFor(item, source.source_id)))].sort()
  const albumTracks = tracks.filter(({ item, source }) => artistFor(item, source.source_id) === artist && albumFor(item, source.source_id) === album)
  const currentTrack = albumTracks.find(({ item, source }) => item.record_id === recordId && source.source_id === sourceId) ?? tracks.find(({ item, source }) => item.record_id === recordId && source.source_id === sourceId)
  const currentTags = detail && sourceId ? (layer === 'final' ? draft : tagsFor(detail, sourceId, layer)) : {}

  function back() { if (screen === 'track') navigate({ screen: 'tracks', artist, album }); else if (screen === 'tracks') navigate({ screen: 'albums', artist, album }); else if (screen === 'albums') navigate({ screen: 'artists' }) }
  return <div className="app-shell">
    <aside className="sidebar"><div className="brand"><div className="brand-mark">{icon('M9 18V6l10-2v12M9 18a3 3 0 1 1-3-3 3 3 0 0 1 3 3Zm10-2a3 3 0 1 1-3-3 3 3 0 0 1 3 3Z')}</div><div><strong>Music Ingest</strong><span>LIBRARY / REVIEW</span></div></div><nav><button className={screen !== 'track' ? 'nav-item active' : 'nav-item'} onClick={() => navigate({ screen: 'artists' })}>{icon('M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v13A1.5 1.5 0 0 1 18.5 20h-13A1.5 1.5 0 0 1 4 18.5v-13ZM7 8h10M7 12h10M7 16h6')}<span>Медиатека</span><b>{tracks.length}</b></button></nav><div className="sidebar-bottom"><div className="storage"><span>Состояние</span><strong>{items.filter((item) => item.processing_state !== 'complete').length ? 'Есть анализ' : 'Готово'}</strong><small>Исходники не изменяются</small></div><button className="nav-item quiet" onClick={() => void loadLibrary()}>{icon('M4 12a8 8 0 1 0 2.34-5.66L4 8.68M4 4v4.68h4.68')}<span>Обновить список</span></button></div></aside>
    <main className="workspace"><header className="topbar"><div className="breadcrumbs"><span>Music Ingest</span><i>/</i><strong>{screen === 'artists' ? 'Медиатека' : screen === 'albums' ? artist : screen === 'tracks' ? album : titleFor(detail ?? (currentTrack?.item ?? items[0] ?? { record_id: '', sources: [] }), sourceId)}</strong></div><div className="topbar-actions"><span className="sync">● Синхронизировано</span><button className="avatar">MI</button></div></header>
      <div className="content"><section className="hero"><div>{screen !== 'artists' && <button className="back" onClick={back}>← Назад</button>}<p className="eyebrow">{screen === 'track' ? 'Инспектор трека' : 'Ваша медиатека'}</p><h1>{screen === 'artists' ? 'Исполнители' : screen === 'albums' ? artist : screen === 'tracks' ? album : titleFor(detail ?? (currentTrack?.item ?? items[0] ?? { record_id: '', sources: [] }), sourceId)}</h1><p className="hero-copy">{screen === 'artists' ? 'Отдельный каталог артистов. Откройте исполнителя, чтобы увидеть его альбомы.' : screen === 'albums' ? 'Альбомы исполнителя и их состояние обработки.' : screen === 'tracks' ? 'Треки альбома. Выберите файл, чтобы сравнить слои и результат AcousticID.' : 'Исходные теги, анализ, AcousticID, финальная версия и история одной записи.'}</p></div><div className="hero-stat"><strong>{screen === 'artists' ? artists.length : screen === 'albums' ? albums.length : screen === 'tracks' ? albumTracks.length : '01'}</strong><span>{screen === 'artists' ? 'артистов' : screen === 'albums' ? 'альбомов' : screen === 'tracks' ? 'треков' : 'трек'}</span></div></section>
        {notice && <div className="notice-bar"><span>{notice}</span><button onClick={() => setNotice('')}>Скрыть</button></div>}
        {screen !== 'track' && <div className="library-toolbar"><label className="search">{icon('m20 20-4.5-4.5M10.75 17a6.25 6.25 0 1 0 0-12.5 6.25 6.25 0 0 0 0 12.5Z')}<input aria-label="Поиск" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Поиск по артисту, альбому или треку" /></label><div className="provider-actions"><button className="secondary" disabled={reprocessing} onClick={() => void retryFailedProviders()}>{reprocessing ? 'Ставим в очередь…' : 'Переотправить провайдерам'}</button><button className="primary scan" disabled={scanning || reprocessing} onClick={() => void scan()}>{scanning ? 'Сканируем…' : 'Сканировать файлы'}</button></div></div>}
        {loading ? <div className="empty-state">Загрузка медиатеки…</div> : screen === 'artists' ? <section className="catalog-grid">{artists.map((name) => <button className="entity-card" key={name} onClick={() => navigate({ screen: 'albums', artist: name })}><span className="entity-art">{name.slice(0, 1)}</span><span><strong>{name}</strong><small>{tracks.filter(({ item, source }) => artistFor(item, source.source_id) === name).length} треков</small></span><b>→</b></button>)}</section> : screen === 'albums' ? <section className="catalog-grid">{albums.map((name) => <button className="entity-card album-card" key={name} onClick={() => navigate({ screen: 'tracks', artist, album: name })}><span className="entity-art disc">◉</span><span><strong>{name}</strong><small>{tracks.filter(({ item, source }) => artistFor(item, source.source_id) === artist && albumFor(item, source.source_id) === name).length} треков</small></span><b>→</b></button>)}</section> : screen === 'tracks' ? <section className="track-screen"><div className="screen-heading"><div><p className="eyebrow">{albumTracks.length} файлов</p><h2>Список треков</h2></div><button className="secondary" onClick={() => void loadLibrary()}>Обновить список</button></div><div className="track-table">{albumTracks.map(({ item, source }, index) => <button className="track-line" key={`${item.record_id}-${source.source_id}`} onClick={() => navigate({ screen: 'track', recordId: item.record_id, sourceId: source.source_id, artist, album })}><b>{String(index + 1).padStart(2, '0')}</b><span><strong>{titleFor(item, source.source_id)}</strong><small>{source.path}</small></span><span className="track-meta">{item.match_state === 'matched' ? 'AcousticID найден' : 'Ожидает анализа'}</span><i>→</i></button>)}</div></section> : <TrackDetail detail={detail} sourceId={sourceId} layer={layer} setLayer={setLayer} tags={currentTags} draft={draft} setDraft={setDraft} saving={saving} reprocessing={reprocessing} onSave={() => void saveMetadata()} onRetry={() => void retryCurrentSource()} />}
      </div></main></div>
}

function TrackDetail({ detail, sourceId, layer, setLayer, tags, draft, setDraft, saving, reprocessing, onSave, onRetry }: { readonly detail: Detail | null; readonly sourceId: string; readonly layer: Layer; readonly setLayer: (value: Layer) => void; readonly tags: Tags; readonly draft: Tags; readonly setDraft: (value: Tags) => void; readonly saving: boolean; readonly reprocessing: boolean; readonly onSave: () => void; readonly onRetry: () => void }) {
  const source = detail?.sources.find((item) => item.source_id === sourceId)
  const attempts = source?.provider_attempts ?? []
  const fingerprint = source?.fingerprints?.at(-1)
  if (!detail || !source) return <div className="empty-state">Открываем данные трека…</div>
  const format = source.format?.toUpperCase() ?? source.path.split('.').at(-1)?.toUpperCase() ?? 'AUDIO'
  const visibleTagFields = [...new Set([...TAG_FIELDS, ...Object.keys(tags)])]
  return <section className="inspector"><div className="inspector-grid"><div><div className="file-card"><span className="entity-art disc">◉</span><div><p className="eyebrow">Исходный файл</p><strong>{source.path.split('/').at(-1)}</strong><small>{format} · {source.size_bytes ? `${Math.round(source.size_bytes / 1024)} KB` : 'размер неизвестен'} · {source.state}</small></div></div><div className="evidence-card"><div className="section-heading"><div><p className="eyebrow">Анализ файла</p><h2>Что найдено</h2></div><div className="provider-actions"><span className="badge success">{detail.states.match === 'matched' ? 'Совпадение' : 'Нужна проверка'}</span><button className="secondary" disabled={reprocessing} onClick={onRetry}>{reprocessing ? 'В очереди…' : 'Повторить анализ'}</button></div></div><div className="evidence-grid"><div><span>Fingerprint</span><strong>{fingerprint?.fingerprint ? 'Сформирован' : 'Не найден'}</strong><small>{fingerprint?.tool_version ?? 'инструмент не указан'}</small></div><div><span>AcousticID</span><strong>{attempts.slice().reverse().find((attempt) => attempt.provider === 'acoustid')?.outcome ?? 'Не запускался'}</strong><small>{attempts.length ? `проверок провайдеров: ${attempts.length}` : 'после сканирования появится здесь'}</small></div></div><p className="hash">SHA-256: {source.sha256}</p></div><div className="history-card"><p className="eyebrow">История</p>{detail.events.length ? detail.events.slice().reverse().map((event) => <div className="history-line" key={`${event.kind}-${event.created_at}`}><span /><div><strong>{event.kind.replaceAll('_', ' ')}</strong><small>{new Date(event.created_at).toLocaleString('ru-RU')} · {event.reason ?? event.state}</small></div></div>) : <p className="muted">История появится после обработки файла.</p>}</div></div><div className="metadata-card"><div className="section-heading"><div><p className="eyebrow">Метаданные</p><h2>Сравнение слоёв</h2></div><span className="revision">rev {detail.metadata_revisions?.filter((item) => item.source_id === sourceId && item.layer === 'final').at(-1)?.revision ?? 0}</span></div><div className="layer-tabs">{(['original', 'analyzed', 'final'] as const).map((value) => <button className={layer === value ? 'active' : ''} key={value} onClick={() => setLayer(value)}>{value === 'original' ? 'Исходные' : value === 'analyzed' ? 'Анализ' : 'Финальные'}</button>)}</div>{layer !== 'final' && <div className="read-only-note">Слой зафиксирован при сканировании и не редактируется. Переключитесь на «Финальные», чтобы внести правку.</div>}<div className="tag-grid">{visibleTagFields.map((field) => <label key={field}><span>{field}</span><input value={tags[field] ?? ''} readOnly={layer !== 'final'} onChange={(event) => setDraft({ ...draft, [field]: event.target.value })} /></label>)}</div><div className="editor-footer"><span className="muted">Исходные и анализ остаются неизменяемыми.</span><button className="primary" disabled={saving} onClick={onSave}>{saving ? 'Сохраняем…' : 'Сохранить финальные'}</button></div></div></div></section>
}

const root = document.getElementById('root')
if (root) createRoot(root).render(<App />)
