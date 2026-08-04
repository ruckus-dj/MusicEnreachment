import { useEffect, useMemo, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'

type Tags = Record<string, string>
type Track = { track_id: string; file_id?: string; position: number; title: string; relative_path?: string; layers?: Record<string, Tags>; final_revision?: number }
type Release = { release_id: string; title: string; incoming_folder: string; publication: { state: string }; review_state: string; tracks: Track[]; candidates: Array<{ key: string; confidence?: number; evidence: Record<string, unknown> }>; audit: Array<{ action: string; actor: string; details: Record<string, unknown> }> }
type QueueItem = { release_id: string; title: string; artist: string; album: string; incoming_folder: string; publication_state: string; review_state: string }
type ScanResult = { added: number; changed: number; removed: number; moved: number; unchanged: number; queued_jobs: number }
type MatchingSettings = { confidence_threshold: number }
type Layer = 'final' | 'original' | 'analyzed'

const TAG_FIELDS = ['TITLE', 'ARTIST', 'ALBUM', 'ALBUMARTIST', 'DATE', 'ORIGINALDATE', 'GENRE', 'TRACKNUMBER', 'TRACKTOTAL', 'DISCNUMBER', 'DISCTOTAL', 'MUSICBRAINZ_TRACKID', 'MUSICBRAINZ_ALBUMID', 'MUSICBRAINZ_RELEASEGROUPID', 'ISRC']

function Icon({ name }: { name: 'library' | 'disc' | 'folder' | 'search' | 'settings' | 'chevron' | 'save' | 'history' | 'music' }) {
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
  const [release, setRelease] = useState<Release | null>(null)
  const [selectedReleaseId, setSelectedReleaseId] = useState('')
  const [selectedTrackId, setSelectedTrackId] = useState('')
  const [query, setQuery] = useState('')
  const [view, setView] = useState<'library' | 'attention'>('library')
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
      const payload = await api<{ items: QueueItem[] }>('/api/release-review/queue')
      setQueue(payload.items)
      if (!selectedReleaseId) {
        const first = payload.items.find((item) => view === 'attention' ? item.review_state === 'needs_review' : true)
        if (first) setSelectedReleaseId(first.release_id)
      }
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось загрузить медиатеку') }
    finally { setLoading(false) }
  }

  async function loadRelease(id: string) {
    setSelectedReleaseId(id)
    setNotice('')
    try {
      const payload = await api<Release>(`/api/release-review/releases/${id}`)
      setRelease(payload)
      const track = payload.tracks.find((item) => item.track_id === selectedTrackId) ?? payload.tracks[0]
      if (track) chooseTrack(track)
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось открыть релиз') }
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

  useEffect(() => { void loadQueue() }, [])
  useEffect(() => {
    void api<MatchingSettings>('/api/settings/matching').then((payload) => {
      setThreshold(payload.confidence_threshold)
      setThresholdDraft(payload.confidence_threshold)
    }).catch((error: unknown) => setNotice(error instanceof Error ? error.message : 'Не удалось загрузить настройки'))
  }, [])
  useEffect(() => { if (selectedReleaseId) void loadRelease(selectedReleaseId) }, [selectedReleaseId])

  const visibleQueue = useMemo(() => queue.filter((item) => {
    const haystack = `${item.incoming_folder} ${item.release_id} ${item.title} ${item.artist} ${item.album}`.toLowerCase()
    return haystack.includes(query.toLowerCase()) && (view === 'library' || item.review_state === 'needs_review')
  }), [queue, query, view])
  const groups = useMemo(() => {
    const map = new Map<string, QueueItem[]>()
    visibleQueue.forEach((item) => {
      const parts = item.incoming_folder.split('/').filter(Boolean)
      const key = group === 'folder' ? parts.at(-1) || 'Корень' : group === 'album' ? item.album || item.title : item.artist || 'Неизвестный исполнитель'
      map.set(key, [...(map.get(key) ?? []), item])
    })
    return [...map.entries()]
  }, [visibleQueue, group])
  const selectedTrack = release?.tracks.find((item) => item.track_id === selectedTrackId) ?? null
  const artist = selectedTrack?.layers?.final?.ARTIST || 'Неизвестный исполнитель'
  const album = selectedTrack?.layers?.final?.ALBUM || release?.title || 'Без альбома'
  const visibleTags = layer === 'final' ? tags : (selectedTrack?.layers?.[layer] ?? {})
  const canEdit = layer === 'final' && selectedTrack?.final_revision !== undefined && Boolean(selectedTrack.layers?.final)

  function chooseTrack(track: Track) {
    setSelectedTrackId(track.track_id)
    setTags({ ...(track.layers?.final ?? {}) })
    setLayer('final')
  }

  async function saveTrack() {
    if (!selectedTrack || selectedTrack.final_revision === undefined || !canEdit) return
    try {
      await api(`/api/release-review/tracks/${selectedTrack.track_id}/tags`, { method: 'PATCH', body: JSON.stringify({ revision: selectedTrack.final_revision, tags }) })
      setNotice('Теги сохранены и опубликованы')
      await loadRelease(selectedReleaseId)
      await loadQueue()
    } catch (error) { setNotice(error instanceof Error ? error.message : 'Не удалось сохранить теги') }
  }

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark"><Icon name="music" /></div><div><strong>Music Ingest</strong><span>LOCAL LIBRARY</span></div></div>
      <nav className="nav" aria-label="Основная навигация">
        <button className={view === 'library' ? 'nav-item active' : 'nav-item'} onClick={() => setView('library')}><Icon name="library" /> Медиатека <span>{queue.length}</span></button>
        <button className={view === 'attention' ? 'nav-item active' : 'nav-item'} onClick={() => setView('attention')}><Icon name="settings" /> Требует внимания <span className="nav-alert">{queue.filter((item) => item.review_state === 'needs_review').length}</span></button>
      </nav>
        <div className="sidebar-bottom"><div className="storage"><div className="storage-label"><span>Хранилище</span><b>FLAC</b></div><div className="progress"><i /></div><small>Локальная библиотека · готова</small></div><button className="nav-item quiet"><Icon name="settings" /> Настройки</button></div>
    </aside>
    <main className="workspace">
      <header className="topbar"><div className="breadcrumbs"><span>Коллекция</span><Icon name="chevron" /><strong>{view === 'library' ? 'Медиатека' : 'Требует внимания'}</strong></div><div className="topbar-actions"><span className="live-dot">Синхронизировано</span><button className="avatar" aria-label="Профиль">Р</button></div></header>
      <div className="content">
        <section className="hero"><div><p className="eyebrow">{view === 'library' ? 'Ваша коллекция' : 'Нужна проверка'}</p><h1>{view === 'library' ? 'Медиатека' : 'Проверьте эти релизы'}</h1><p className="hero-copy">{view === 'library' ? 'Все релизы, треки и теги в одном спокойном рабочем пространстве.' : 'Низкая уверенность и неоднозначные совпадения собраны здесь.'}</p></div><div className="hero-stat"><strong>{queue.length.toString().padStart(2, '0')}</strong><span>релизов<br />в каталоге</span></div></section>
         <div className="toolbar"><label className="search"><span className="sr-only">Поиск по медиатеке</span><Icon name="search" /><input aria-label="Поиск по медиатеке" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Поиск по папке, исполнителю или альбому" /></label><div className="toolbar-controls"><span>Группировать:</span><button aria-pressed={group === 'folder'} className={group === 'folder' ? 'seg active' : 'seg'} onClick={() => setGroup('folder')}><Icon name="folder" /> Папки</button><button aria-pressed={group === 'artist'} className={group === 'artist' ? 'seg active' : 'seg'} onClick={() => setGroup('artist')}><Icon name="music" /> Исполнители</button><button aria-pressed={group === 'album'} className={group === 'album' ? 'seg active' : 'seg'} onClick={() => setGroup('album')}><Icon name="disc" /> Альбомы</button></div></div><div className="mobile-threshold-control"><div className="threshold-heading"><label htmlFor="mobile-confidence-threshold">Порог совпадения</label><output>{Math.round(thresholdDraft * 100)}%</output></div><input id="mobile-confidence-threshold" type="range" min="0" max="1" step="0.01" value={thresholdDraft} onChange={(event) => setThresholdDraft(Number(event.target.value))} /><button className="threshold-save" disabled={thresholdSaving || thresholdDraft === threshold} onClick={() => void saveThreshold()}>{thresholdSaving ? 'Сохранение…' : 'Сохранить порог'}</button><small>Меняется без перезапуска</small></div>
         <div className="library-grid"><section className="release-panel"><div className="section-heading"><div><p className="eyebrow">{visibleQueue.length} результатов</p><h2>Каталог релизов</h2></div><div className="section-actions"><button className="refresh" disabled={scanLoading} onClick={() => void scanIncoming()}>{scanLoading ? 'Сканирование…' : 'Сканировать файлы'}</button><button className="refresh" onClick={() => void loadQueue()}>Обновить</button></div></div>{loading ? <div className="empty-state">Загрузка каталога…</div> : groups.length ? groups.map(([key, items]) => <div className="group" key={key}><div className="group-title"><Icon name={group === 'folder' ? 'folder' : group === 'album' ? 'disc' : 'music'} /><span>{key}</span><small>{items.length}</small></div>{items.map((item) => <button className={item.release_id === selectedReleaseId ? 'release-row selected' : 'release-row'} key={item.release_id} onClick={() => setSelectedReleaseId(item.release_id)}><span className="cover"><Icon name="disc" /></span><span className="release-copy"><strong>{item.title || item.release_id}</strong><small>{item.artist || item.incoming_folder}</small></span><span className={item.review_state === 'needs_review' ? 'badge warning' : 'badge'}>{item.review_state === 'needs_review' ? 'Проверить' : 'Готов'}</span><Icon name="chevron" /></button>)}</div>) : <div className="empty-state"><Icon name="library" /><strong>Релизы не найдены</strong><span>Измените запрос или дождитесь публикации новых файлов.</span></div>}</section>
          <section className="detail-panel">{release && selectedTrack ? <><div className="detail-header"><div><p className="eyebrow">{artist}</p><h2>{album}</h2><span className="path">{release.incoming_folder}</span></div><span className="status"><i /> {release.publication.state}</span></div><div className="track-list"><div className="track-list-head"><span>Трек</span><span>Файл</span><span>Состояние</span></div>{release.tracks.map((track) => <button className={track.track_id === selectedTrack.track_id ? 'track-row selected' : 'track-row'} key={track.track_id} onClick={() => chooseTrack(track)}><span className="track-name"><b>{String(track.position).padStart(2, '0')}</b><strong>{track.title || track.layers?.final?.TITLE || 'Без названия'}</strong></span><span>{track.relative_path?.split('/').at(-1) || 'FLAC'}</span><span className="track-state"><i /> {track.final_revision === undefined ? 'несколько файлов' : `ревизия ${track.final_revision}`}</span></button>)}</div><div className="editor"><div className="editor-heading"><div><p className="eyebrow">Редактор тегов</p><h3>{selectedTrack.title || tags.TITLE || 'Выбранный трек'}</h3></div><span className="revision">v{selectedTrack.final_revision ?? '—'}</span></div><div className="layer-tabs"><button aria-pressed={layer === 'final'} className={layer === 'final' ? 'active' : ''} onClick={() => setLayer('final')}>Final · редактируется</button><button aria-pressed={layer === 'original'} className={layer === 'original' ? 'active' : ''} onClick={() => setLayer('original')}>Original · только чтение</button><button aria-pressed={layer === 'analyzed'} className={layer === 'analyzed' ? 'active' : ''} onClick={() => setLayer('analyzed')}>Analyzed · кандидат</button></div><div className="tag-grid">{TAG_FIELDS.map((field) => <label key={field}><span>{field}</span><input readOnly={!canEdit} value={visibleTags[field] ?? ''} onChange={(event) => setTags({ ...tags, [field]: event.target.value })} placeholder="Не задано" /></label>)}</div><div className="editor-footer"><span className="notice" role="status">{notice || (!selectedTrack.layers ? 'У этого трека несколько файлов: редактирование доступно после выбора конкретного файла.' : layer !== 'final' ? 'Слой доступен только для просмотра.' : '')}</span><button disabled={!canEdit} className="primary" onClick={() => void saveTrack()}><Icon name="save" /> Сохранить изменения</button></div></div><div className="history"><div className="section-heading"><div><p className="eyebrow">Прозрачность</p><h3>История релиза</h3></div><Icon name="history" /></div>{release.audit.length ? release.audit.slice(-3).reverse().map((entry, index) => <div className="history-row" key={`${entry.action}-${index}`}><span className="history-dot" /><div><strong>{entry.action.replaceAll('_', ' ')}</strong><small>{entry.actor}</small></div></div>) : <span className="muted">Изменений пока нет.</span>}</div></> : <div className="detail-empty"><div className="empty-disc"><Icon name="disc" /></div><p className="eyebrow">Детали библиотеки</p><h2>Выберите релиз</h2><p>Здесь появятся треки, слои тегов и история публикации.</p></div>}</section></div>
      </div>
    </main>
  </div>
}

createRoot(document.getElementById('root')!).render(<App />)
