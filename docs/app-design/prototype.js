/// <reference path="./catalog-data.js" />
const view = document.querySelector('#view');
const dialog = document.querySelector('#confirmation');
const folderDialog = document.querySelector('#folder-picker');
const ui = { groupId: 'north', selectedFile: null, selectedTarget: null, inspector: 'tags', tagScope: 'track', differencesOnly: false, libraryView: 'artists', libraryQuery: '', publicationFilter: 'all', availabilityFilter: 'all', librarySelection: new Set(), libraryParentSelection: new Map(), excludedLibraryTracks: new Set(), detailsOpen: new Map(), bulkRows: null, bulkPreviewVersion: null, contextPreviewTrackId: null, absentEdition: {}, removedSourceRoots: new Set(), inventoryVersion: 0, providerQuery: 'Northern Lines', providerResults: null, recordingQuery: '', recordingResults: null, recordingTarget: null, plan: null, manualDraft: null, manualReleaseCount: 0, notice: '', demoDeleteOperations: [], toolsChecked: false, toolsUpdated: false, setupStep: 0, toolsPath: '/var/lib/melotrove/tools', pathChecked: false, sourceAddOpen: false, sourceRoots: null, sourcePath: '/mnt/music', sourceMode: '', sourceWorkDirectory: '', outputCandidate: '', outputDemoExecution: 'idle', folderPicker: null };
let confirmAction = null;
let draggedFile = null;
const esc = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
const badge = (text, tone = '') => `<span class="status ${tone}">${esc(text)}</span>`;
const button = (action, text, attrs = '', primary = false) => `<button type="button" data-action="${action}" ${attrs} class="${primary ? 'primary' : ''}">${text}</button>`;
const link = (route, text, className = '') => `<a href="#${route}" class="${className}">${esc(text)}</a>`;
const duration = seconds => Number.isFinite(seconds) ? `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, '0')}` : '—';
const russianCount = (count, one, few, many) => {
  const remainder100 = Math.abs(count) % 100;
  const remainder10 = remainder100 % 10;
  const word = remainder100 >= 11 && remainder100 <= 14 ? many : remainder10 === 1 ? one : remainder10 >= 2 && remainder10 <= 4 ? few : many;
  return `${count} ${word}`;
};
const cover = (release, size = '') => `<span class="cover ${release.color || 'clay'} ${size}" aria-hidden="true">${esc(release.title?.[0] || release.name?.[0] || 'M')}</span>`;
const panel = (title, content, extra = '') => `<section class="panel"><div class="panel-heading"><h2>${title}</h2>${extra}</div>${content}</section>`;
const note = (text, tone = '') => `<div class="note ${tone}">${text}</div>`;
const header = (title, description, actions = '') => `<div class="page-header"><div><h1 tabindex="-1">${esc(title)}</h1><p class="muted">${description}</p></div><div class="actions">${actions}</div></div>`;
const table = (heads, rows, className = '', rowClasses = []) => `<div class="table-wrap ${className}"><table><thead><tr>${heads.map(head => `<th scope="col">${head}</th>`).join('')}</tr></thead><tbody>${rows.length ? rows.map((row, index) => `<tr${rowClasses[index] ? ` class="${rowClasses[index]}"` : ''}>${row.map((cell, i) => `<td data-label="${esc(heads[i].replace(/<[^>]*>/g, ''))}">${cell}</td>`).join('')}</tr>`).join('') : `<tr><td colspan="${heads.length}"><div class="empty">Нет результатов. Измените запрос или фильтр.</div></td></tr>`}</tbody></table></div>`;
const facts = items => `<dl class="facts">${items.map(([key, value]) => `<div><dt>${esc(key)}</dt><dd>${value}</dd></div>`).join('')}</dl>`;
const artistName = id => demoArtist(id)?.name || 'Артист не указан';
const allTracks = () => localReleases.flatMap(release => release.tracks.map(track => ({ release, track })));
const artistReleases = id => localReleases.filter(release => release.credits?.some(credit => credit.artistId === id) || release.artistId === id);
const trackSelectionKey = id => `track:${id}`;
const selectedTrack = id => !ui.excludedLibraryTracks.has(id) && (ui.librarySelection.has(trackSelectionKey(id)) || [...ui.libraryParentSelection.values()].some(ids => ids.has(id)));
const selectedTrackIds = () => {
  const ids = new Set([...ui.librarySelection].map(key => key.slice('track:'.length)));
  for (const parentTracks of ui.libraryParentSelection.values()) for (const id of parentTracks) ids.add(id);
  for (const id of ui.excludedLibraryTracks) ids.delete(id);
  return [...ids];
};
const setTrackSelection = (ids, selected) => ids.forEach(id => {
  if (selected) {
    ui.librarySelection.add(trackSelectionKey(id));
    ui.excludedLibraryTracks.delete(id);
  } else {
    ui.librarySelection.delete(trackSelectionKey(id));
    ui.excludedLibraryTracks.add(id);
  }
});
const setParentSelection = (kind, id, selected, trackIds) => {
  const key = `${kind}:${id}`;
  if (!selected) {
    ui.libraryParentSelection.delete(key);
    trackIds.forEach(trackId => ui.excludedLibraryTracks.add(trackId));
    return;
  }
  ui.libraryParentSelection.set(key, new Set(trackIds));
  trackIds.forEach(trackId => ui.excludedLibraryTracks.delete(trackId));
};
const selectedCount = ids => ids.filter(selectedTrack).length;
function selectionInput(kind, id, label, trackIds, compact = false) {
  const checkedCount = selectedCount(trackIds);
  const checked = trackIds.length > 0 && checkedCount === trackIds.length;
  const mixed = checkedCount > 0 && checkedCount < trackIds.length;
  const disabled = trackIds.length === 0;
  return `<label class="selection-check${compact ? ' selection-check-compact' : ''}"><input type="checkbox" data-select-${kind}="${esc(id)}" data-selection-children="${esc(JSON.stringify(trackIds))}" aria-label="${esc(label)}${disabled ? ': нет позиций в медиатеке' : ''}" aria-checked="${mixed ? 'mixed' : checked}" ${checked ? 'checked' : ''} ${disabled ? 'disabled' : ''}><span${compact ? ' class="sr-only"' : ''}>${disabled ? 'Нет позиций' : mixed ? 'Выбрано частично' : label}</span></label>`;
}
function syncSelectionControls(root = view) {
  for (const input of root.querySelectorAll('input[data-selection-children]')) {
    const ids = JSON.parse(input.dataset.selectionChildren || '[]');
    const count = selectedCount(ids);
    input.indeterminate = count > 0 && count < ids.length;
    input.checked = ids.length > 0 && count === ids.length;
    input.setAttribute('aria-checked', input.indeterminate ? 'mixed' : String(input.checked));
    const label = input.closest('label');
    const text = label?.querySelector('span');
    if (text && !input.disabled) text.textContent = input.indeterminate ? 'Выбрано частично' : input.checked ? 'Выбраны все позиции' : 'Выбрать все позиции';
  }
}
function preserveDetails(root = view) {
  for (const details of root.querySelectorAll('details[data-detail-key]')) {
    const key = details.dataset.detailKey;
    if (ui.detailsOpen.has(key)) details.open = ui.detailsOpen.get(key);
    else ui.detailsOpen.set(key, details.open);
  }
}
function captureDetails(root = view) {
  for (const details of root.querySelectorAll('details[data-detail-key]')) ui.detailsOpen.set(details.dataset.detailKey, details.open);
}
const group = () => incomingGroups.find(item => item.id === ui.groupId);
const localRelease = id => localReleases.find(release => release.id === id);
const findProviderTrack = id => {
  for (const release of providerReleases) {
    const track = release.tracks.find(item => item.id === id);
    if (track) return { release, track };
  }
  return null;
};
const findLocalTrack = id => allTracks().find(item => item.track.id === id);
const normalized = value => String(value ?? '').normalize('NFKC').toLocaleLowerCase().replace(/\s+/g, ' ').trim();
const publicationCountLabel = (published, total, known) => known && total > 0 ? `${published}/${total} · ${published === total ? 'Полностью опубликовано' : published ? 'Частично опубликовано' : 'Не опубликовано'}` : `${published} опубликовано · полнота неизвестна`;

function currentTarget(g = group()) {
  return g.mode === 'local' ? localRelease(g.releaseId) : providerReleases.find(release => release.id === g.candidateId);
}

function draft(g = group()) {
  const target = currentTarget(g);
  const key = g.mode === 'local' ? 'local' : g.candidateId;
  if (!g.drafts[key]) {
    const assignments = {};
    for (const track of target.tracks) {
      const file = g.fileIds.map(id => demoFiles[id]).filter(Boolean).find(item => g.mode === 'local' ? item.id === track.sourceId : item.providerTrackId === track.id && item.confidence === 'high');
      if (file) {
        const method = g.mode === 'local' ? 'Из исходной группы' : 'Авто · тестовый вердикт';
        assignments[track.id] = { sourceIds: [file.id], selectedSourceId: file.id, sourceMeta: { [file.id]: { method, confidence: file.confidence } }, method, confidence: file.confidence, providerTrackId: g.mode === 'local' ? track.providerTrackId : track.id, included: true };
      }
    }
    g.drafts[key] = assignments;
  }
  return g.drafts[key];
}

const associatedSourceIds = assignment => assignment?.sourceIds || (assignment?.fileId ? [assignment.fileId] : []);
const selectedSourceId = assignment => assignment?.selectedSourceId || assignment?.fileId || null;
const assignmentMethod = assignment => {
  const selected = selectedSourceId(assignment);
  return selected ? assignment?.sourceMeta?.[selected]?.method || assignment?.method || '' : '';
};
const assignmentConfidence = assignment => {
  const selected = selectedSourceId(assignment);
  return selected && demoFiles[selected] ? demoFiles[selected].confidence : null;
};

function targetLocalRelease(g, target) {
  if (g.mode === 'local') return localRelease(g.releaseId);
  return localReleases.find(release => release.providerId === target.id) || null;
}

function tagsForLocal(release, track) {
  return providerTags(release, track);
}

const tagFields = {
  artist: ['ARTIST', 'ARTISTSORT', 'MUSICBRAINZ_ARTISTID'],
  release: ['ALBUM', 'ALBUMARTIST', 'DATE', 'COUNTRY', 'LABEL', 'CATALOGNUMBER', 'MUSICBRAINZ_ALBUMID'],
  track: ['TITLE', 'ARTIST', 'TRACKNUMBER', 'DISCNUMBER', 'GENRE', 'MUSICBRAINZ_TRACKID', 'COMMENT']
};
const showTag = value => value?.length ? value.map(item => esc(item)).join('<br>') : '<span class="muted">—</span>';

function tagMatrix(release, track, file, provider, published = track?.published, context = '') {
  const sourceTags = file?.tags || {};
  const remoteTags = provider ? providerTags(provider.release, provider.track) : {};
  const localTags = track ? tagsForLocal(release, track) : {};
  const publicationTags = published?.tags || {};
  const rows = tagFields[ui.tagScope].filter(key => !ui.differencesOnly || new Set([sourceTags, remoteTags, localTags, publicationTags].map(tags => JSON.stringify(tags[key] || []))).size > 1).map(key => {
    const different = new Set([sourceTags, remoteTags, localTags, publicationTags].map(tags => JSON.stringify(tags[key] || []))).size > 1;
    return [`<span class="mono ${different ? 'difference' : ''}">${key}</span>`, showTag(sourceTags[key]), provider ? showTag(remoteTags[key]) : '<span class="muted">Нет связи</span>', showTag(localTags[key]), published ? showTag(publicationTags[key]) : '<span class="muted">Не опубликован</span>'];
  });
  return `<div class="matrix-toolbar"><div class="segments" aria-label="Область тегов">${[['artist', 'Артист'], ['release', 'Альбом'], ['track', 'Трек']].map(([id, title]) => `<button data-scope="${id}" aria-pressed="${ui.tagScope === id}">${title}</button>`).join('')}</div><label class="inline-check"><input type="checkbox" id="differences-only" ${ui.differencesOnly ? 'checked' : ''}>Только отличия</label><span class="muted small">${esc(context)}</span></div>${table(['Поле', 'Исходный файл', 'Провайдер · кэш', 'Локальная медиатека', 'Сейчас в публикации'], rows, 'tag-matrix')}<div class="panel-foot mono">${esc(file?.path || 'Исходник не выбран')}<br>${published ? `Публикация: ${esc(published.path)} · чтение ${esc(published.readAt)} (демо)` : 'Публикация отсутствует; пустая колонка не подменяется текущими метаданными.'}</div>`;
}

function evidence(file, target, assignment) {
  if (!file || !target) return '<div class="empty">Выберите строку и файл: здесь будут сравнения признаков.</div>';
  const rawTitle = file.tags.TITLE?.join('; ') || '';
  const rawArtist = file.tags.ARTIST?.join('; ') || '';
  const remoteArtist = artistName(target.artistId);
  const compare = (source, remote) => normalized(source) === normalized(remote) ? badge('Совпало после нормализации', 'success') : badge('Различается', 'warning');
  const delta = file.duration - target.duration;
  return `<div class="evidence-summary">${badge(assignmentMethod(assignment).startsWith('Вручную') ? 'Назначено оператором' : file.confidence === 'high' ? 'Высокая уверенность · демонстрационные данные' : 'Недостаточная уверенность · демонстрационные данные', file.confidence === 'high' ? 'success' : 'warning')}<strong>Числовая оценка: не рассчитана</strong><span class="muted">Макет не вычисляет уверенность и её вклады.</span></div>${table(['Признак', 'Исходное значение', 'Кандидат', 'Результат / отличие', 'Вес / вклад'], [
    ['Название', esc(rawTitle), esc(target.title), compare(rawTitle, target.title), 'Не моделируется'],
    ['Артист', esc(rawArtist), esc(remoteArtist), compare(rawArtist, remoteArtist), 'Не моделируется'],
    ['Длительность', duration(file.duration), duration(target.duration), badge(`${delta > 0 ? '+' : ''}${delta} с`, delta ? 'warning' : 'success'), 'Не моделируется'],
    ['Номер позиции', showTag(file.tags.TRACKNUMBER), String(target.position), file.tags.TRACKNUMBER?.[0] === String(target.position) ? 'Совпадает' : 'Отличается / не применим к сборнику', 'Не моделируется'],
    ['MBID композиции', showTag(file.tags.MUSICBRAINZ_TRACKID), esc(target.recordingId || 'Не указан'), 'В исходнике нет MBID; совпадение не подтверждено идентификатором', 'Не моделируется'],
    ['Отпечаток', esc(file.fingerprint || 'Нет результата'), 'AcoustID / Chromaprint', 'Не используется как доказанное совпадение', 'Не моделируется']
  ])}<div class="panel-foot">Показаны только сравнения демонстрационных значений. Нормализация строк — часть визуальной демонстрации, не расчёт утверждённой политики сопоставления. Ручное назначение не меняет числовую уверенность в этом макете: она не рассчитывается.</div>`;
}

function review() {
  const rows = incomingGroups.map(g => {
    const target = currentTarget(g);
    const assignments = draft(g);
    const used = new Set(Object.values(assignments).flatMap(associatedSourceIds));
    const published = targetLocalRelease(g, target)?.tracks.filter(track => track.published).length || 0;
    const fileCount = new Set(g.fileIds).size;
    const assignedPositions = Object.values(assignments).filter(item => associatedSourceIds(item).length).length;
    return [`<div class="entity">${cover(localRelease(g.releaseId))}<div>${link(`match/${g.id}`, g.title)}<span class="sub">${artistName(g.artistId)} · <span class="mono">/mnt/inbox/${g.id}</span></span></div></div>`, `${fileCount} файлов`, `${assignedPositions} / ${target.tracks.length}`, `${fileCount - used.size}`, badge(g.processed ? 'Опубликовано автоматически' : g.autoEligible ? 'Готов к автоматике' : 'Ручная обработка', g.processed || g.autoEligible ? 'success' : 'warning'), `${published} / ${target.tracks.length}`, `<span class="small">${esc(g.reason)}</span>`, link(`match/${g.id}`, 'Открыть', 'button')];
  });
  return header('Входящие альбомы', 'Группы из исходных тегов → поиск издания → назначения → публикация. Неполные данные допустимы.', button('run-auto', 'Запустить обработку · демо', '', true)) +
    `<div class="flow-strip"><span><b>1</b> Сгруппировать файлы</span><span><b>2</b> Сопоставить</span><span><b>3</b> Все позиции уверенные → автоматически</span><span><b>4</b> Иначе → ручной разбор, в том числе частичный</span></div>` +
    panel('Текущие группы', table(['Альбом / папка', 'Входящие', 'Назначено', 'Свободно', 'Режим', 'Опубликовано', 'Причина', ''], rows)) +
    `<div class="two-columns">${panel('Как пройти ручной сценарий', '<ol class="compact-list"><li>Откройте Northern Lines: 7 уверенных назначений уже заполнены.</li><li>Перетащите 03 — Northbound (live?) на позицию 03. Проверьте отличие длительности.</li><li>Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.</li><li>Предпросмотр → подтвердить публикацию выбранных позиций.</li></ol>')}${panel('Сборник, которого нет у провайдера', `<p class="padded">Road Notes сохраняет собственное название и порядок. Для каждой позиции можно найти композицию в другом альбоме. Альбом провайдера не становится названием сборника.</p><div class="panel-foot">${link('match/mix', 'Открыть локальный сборник', 'button')}</div>`)}</div>`;
}

function candidateList() {
  const g = group();
  const candidates = ui.providerResults || providerReleases.filter(release => g.mode === 'provider' && release.title.toLowerCase().includes(currentTarget(g).title.toLowerCase()));
  return `<form id="provider-search" class="search-form"><label for="provider-query">Поиск изданий · текстом</label><div class="input-action"><input id="provider-query" name="query" value="${esc(ui.providerQuery)}" placeholder="Артист, альбом, каталог…"><button type="submit">Найти</button></div></form><p class="small muted inset">Локальные демонстрационные данные MusicBrainz, не реальный API.</p><div class="candidate-list">${candidates.length ? candidates.map(release => `<button class="candidate ${g.mode === 'provider' && g.candidateId === release.id ? 'selected' : ''}" data-candidate="${release.id}" aria-pressed="${g.mode === 'provider' && g.candidateId === release.id}"><span class="entity">${cover(release)}<span><strong>${esc(release.title)}</strong><span class="sub">${artistName(release.artistId)} · ${release.year} · ${release.country}</span></span></span><span class="sub">${release.medium} · ${release.tracks.length} позиций · ${esc(release.catalog)}</span><span class="mono">${release.id}</span></button>`).join('') : '<div class="empty">Ничего не найдено. Попробуйте название композиции в локальном режиме.</div>'}</div><div class="panel-foot">${button('local-mode', 'Локальный релиз / сборник', `aria-pressed="${g.mode === 'local'}"`)}</div>`;
}

function fileChip(file, compact = false) {
  return `<button class="file-chip ${ui.selectedFile === file.id ? 'selected' : ''}" draggable="true" data-file="${file.id}" aria-pressed="${ui.selectedFile === file.id}" title="Выбрать или перетащить файл"><span class="grip" aria-hidden="true">⠿</span><span><strong>${esc(file.name)}</strong><span class="sub">${file.codec} · ${duration(file.duration)}${compact ? '' : ` · ${file.codec === 'FLAC' ? '24 бит / 48 кГц' : '320 кбит/с'} · ${file.size}`}</span></span></button>`;
}

function match() {
  const g = group();
  const target = currentTarget(g);
  const assignments = draft(g);
  if (!target.tracks.some(track => track.id === ui.selectedTarget)) ui.selectedTarget = target.tracks[0].id;
  const used = new Set(Object.values(assignments).flatMap(associatedSourceIds));
  const free = [...new Set(g.fileIds)].filter(id => !used.has(id) && demoFiles[id]?.available);
  const included = Object.values(assignments).filter(item => item.included && demoFiles[selectedSourceId(item)]).length;
  const rows = target.tracks.map(track => {
    const assignment = assignments[track.id];
    const sources = associatedSourceIds(assignment).map(id => demoFiles[id]).filter(Boolean);
    const selectedId = selectedSourceId(assignment);
    const file = demoFiles[selectedId];
    const remote = g.mode === 'local' ? findProviderTrack(assignment?.providerTrackId) : { release: target, track };
    const sourceChoices = sources.map(source => `<span class="linked-source"><input type="radio" name="selected-source-${esc(track.id)}" data-selected-source="${esc(track.id)}" value="${esc(source.id)}" aria-label="Выбрать ${esc(source.name)} для публикации" ${source.id === selectedId ? 'checked' : ''}>${fileChip(source, true)}${button('unassign-source', '×', `data-target="${esc(track.id)}" data-source="${esc(source.id)}" aria-label="Снять связь ${esc(source.name)} с ${esc(track.title)}"`)}</span>`).join('');
    return `<tr class="drop-row ${track.id === ui.selectedTarget ? 'inspected' : ''} ${sources.length ? 'assigned' : 'unassigned'}" data-drop="${track.id}"><td data-label="Выбор"><input type="checkbox" data-include="${track.id}" aria-label="Публиковать ${esc(track.title)}" ${assignment?.included && file ? 'checked' : ''} ${!file ? 'disabled' : ''}></td><td data-label="№" class="mono">${String(track.position).padStart(2, '0')}</td><td data-label="Композиция"><button class="text-button" data-inspect="${track.id}">${esc(track.title)}</button><span class="sub">${artistName(track.artistId)}${g.mode === 'local' ? ` · ${remote ? esc(remote.release.title) : 'Без связи с провайдером'}` : ''}</span></td><td data-label="Длительность" class="mono">${duration(track.duration)}</td><td data-label="Наш файл">${sources.length ? `<div class="linked-sources">${sourceChoices}</div>` : '<span class="drop-hint">Перетащите файл сюда</span>'}${ui.selectedFile ? button('assign-selected', 'Добавить вариант', `data-target="${track.id}"`) : ''}</td><td data-label="Оценка / доказательства">${file ? `<button class="score-button" data-evidence="${track.id}">${badge(assignmentMethod(assignment).startsWith('Вручную') ? 'Вручную' : assignmentConfidence(assignment) === 'high' ? 'Высокий*' : 'Низкий*', assignmentConfidence(assignment) === 'high' ? 'success' : 'warning')}<span class="sub">Детали</span></button>` : badge(sources.length ? 'Выберите источник' : 'Нет файла')}${g.mode === 'local' ? button('find-recording', 'Найти композицию', `data-target="${track.id}"`) : ''}</td></tr>`;
  }).join('');
  const inspected = target.tracks.find(track => track.id === ui.selectedTarget);
  const assignment = assignments[inspected.id];
  const file = demoFiles[selectedSourceId(assignment)];
  const provider = g.mode === 'local' ? findProviderTrack(assignment?.providerTrackId) : { release: target, track: inspected };
  const existing = targetLocalRelease(g, target);
  const localTrack = existing?.tracks.find(track => track.position === inspected.position);
  const projectedTrack = localTrack || { ...inspected, published: null };
  const inspectorContent = ui.inspector === 'evidence' ? evidence(file, provider?.track || inspected, assignment) : tagMatrix(existing || target, projectedTrack, file, provider, localTrack?.published, 'Артист/альбом: значения выбранного файла');
  return header(`Разбор: ${g.title}`, `${g.mode === 'local' ? 'Локальные позиции; композиции можно брать из разных альбомов.' : 'Выбранное издание → его позиции. Наши файлы находятся отдельно справа.'}`, link('review', 'Все входящие', 'button')) +
    `<div class="inline-summary">${badge(g.mode === 'local' ? 'Сборник без издания провайдера' : `${target.year} · ${target.country} · ${target.catalog}`, 'info')}<span><b>${Object.keys(assignments).length}/${target.tracks.length}</b> назначено</span><span><b>${free.length}</b> файлов свободно</span><span>Только чтение: <code>/mnt/inbox/${g.id}</code></span></div>` +
    `<div class="matching-grid">${panel('Найти издание', candidateList())}<section class="panel target-panel"><div class="panel-heading"><div><h2>${esc(target.title)}</h2><span class="sub">${artistName(target.artistId)} · ${target.tracks.length} позиций · ${g.mode === 'local' ? 'Локальный порядок' : 'Носитель 1'}</span></div>${badge('Черновик назначений')}</div><div class="table-wrap"><table class="mapping-table"><thead><tr><th scope="col">✓</th><th scope="col">№</th><th scope="col">Композиция</th><th scope="col">Длит.</th><th scope="col">Наш файл / перетаскивание</th><th scope="col">Оценка*</th></tr></thead><tbody>${rows}</tbody></table></div><div class="panel-foot">* Утверждённая политика — <code>matching-rapidfuzz-v1</code>, применимость по совокупной уверенности ≥0.70. Этот макет уверенность не рассчитывает; уровни — демонстрационные. Нажмите на уровень, чтобы увидеть демонстрационные различия.</div></section><section class="panel file-pool"><div class="panel-heading"><h2>Наши файлы</h2>${badge(`${free.length} свободно`)}</div><div class="file-list" data-pool="true">${free.length ? free.map(id => fileChip(demoFiles[id])).join('') : '<div class="empty">Все файлы назначены.</div>'}</div><div class="panel-foot">Перетащите на строку слева.<br>Или выберите файл → «Назначить выбранный». Escape снимает выбор.<br><strong>Это не загрузка файлов с компьютера.</strong></div></section></div>` +
    (ui.recordingTarget ? recordingSearch() : '') +
    `<div id="match-inspector">${panel(`Инспектор · ${esc(inspected.title)}`, `<div class="inspector-tabs"><button data-inspector="tags" aria-pressed="${ui.inspector === 'tags'}">Сравнение тегов</button><button data-inspector="evidence" aria-pressed="${ui.inspector === 'evidence'}">Почему такое совпадение</button>${ui.selectedFile && demoFiles[ui.selectedFile] ? `<span class="small">Для назначения выбран: ${esc(demoFiles[ui.selectedFile].name)}</span>` : ''}</div>${inspectorContent}`)}</div>` +
    `<div class="commit-bar"><div><strong>${included} позиций выбрано для публикации</strong><span class="sub">${target.tracks.length - Object.keys(assignments).length} пустых позиций не блокируют публикацию. ${free.length} файлов останутся неразмеченными во входящих.</span></div>${button('preview-group', 'Предпросмотр публикации', included ? '' : 'disabled', true)}</div>`;
}

function recordingSearch() {
  const target = currentTarget().tracks.find(track => track.id === ui.recordingTarget);
  const candidates = ui.recordingResults || [];
  return panel(`Поиск композиции для «${esc(target.title)}»`, `<form id="recording-search" class="search-form inline"><label for="recording-query">Название / артист</label><input id="recording-query" name="query" value="${esc(ui.recordingQuery)}"><button type="submit">Найти композиции</button>${button('close-recording', 'Закрыть')}</form>${table(['Композиция', 'Артист', 'Найдена в релизе', 'Длительность', ''], candidates.map(({ release, track }) => [esc(track.title), artistName(track.artistId), `${esc(release.title)} · ${release.year}`, duration(track.duration), button('bind-recording', 'Связать композицию', `data-recording="${track.id}"`)]))}<div class="panel-foot">Связывается только композиция. Название сборника, его год, порядок и локальные позиции не заменяются чужим альбомом.</div>`);
}

function assignFile(fileId, targetId) {
  const g = group();
  if (!g.fileIds.includes(fileId) || !demoFiles[fileId]?.available || !currentTarget().tracks.some(track => track.id === targetId)) return;
  const assignments = draft();
  const assignment = assignments[targetId];
  if (associatedSourceIds(assignment).includes(fileId)) {
    ui.selectedFile = null;
    ui.notice = `${demoFiles[fileId].name} уже связан с этой позицией; повтор не добавлен.`;
    render(false);
    return;
  }
  assignments[targetId] = assignment || { sourceIds: [], selectedSourceId: null, sourceMeta: {}, method: 'Вручную · перетаскивание или выбор', confidence: null, providerTrackId: g.mode === 'local' ? null : targetId, included: false };
  const current = assignments[targetId];
  if (!current.sourceMeta) current.sourceMeta = Object.fromEntries(associatedSourceIds(current).map(id => [id, { method: current.method, confidence: demoFiles[id]?.confidence || null }]));
  current.sourceMeta[fileId] = { method: 'Вручную · добавлен вариант', confidence: demoFiles[fileId].confidence };
  current.sourceIds = [...associatedSourceIds(current), fileId];
  current.method = assignmentMethod(current);
  current.confidence = assignmentConfidence(current);
  ui.selectedFile = null; ui.selectedTarget = targetId;
  ui.notice = `К позиции добавлен вариант ${demoFiles[fileId].name}. Связь не меняет выбранный источник или публикацию.`;
  render(false);
}

function catalogRows() {
  const query = normalized(ui.libraryQuery);
  const hasAudio = trackHasAudio;
  const availabilityMatches = tracks => ui.availabilityFilter === 'all' || (ui.availabilityFilter === 'available' ? tracks.some(hasAudio) : tracks.some(track => !hasAudio(track)));
  const publicationMatches = track => ui.publicationFilter === 'all' || (ui.publicationFilter === 'published' ? Boolean(track.published) : !track.published);
  const missingGroups = absentProviderGroups.filter(group => {
    const represented = localReleases.some(release => release.releaseGroupId === group.groupId);
    return !represented && ui.availabilityFilter !== 'available' && ui.publicationFilter !== 'published' &&
      normalized(`${group.title} ${artistName(group.artistId)} ${group.editions.map(edition => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(' ')}`).join(' ')}`).includes(query);
  });
  if (ui.libraryView === 'artists') {
    const artists = demoArtists.filter(artist => (normalized(`${artist.name} ${artist.genre}`).includes(query) || missingGroups.some(group => group.artistId === artist.id)) && (
      localReleases.some(release => (release.credits?.some(credit => credit.artistId === artist.id) || release.artistId === artist.id) && (release.tracks.some(publicationMatches) || !release.tracks.length && ui.publicationFilter === 'all') && availabilityMatches(release.tracks)) ||
      missingGroups.some(group => group.artistId === artist.id)
    ));
    return `<div class="artist-grid">${artists.length ? artists.map(artist => {
      const releases = artistReleases(artist.id);
      const tracks = releases.flatMap(release => release.tracks);
      const trackIds = tracks.map(track => track.id);
      const available = tracks.filter(hasAudio).length, published = tracks.filter(track => track.published).length;
      return `<article class="artist-card">${selectionInput('artist', artist.id, `Выбрать все позиции артиста ${artist.name}`, trackIds, true)}<a href="#artist/${esc(artist.id)}">${cover(artist, 'medium')}<span class="artist-card-copy"><strong>${esc(artist.name)}</strong><span class="sub">${russianCount(releases.length, 'издание', 'издания', 'изданий')} · ${russianCount(tracks.length, 'позиция', 'позиции', 'позиций')}${missingGroups.some(group => group.artistId === artist.id) ? ` · ${russianCount(missingGroups.filter(group => group.artistId === artist.id).length, 'альбом без издания', 'альбома без издания', 'альбомов без издания')}` : ''}</span></span><span class="artist-card-status"><span>Аудио <strong>${available} / ${tracks.length}</strong></span><span>Опубликовано <strong>${published} / ${tracks.length}</strong></span></span><span class="artist-card-arrow" aria-hidden="true">›</span></a></article>`;
    }).join('') : '<div class="empty">Артистов не найдено.</div>'}</div>`;
  }
  if (ui.libraryView === 'albums') {
    const releases = localReleases.filter(release => normalized(`${release.title} ${release.credits?.map(credit => credit.credit || credit.name).join(' ') || artistName(release.artistId)} ${release.catalog}`).includes(query) && (release.tracks.some(publicationMatches) || !release.tracks.length && ui.publicationFilter === 'all') && availabilityMatches(release.tracks));
    return table(['✓', 'Альбом', 'Артист', 'Год / издание', 'Позиции', 'Есть исходники', 'Есть аудио', 'Опубликовано', 'Провайдер'], releases.map(release => {
      const published = release.tracks.filter(track => track.published).length;
      const provider = providerReleases.find(item => item.id === release.providerId);
      const existingAudio = release.tracks.filter(hasAudio).length;
      const publicationKnown = Boolean(provider?.tracks.length && provider.tracks.length === release.tracks.length);
      const publicationTone = !publicationKnown ? '' : published === provider.tracks.length ? 'success' : published ? 'warning' : 'error';
      const publicationSummary = publicationCountLabel(published, publicationKnown ? provider.tracks.length : 0, publicationKnown);
      return [selectionInput('release', release.id, `Выбрать все позиции альбома ${release.title}`, release.tracks.map(track => track.id), true), `<div class="entity">${cover(release)}${link(`release/${release.id}`, release.title || 'Без названия')}</div>`, release.credits?.length ? release.credits.map(credit => credit.artistId ? link(`artist/${credit.artistId}`, credit.credit || credit.name) : esc(credit.credit || credit.name || 'Не указан')).join(', ') : artistIdLink(release), `${release.year || 'Дата не указана'} · ${release.country || 'Локальный'}<span class="sub">${esc(release.catalog || '—')}</span>`, release.tracks.length, release.tracks.filter(track => trackSourceIds(track).length).length, `${existingAudio} с аудио${provider?.tracks.length ? ` / ${provider.tracks.length}` : ''}`, badge(publicationSummary, publicationTone), provider ? badge('MusicBrainz', 'success') : badge('Локальный сборник')];
    })) + (missingGroups.length ? missingProviderGroupsPanel(missingGroups) : '');
  }
  const tracks = allTracks().filter(({ release, track }) => normalized(`${track.title} ${artistName(track.artistId)} ${release.title}`).includes(query) && publicationMatches(track) && (ui.availabilityFilter === 'all' || (ui.availabilityFilter === 'available' ? hasAudio(track) : !hasAudio(track))));
  return table(['Выбрать', 'Издание', '№', 'Композиция', 'Источник / аудио', 'Публикация'], tracks.map(({ release, track }) => [`<input type="checkbox" data-select-track="${track.id}" aria-label="Выбрать ${esc(track.title)}" ${selectedTrack(track.id) ? 'checked' : ''}>`, link(`release/${release.id}`, release.title), trackNumberLabel(release, track), link(`track/${track.id}`, track.title), `${track.sourceIds?.length || Number(Boolean(track.sourceId))} исходников · ${hasAudio(track) ? 'есть аудио' : 'нет аудио'}`, track.published ? badge('Опубликовано', 'success') : badge('Не опубликовано')]));
}

function artistIdLink(release) { return release.artistId ? link(`artist/${release.artistId}`, artistName(release.artistId)) : 'Артист не указан'; }

function metadataChanged(track, release) {
  if (!track.published || !track.metadataBaseline) return false;
  const effective = providerTags(release, track);
  const fields = ['TITLE', 'ARTIST', 'ALBUM', 'ALBUMARTIST', 'DATE', 'COUNTRY', 'LABEL', 'CATALOGNUMBER', 'TRACKNUMBER', 'DISCNUMBER', 'GENRE'];
  const tagsChanged = fields.some(field => normalized((effective[field] || []).join('\u0000')) !== normalized((track.metadataBaseline[field] || []).join('\u0000')));
  const baselineExtras = track.metadataBaselineExtras || {};
  return tagsChanged || (track.coverHash || null) !== (baselineExtras.coverHash || null) || (track.lyrics || null) !== (baselineExtras.lyrics || null);
}

function metadataDiffFields(track, release) {
  if (!track.published || !track.metadataBaseline) return [];
  const effective = providerTags(release, track);
  const fields = ['TITLE', 'ARTIST', 'ALBUM', 'ALBUMARTIST', 'DATE', 'COUNTRY', 'LABEL', 'CATALOGNUMBER', 'TRACKNUMBER', 'DISCNUMBER', 'GENRE'];
  const changed = fields.filter(field => normalized((effective[field] || []).join('\u0000')) !== normalized((track.metadataBaseline[field] || []).join('\u0000')));
  if ((track.coverHash || null) !== (track.metadataBaselineExtras?.coverHash || null)) changed.push('Встроенная обложка');
  if ((track.lyrics || null) !== (track.metadataBaselineExtras?.lyrics || null)) changed.push('Текст песни');
  return changed;
}

function sourceRootDemo() {
  return `<details class="source-removal-demo" data-detail-key="source-removal-demo"><summary>Демонстрация удаления источника</summary>${panel('Удаление каталога источника', `<p class="padded">Подтверждение показывает точный путь и число записей инвентаря. Файлы на диске и текущие публикации не затрагиваются. Включите активную операцию, чтобы увидеть отказ.</p><label class="inline-check"><input type="checkbox" id="root-operation-active" ${ui.rootOperationActive ? 'checked' : ''}>Для /mnt/music выполняется операция</label>${table(['Идентификатор', 'Путь на сервере', 'Записей', 'Сценарий', 'Действие'], [
    ['root-inbox', '/mnt/inbox', Object.values(demoFiles).filter(file => file.locations?.some(location => location.rootId === 'root-inbox')).length, 'Общий файл остаётся связанным с резервным каталогом', ui.removedSourceRoots.has('root-inbox') ? badge('Удалён в демо') : button('remove-root', 'Удалить каталог', 'data-root="root-inbox"')],
    ['root-library', '/mnt/music', Object.values(demoFiles).filter(file => file.locations?.some(location => location.rootId === 'root-library')).length, 'Последний FLAC-исходник снимает связь; MP3 не выбирается автоматически', ui.removedSourceRoots.has('root-library') ? badge('Удалён в демо') : button('remove-root', 'Удалить каталог', 'data-root="root-library"')]
  ])}<div class="panel-foot">После удаления последней записи связи позиций снимаются, кэш анализа по SHA-256 сохраняется. Выбранный файл очищается без автопереключения. Публикация и медиатека остаются.</div>`)}</details>`;
}

function missingProviderGroupsPanel(groups) {
  const missingRows = groups.map(group => {
    const editionId = ui.absentEdition[group.id];
    const chosen = group.editions.find(edition => edition.id === editionId);
    return [`<strong>${esc(group.title)}</strong><span class="sub">${artistName(group.artistId)} · альбом, которого нет в медиатеке</span>`, `<select data-absent-edition="${group.id}" aria-label="Выберите издание группы ${esc(group.title)}"><option value="">Выберите конкретное издание</option>${group.editions.map(edition => `<option value="${edition.id}" ${edition.id === editionId ? 'selected' : ''}>${edition.year} · ${edition.country} · ${edition.format} · ${edition.titles.length} поз. · ${edition.disambiguation}</option>`).join('')}</select>`, chosen ? `${chosen.year} · ${chosen.country} · ${chosen.format} · ${chosen.disambiguation}` : '—', chosen ? table(['№', 'Композиция'], chosen.titles.map((title, index) => [index + 1, esc(title)])) : 'Выберите конкретное издание; выбор не создаёт запись в медиатеке.'];
  });
  return panel('Отсутствующие альбомы', '<p class="padded">Известная часть каталога провайдера. Выберите издание, чтобы посмотреть его треки. Просмотр не добавляет музыку в медиатеку и не скачивает файлы.</p>' + table(['Альбом', 'Выбор издания', 'Детали издания', 'Треки'], missingRows));
}

function library() {
  const all = allTracks();
  const chosenCount = selectedTrackIds().length;
  return header('Медиатека', `${russianCount(demoArtists.length, 'артист', 'артиста', 'артистов')} · ${russianCount(localReleases.length, 'издание', 'издания', 'изданий')} · ${russianCount(all.length, 'позиция', 'позиции', 'позиций')} · ${all.filter(({ track }) => track.published).length} опубликовано`, `${link('create-release', 'Создать вручную', 'button')}${link('review', 'Разобрать входящие', 'button')}`) +
    `<div class="catalog-toolbar"><div class="segments" aria-label="Вид медиатеки">${[['artists', 'Артисты'], ['albums', 'Все альбомы'], ['tracks', 'Все треки']].map(([id, title]) => `<button data-library-view="${id}" aria-pressed="${ui.libraryView === id}">${title}</button>`).join('')}</div><label class="search-label"><span class="sr-only">Поиск в медиатеке</span><input type="search" id="library-search" value="${esc(ui.libraryQuery)}" placeholder="Артист, альбом, трек…"></label><label class="inline-field">Наличие<select id="availability-filter"><option value="all">Все</option><option value="available" ${ui.availabilityFilter === 'available' ? 'selected' : ''}>В наличии</option><option value="missing" ${ui.availabilityFilter === 'missing' ? 'selected' : ''}>Не хватает</option></select></label><label class="inline-field">Публикация<select id="publication-filter"><option value="all">Все</option><option value="published" ${ui.publicationFilter === 'published' ? 'selected' : ''}>Опубликованные</option><option value="unpublished" ${ui.publicationFilter === 'unpublished' ? 'selected' : ''}>Не опубликованные</option></select></label></div>${chosenCount ? `<div class="bulk-toolbar" role="region" aria-label="Действия с выбранным">${button('preview-bulk', `Предпросмотр ${russianCount(chosenCount, 'позиции', 'позиции', 'позиций')}`, '', true)}<span class="sub">Выбор артиста или альбома отмечает все его существующие позиции. Снимите отметку у отдельной позиции, чтобы исключить её; повторяющиеся позиции учитываются один раз.</span><button type="button" data-action="clear-library-selection">Снять весь выбор</button></div>` : ''}<div id="catalog-content">${catalogRows()}</div>`;
}

function trackNumberLabel(release, track) {
  if (track.displayedNumber) return track.displayedNumber;
  const multiMedium = (release.media?.length || 0) > 1 || new Set(release.tracks.map(item => item.medium).filter(Boolean)).size > 1;
  if (multiMedium && track.mediumPosition) return `${track.medium}.${String(track.mediumPosition).padStart(2, '0')}`;
  return String(track.mediumPosition || track.position).padStart(2, '0');
}

function trackTable(items, withAlbum = false) {
  const withMedium = items.some(({ release }) => (release.media?.length || 1) > 1);
  const heads = [...(withMedium ? ['Носитель'] : []), '№', 'Композиция', ...(withAlbum ? ['Альбом / артист'] : []), 'Длит.', 'Источник', 'Параметры', 'Файл', 'Публикация', ''];
  return table(heads, items.map(({ release, track }) => {
    const file = demoFiles[track.sourceId];
    const sourceNames = (track.sourceIds || (track.sourceId ? [track.sourceId] : [])).map(id => demoFiles[id]?.name).filter(Boolean).join(', ');
    const recordingLabel = track.recordingTitle ? ` · ${esc(track.recordingTitle)}${track.localRecordingId ? ' · новая композиция (демо)' : ''}` : '';
    return [...(withMedium ? [esc(release.media?.[(track.medium || 1) - 1]?.title || track.medium || 1)] : []), trackNumberLabel(release, track), `${link(`track/${track.id}`, track.title)}<span class="sub">${artistName(track.artistId)}${recordingLabel}</span>`, ...(withAlbum ? [`${link(`release/${release.id}`, release.title)}<span class="sub">${artistName(release.artistId)} · ${release.year}</span>`] : []), `<span class="mono">${duration(track.duration)}</span>`, file ? badge(file.codec, file.codec === 'FLAC' ? 'success' : '') : badge(sourceNames ? 'Источник не выбран' : 'Нет файла', 'warning'), file ? `<span class="mono">${file.codec === 'FLAC' ? '24 / 48 кГц' : '320 кбит/с'}</span>` : '—', `<span class="filename">${esc(sourceNames || file?.name || '—')}</span>`, track.published ? badge('Опубликовано', 'success') : badge('Не опубликовано'), link(`track/${track.id}`, 'Теги')];
  }));
}

const artistGenreDefaults = { mira: ['demo-genre-ambient', 'demo-genre-folk'], arden: ['demo-genre-contemporary-jazz'], forma: ['demo-genre-electronic'] };
const artistGenreIds = artist => artist.genres || artistGenreDefaults[artist.id] || [];
const artistGenreNames = artist => {
  const names = artistGenreIds(artist).map(id => window.GenreLibraryPrototype?.get(id)?.name).filter(Boolean);
  return names.length ? names.join(', ') : artist.genre || '—';
};

function artistMetadataPanel(artist) {
  const identities = Array.isArray(artist.providerIdentities) ? artist.providerIdentities : [];
  return `<section class="artist-metadata panel" data-artist-metadata-panel><div class="panel-heading"><div><h2>Сведения об артисте</h2><p class="muted">Только поля артиста в этом демо. Публикации и теги композиций не изменяются.</p></div><div class="artist-metadata-actions" aria-label="Действия с метаданными артиста"><button type="button" data-artist-metadata="main" data-mode="edit">Редактировать</button><button type="button" data-artist-metadata="cancel" aria-hidden="true" tabindex="-1">Отменить</button></div></div><form class="artist-metadata-form" data-artist-form><div class="artist-metadata-fields"><label class="artist-metadata-field"><span>Имя</span><input name="name" value="${esc(artist.name || '')}" readonly></label><label class="artist-metadata-field"><span>Имя для сортировки</span><input name="sort_name" value="${esc(artist.sort_name || '')}" readonly></label><label class="artist-metadata-field"><span>Уточнение</span><input name="disambiguation" value="${esc(artist.disambiguation || '')}" readonly></label><div class="artist-metadata-field artist-genre-field"><span>Жанры</span><button type="button" class="artist-genre-trigger" data-artist-genres aria-label="Жанры" aria-haspopup="dialog" aria-expanded="false" disabled>${esc(artistGenreNames(artist))}</button><dialog class="artist-genre-dialog" data-artist-genre-dialog aria-labelledby="artist-genre-title"><div class="artist-genre-dialog-heading"><h3 id="artist-genre-title">Жанры артиста</h3><button type="button" data-artist-genre-close aria-label="Закрыть">Закрыть</button></div><div data-artist-genre-picker></div></dialog></div><label class="artist-metadata-field"><span>Теги артиста</span><input name="tags" value="${esc(artist.tags || '')}" readonly></label><label class="artist-metadata-field"><span>MusicBrainz ID · только чтение</span><input value="${esc(artist.mbid || 'Не связан')}" readonly></label><label class="artist-metadata-field artist-provider-identities"><span>Идентификаторы провайдеров · только чтение</span><input value="${esc(identities.length ? identities.map(identity => `${identity.provider}: ${identity.id}`).join(' · ') : 'Не указаны в демо')}" readonly></label></div><p class="artist-metadata-hint">Идентификаторы провайдеров доступны только для чтения. Выбор или создание связи с MusicBrainz здесь не выполняется.</p></form></section>`;
}

function setArtistMetadataMode(panel, editing) {
  panel.classList.toggle('editing', editing);
  panel.querySelectorAll('[data-artist-form] input[name]').forEach(input => { input.readOnly = !editing; });
  const main = panel.querySelector('[data-artist-metadata="main"]');
  main.textContent = editing ? 'Сохранить' : 'Редактировать';
  main.dataset.mode = editing ? 'save' : 'edit';
  const cancel = panel.querySelector('[data-artist-metadata="cancel"]');
  cancel.setAttribute('aria-hidden', String(!editing));
  cancel.tabIndex = editing ? 0 : -1;
  panel.querySelector('[data-artist-genres]').disabled = !editing;
}

function resetArtistMetadataForm(panel, artist) {
  const form = panel.querySelector('[data-artist-form]');
  form.querySelector('[name="name"]').value = artist.name || '';
  form.querySelector('[name="sort_name"]').value = artist.sort_name || '';
  form.querySelector('[name="disambiguation"]').value = artist.disambiguation || '';
  form.querySelector('[name="tags"]').value = artist.tags || '';
  form.dataset.genresDirty = 'false';
  panel.querySelector('[data-artist-genres]').textContent = artistGenreNames(artist);
}

function artistPage(id) {
  const artist = demoArtist(id);
  if (!artist) return header('Артист не найден', 'Эта ссылка не соответствует артисту медиатеки.', link('library', 'Вся медиатека', 'button'));
  const releases = artistReleases(artist.id);
  const tracks = releases.flatMap(release => release.tracks);
  const trackIds = tracks.map(track => track.id);
  const chosenCount = selectedTrackIds().length;
  for (const release of releases) for (const track of release.tracks) track.metadataChanged = metadataChanged(track, release);
  const providerComplete = release => { const provider = providerReleases.find(item => item.id === release.providerId); return Boolean(provider?.tracks.length && provider.tracks.length === release.tracks.length); };
  const full = releases.filter(release => providerComplete(release) && release.tracks.length && release.tracks.every(trackHasAudio)).length;
  const partial = releases.filter(release => providerComplete(release) && release.tracks.some(trackHasAudio) && release.tracks.some(track => !trackHasAudio(track))).length;
  const audioNone = releases.filter(release => providerComplete(release) && release.tracks.length && release.tracks.every(track => !trackHasAudio(track))).length;
  const pubFull = releases.filter(release => providerComplete(release) && release.tracks.length && release.tracks.every(track => track.published)).length;
  const pubPartial = releases.filter(release => providerComplete(release) && release.tracks.some(track => track.published) && release.tracks.some(track => !track.published)).length;
  const pubNone = releases.filter(release => providerComplete(release) && release.tracks.length && release.tracks.every(track => !track.published)).length;
  const pubUnknown = releases.filter(release => !providerComplete(release)).length;
  const query = normalized(ui.libraryQuery);
  const missingGroups = absentProviderGroups.filter(group => group.artistId === artist.id && !localReleases.some(release => release.releaseGroupId === group.groupId) && ui.availabilityFilter !== 'available' && normalized(`${group.title} ${group.editions.map(edition => `${edition.year} ${edition.country} ${edition.format} ${edition.disambiguation} ${edition.titles.join(' ')}`).join(' ')}`).includes(query));
  return `<section class="artist-overview"><div><h1 tabindex="-1" data-artist-heading>${esc(artist.name)}</h1><p class="muted">${russianCount(releases.length, 'издание', 'издания', 'изданий')} · ${russianCount(tracks.length, 'позиция', 'позиции', 'позиций')} · ${russianCount(missingGroups.length, 'альбом без издания', 'альбома без издания', 'альбомов без издания')}</p><div class="artist-metrics"><span>Исходники <strong>${tracks.filter(track => trackSourceIds(track).length).length}</strong></span><span>Есть аудио <strong>${tracks.filter(trackHasAudio).length}</strong></span><span>Опубликовано <strong>${tracks.filter(track => track.published).length}</strong></span><span>Аудио по изданиям <strong>полностью: ${full} · частично: ${partial} · нет: ${audioNone} · неизвестно: ${releases.filter(release => !providerComplete(release)).length}</strong></span><span>Публикация по изданиям <strong>полностью: ${pubFull} · частично: ${pubPartial} · нет: ${pubNone} · неизвестно: ${pubUnknown}</strong></span></div></div><div class="actions">${selectionInput('artist', artist.id, `Выбрать все позиции артиста ${artist.name}`, trackIds)}${button('delete-library', 'Удалить артиста', `data-kind="artist" data-id="${esc(artist.id)}"`)}${link('library', 'К медиатеке', 'button')}</div></section>${artistMetadataPanel(artist)}${chosenCount ? `<div class="bulk-toolbar" role="region" aria-label="Действия с выбранным">${button('preview-bulk', `Предпросмотр ${russianCount(chosenCount, 'позиции', 'позиции', 'позиций')}`, '', true)}<span class="sub">Выбраны только существующие позиции. Снимите отдельную отметку у позиции, чтобы исключить её.</span><button type="button" data-action="clear-library-selection">Снять весь выбор</button></div>` : ''}` +
    releases.map((release, index) => { const pub = release.tracks.filter(track => track.published).length; const provider = providerReleases.find(item => item.id === release.providerId); const known = Boolean(providerComplete(release) && provider?.tracks.length); const tone = !known ? '' : pub === provider.tracks.length ? 'success' : pub ? 'warning' : 'error'; const summary = publicationCountLabel(pub, known ? provider.tracks.length : 0, known); const releaseTrackIds = release.tracks.map(track => track.id); const changedTracks = release.tracks.filter(track => track.metadataChanged).length; return `<details class="album-disclosure" data-detail-key="artist-album:${esc(release.id)}" ${index === 0 ? 'open' : ''}><summary><span class="entity">${cover(release)}<span><strong>${esc(release.title)}</strong><span class="sub">${release.year} · ${russianCount(release.tracks.length, 'позиция', 'позиции', 'позиций')} · ${release.label || 'Локальный'} · ${release.catalog}</span></span></span><span class="edition-status ${tone}">${badge(summary, tone)}${changedTracks ? badge(`Метаданные изменились у ${russianCount(changedTracks, 'позиции', 'позиций', 'позиций')}`, 'warning') : ''}</span></summary><div class="album-actions">${selectionInput('release', release.id, `Выбрать все позиции альбома ${release.title}`, releaseTrackIds)}${link(`release/${release.id}`, 'Открыть альбом', 'button')}${button('preview-release', 'Предпросмотр публикации', `data-release="${release.id}"`, true)}</div>${release.tracks.length ? table(['Выбрать', '№', 'Композиция', 'Есть аудио', 'Связей с исходниками', 'Публикация'], release.tracks.map(track => [`<input type="checkbox" data-select-track="${track.id}" aria-label="Выбрать ${esc(track.title)}" ${selectedTrack(track.id) ? 'checked' : ''}>`, trackNumberLabel(release, track), `${link(`track/${track.id}`, track.title)}${availabilityRowHint(track)}`, track.sourceId || track.published ? 'Да' : 'Нет', track.sourceIds?.length || Number(Boolean(track.sourceId)), track.published ? badge('Опубликовано', 'success') : badge('Не опубликовано')]), '', release.tracks.map(track => availabilityRowClass(track))) : '<div class="empty">Позиции не добавлены.</div>'}</details>`; }).join('') + (missingGroups.length ? missingProviderGroupsPanel(missingGroups) : '');
}

function releasePage(id) {
  const release = localRelease(id);
  if (!release) return header('Издание не найдено в медиатеке', 'Это издание есть только в каталоге провайдера и пока отсутствует в медиатеке. Выберите конкретное издание в каталоге.', link('library', 'К медиатеке', 'button'));
  const tracks = release.tracks;
  const chosenCount = selectedTrackIds().length;
  const sourceCount = tracks.reduce((count, track) => count + (track.sourceIds?.length ?? Number(Boolean(track.sourceId))), 0);
  const pubCount = tracks.filter(track => track.published).length;
  for (const track of tracks) track.metadataChanged = metadataChanged(track, release);
  const manualLink = release.manual ? link('create-release', 'Вернуться к черновику', 'button') : '';
  const provider = providerReleases.find(item => item.id === release.providerId);
  const publicationKnown = Boolean(provider?.tracks.length && provider.tracks.length === tracks.length);
  const publicationSummary = publicationCountLabel(pubCount, publicationKnown ? provider.tracks.length : 0, publicationKnown);
  const changedCount = tracks.filter(track => track.metadataChanged).length;
  const creditLinks = release.credits?.length ? release.credits.map(credit => credit.artistId ? link(`artist/${credit.artistId}`, credit.credit || credit.name) : esc(credit.credit || credit.name || 'Не указан')).join(', ') : artistIdLink(release);
  const totalDuration = tracks.every(track => Number.isFinite(track.duration)) ? tracks.reduce((sum, track) => sum + track.duration, 0) : null;
  return `<div class="release-header">${cover(release, 'large')}<div class="release-heading-copy"><h1 tabindex="-1">${esc(release.title || 'Локальный релиз без названия')}</h1><p>${creditLinks} · ${release.year || 'Дата не указана'} · ${release.country || 'Страна не указана'} · ${esc(release.label || 'Локальный')} · ${esc(release.catalog || '—')}</p><div class="inline-summary">${badge(release.providerId ? `MusicBrainz · ${release.providerId}` : 'Локальный релиз', release.providerId ? 'success' : '')}<span>${russianCount(tracks.length, 'позиция', 'позиции', 'позиций')}</span><span>${russianCount(sourceCount, 'связь с исходниками', 'связи с исходниками', 'связей с исходниками')}</span><span>${publicationSummary}</span>${changedCount ? badge(`Метаданные изменились у ${changedCount} позиций`, 'warning') : ''}</div></div><div class="actions release-actions">${tracks.length ? button('preview-release', 'Предпросмотр публикации', `data-release="${release.id}"`, true) : ''}${selectionInput('release', release.id, `Выбрать все позиции альбома ${release.title}`, tracks.map(track => track.id))}<details class="secondary-actions" data-detail-key="release-actions:${esc(release.id)}"><summary>Другие действия</summary><div class="actions">${manualLink}${tracks.length ? link(`tags/${tracks[0].id}/release`, 'Сравнить метаданные', 'button') : ''}${button('delete-library', 'Удалить альбом', `data-kind="release" data-id="${esc(release.id)}"`)}</div></details></div></div>${chosenCount ? `<div class="bulk-toolbar" role="region" aria-label="Действия с выбранным">${button('preview-bulk', `Предпросмотр ${russianCount(chosenCount, 'позиции', 'позиции', 'позиций')}`, '', true)}<span class="sub">Выбраны существующие позиции. Снимите отметку у любой строки, чтобы исключить её из массового предпросмотра.</span><button type="button" data-action="clear-library-selection">Снять весь выбор</button></div>` : ''}` +
    panel(`Список треков · ${release.manual ? `${release.media.length} носителя` : 'конкретное издание'}`, tracks.length ? table(['Выбрать', '№', 'Композиция', 'Длительность', 'Связей с исходниками', 'Публикация'], tracks.map(track => [`<input type="checkbox" data-select-track="${track.id}" aria-label="Выбрать ${esc(track.title)}" ${selectedTrack(track.id) ? 'checked' : ''}>`, trackNumberLabel(release, track), `${link(`track/${track.id}`, track.title)}${availabilityRowHint(track)}`, duration(track.duration), russianCount(track.sourceIds?.length || Number(Boolean(track.sourceId)), 'связь', 'связи', 'связей'), track.published ? `${badge(`${track.published.container} · ${track.published.codec}`, 'success')}${track.metadataChanged ? badge('Метаданные изменились', 'warning') : ''}${track.observedFile ? badge('Файл изменён вне приложения', 'warning') : ''}<details class="track-publication-details" data-detail-key="track-publication:${esc(track.id)}"><summary>Путь и снимок публикации</summary><span class="sub mono">${esc(track.published.path)}</span><span class="sub">${showTag(track.published.tags.TITLE)} · ${esc(track.published.readAt)}</span></details>` : badge('Не опубликовано')]), '', tracks.map(track => availabilityRowClass(track))) : '<div class="empty">Позиции не добавлены.</div>', tracks.length && totalDuration !== null ? badge(`${duration(totalDuration)} всего`) : '') +
    (tracks.length ? `<details class="panel metadata-comparison" data-detail-key="metadata-comparison:${esc(release.id)}"><summary><strong>Сравнение метаданных</strong></summary><div class="panel-foot">${release.manual ? `Тип: ${esc(release.metadata.type || '—')} · Исполнители: ${release.credits.map(item => esc(item.credit || item.name)).filter(Boolean).join(', ') || 'не указаны'}<br>` : ''}${link(`tags/${tracks[0].id}/release`, 'Открыть подробное сравнение альбома')} · Для опубликованных тегов выбран конкретный файл.</div>${tagMatrix(release, tracks[0], demoFiles[tracks[0].sourceId], findProviderTrack(tracks[0].providerTrackId), tracks[0].published)}</details>` : '');
}

function manualReleasePage() {
  return `<div class="proposal-layout manual-release"><div class="page-header"><div><h1 tabindex="-1">Создать локальный релиз</h1><p class="muted">Интерактивный прототип на демонстрационных данных. Изменения остаются в памяти этой вкладки.</p></div></div>
    <div class="state-note"><strong>Только демо.</strong> Создание добавляет релиз в текущую медиатеку в памяти вкладки. Никаких запросов, сохранения на сервере или публикации нет.</div>
    <div id="editor-view">
      <section class="panel" aria-labelledby="release-heading"><div class="panel-heading"><h2 id="release-heading">Релиз</h2><span class="small muted">Поля необязательны; неполный релиз допустим</span></div><div class="release-fields">
        <div class="field"><label for="release-title">Название релиза</label><input id="release-title" autocomplete="off"></div><div class="field"><label for="release-date">Дата релиза</label><input id="release-date" placeholder="Необязательно"></div><div class="field"><label for="release-country">Страна</label><input id="release-country" autocomplete="off"></div><div class="field"><label for="release-label">Лейбл</label><input id="release-label" autocomplete="off"></div><div class="field"><label for="release-catalog">Номер каталога</label><input id="release-catalog" autocomplete="off"></div><div class="field"><label for="release-type">Тип / уточнение</label><input id="release-type" autocomplete="off"></div>
      </div></section>
      <section class="panel"><div class="panel-heading"><div><h2>Артисты релиза</h2><span class="sub">Выберите существующего артиста или укажите новое имя; авторство можно уточнить.</span></div><button type="button" data-action="add-artist">Добавить авторство</button></div><div id="artists-list" class="repeat-list"></div></section>
      <section class="panel"><div class="panel-heading"><div><h2>Носители и позиции</h2><span class="sub">Порядок носителей и позиций изменяется независимо.</span></div><button type="button" data-action="add-medium">Добавить носитель</button></div><div id="media-list" class="repeat-list"></div></section>
      <section class="panel"><div class="panel-heading"><div><h2>Источники и обложка</h2><span class="sub">Демонстрационные исходники; один вариант можно связать с несколькими позициями.</span></div></div><div class="explain">Назначение исходника не публикует позицию. Можно связать несколько вариантов; замена набора подтверждается отдельно.</div><div id="assets-status" class="state-note" aria-live="polite"></div><div class="panel-heading"><h3>Обложка (необязательно)</h3><button type="button" data-action="clear-cover">Снять выбор</button></div><div id="covers-list" class="cover-grid"></div></section>
      <section class="panel"><div class="actions-footer"><button type="button" data-action="reset-demo">Восстановить демо-черновик</button><button type="button" class="primary" data-action="create-release">Создать демо-релиз</button></div></section>
      <p class="small muted">«Доступно» означает только опубликованные позиции. У локального релиза нет общего числа позиций провайдера; знаменатель не показывается.</p>
    </div></div>`;
}

function addManualRelease(draft) {
  ui.manualDraft = structuredClone(draft);
  const id = `manual-${++ui.manualReleaseCount}`;
  const credits = draft.artists.map((credit, index) => {
    const name = credit.name;
    if (!name) return { ...credit, artistId: null };
    const existing = demoArtists.find(artist => normalized(artist.name) === normalized(name));
    const artistId = credit.artistId || existing?.id || `${id}-artist-${index + 1}`;
    if (!existing && !credit.artistId) demoArtists.push({ id: artistId, name, genre: 'Демо', color: 'slate' });
    return { ...credit, artistId };
  });
  const primaryCredit = credits.find(credit => credit.artistId);
  const artistId = primaryCredit?.artistId || null;
  const cover = window.manualReleaseDemoData.covers.find(item => item.id === draft.coverId);
  let positionIndex = 0;
  const tracks = draft.media.flatMap((medium, mediumIndex) => medium.positions.map((position, mediumPositionIndex) => {
    const durationMatch = /^(\d+):(\d{1,2})$/.exec(position.duration || '');
    const durationSeconds = durationMatch && Number(durationMatch[2]) < 60 ? Number(durationMatch[1]) * 60 + Number(durationMatch[2]) : null;
    positionIndex += 1;
    return {
      id: `${id}-m${mediumIndex + 1}-p${mediumPositionIndex + 1}`, position: positionIndex, mediumPosition: mediumPositionIndex + 1, displayedNumber: position.number || '', medium: mediumIndex + 1,
      title: position.title || '', duration: durationSeconds,
      artistId, recordingId: position.recordingMode === 'existing' ? position.recordingId || null : null,
      localRecordingId: position.recordingMode === 'existing' ? null : `${id}-recording-${mediumIndex + 1}-${mediumPositionIndex + 1}`,
      recordingTitle: position.recordingTitle || '', sourceIds: [...position.sourceIds], sourceId: position.selectedSourceId || null,
      providerTrackId: null, published: null
    };
  }));
  const release = {
    id, title: draft.title || '', artistId, year: draft.metadata.date || '', country: draft.metadata.country || '', label: draft.metadata.label || '', catalog: draft.metadata.catalogNumber || '',
    color: cover?.style === 'blue' ? 'slate' : cover?.style === 'gold' ? 'sand' : 'clay', providerId: null, manual: true,
    metadata: structuredClone(draft.metadata), credits, media: structuredClone(draft.media), coverId: draft.coverId || '', tracks
  };
  localReleases.push(release);
  location.hash = `release/${id}`;
}

function tagsPage(trackId, scope) {
  const context = findLocalTrack(trackId) || allTracks()[0];
  if (tagFields[scope]) ui.tagScope = scope;
  const { release, track } = context;
  const provider = findProviderTrack(track.providerTrackId);
  return header(`Теги: ${track.title}`, 'Артист и альбом в опубликованной колонке — теги выбранного аудиофайла. Несовпадения не скрываются.', link(`release/${release.id}`, 'К альбому', 'button')) +
    panel('Контекст сравнения', `<div class="context-picker"><label for="tag-file">Файл / позиция</label><select id="tag-file">${release.tracks.map(item => `<option value="${item.id}" ${item.id === track.id ? 'selected' : ''}>${String(item.position).padStart(2, '0')} · ${esc(item.title)} · ${item.published ? 'опубликован' : 'без публикации'}</option>`).join('')}</select><span class="small muted">Кэш провайдера: 26.09.2026 10:20 · идентификаторы демонстрационные</span></div>${tagMatrix(release, track, demoFiles[track.sourceId], provider, track.published)}`) +
    note('В колонке публикации показан отдельный снимок чтения существующего файла. Изменение локальных тегов не изменяет эту колонку до явной публикации.');
}

function trackPage(id) {
  const { release, track } = findLocalTrack(id) || allTracks()[0];
  const file = demoFiles[track.sourceId];
  const sourceIds = track.sourceIds || (track.sourceId ? [track.sourceId] : []);
  const sourceList = sourceIds.length ? `<div class="track-source-list">${sourceIds.map(sourceId => {
    const source = demoFiles[sourceId];
    if (!source) return '';
    return `<label class="track-source-choice"><input type="radio" name="track-source-${esc(track.id)}" data-track-source="${esc(track.id)}" value="${esc(source.id)}" ${track.sourceId === source.id ? 'checked' : ''}><span><strong>${esc(source.name)}</strong><span class="sub">${esc(source.codec)} · ${duration(source.duration)} · ${esc(source.size)}</span></span></label>`;
  }).join('')}</div>` : '<div class="empty">Связанных вариантов нет.</div>';
  track.metadataChanged = metadataChanged(track, release);
  const providerTrack = findProviderTrack(track.providerTrackId)?.track;
  const compositionName = track.recordingTitle || providerTrack?.title || (track.recordingId ? 'Связанная композиция' : 'Без связи с провайдером');
  const stateFacts = facts([
    ['Формат исходника', file ? `${file.codec} · ${duration(file.duration)} · ${file.size}` : 'Не выбран'],
    ['Композиция', esc(compositionName)],
    ['Текущая публикация', track.published ? `${track.published.container} · ${track.published.codec} · ${esc(track.published.readAt)}` : 'Не опубликована'],
    ['Метаданные', track.metadataChanged ? `${badge('Изменились', 'warning')} · ${metadataDiffFields(track, release).join(', ')}` : 'Совпадают со снимком публикации'],
    ['Файл на диске', track.observedFile ? badge('Файл изменён вне приложения', 'warning') : 'Изменения не отмечены']
  ]);
  const technicalDetails = `<details class="track-publication-details" data-detail-key="track-technical:${esc(track.id)}"><summary>Пути и технические сведения</summary>${facts([
    ['Путь исходника', `<span class="mono">${esc(file?.path || 'Не выбран')}</span>`],
    ['Идентификатор композиции', esc(track.recordingId || 'Нет связи')],
    ['Подсказка провайдера', esc(track.providerTitle || providerTrack?.title || 'Нет')],
    ['Путь и снимок публикации', track.published ? `<span class="mono">${esc(track.published.path)}</span><br>${showTag(track.published.tags.TITLE)} · ${esc(track.published.readAt)}` : 'Публикации нет'],
    ['Наблюдение за файлом', track.observedFile ? esc(track.observedFile.mtime || 'Файл изменён вне приложения') : 'Изменения не отмечены']
  ])}</details>`;
  const metadataAction = track.published
    ? button('preview-track-metadata', 'Обновить метаданные', `data-track="${track.id}"`, track.metadataChanged)
    : '';
  const previewAction = button('preview-track', 'Предпросмотр публикации', `data-track="${track.id}"`, !track.metadataChanged || !track.published);
  const publicationActions = `<section class="track-publication-actions" aria-labelledby="track-publication-actions-title"><h3 id="track-publication-actions-title">Публикация</h3><div class="actions">${track.published && track.metadataChanged ? metadataAction + previewAction : previewAction + metadataAction}</div></section>`;
  const otherActions = `<details class="secondary-actions" data-detail-key="track-actions:${esc(track.id)}"><summary>Другие действия</summary><div class="actions">${button('edit-title', 'Изменить локальное название', `data-track="${track.id}"`)}${button('refresh-provider-track', 'Обновить кэш провайдера', `data-track="${track.id}"`)}${track.published?.codec === 'MP3' && file?.codec === 'FLAC' ? button('quality-upgrade-demo', 'Демо: улучшить только качество аудио', `data-track="${track.id}"`) : ''}${track.published ? button('explicit-republish', 'Перепубликовать явно', `data-track="${track.id}"`) : ''}${track.published ? button('delete-library', 'Удалить публикацию', `data-kind="publication" data-id="${esc(track.id)}"`) : ''}</div></details>`;
  return header(`${String(track.position).padStart(2, '0')} · ${track.title}`, `${artistName(track.artistId)} / ${release.title} · позиция конкретного издания`, link(`release/${release.id}`, 'К списку треков', 'button')) +
    `<div class="two-columns">${panel('Связанные аудиоварианты', `${sourceList}<div class="panel-foot">Переключатель явно выбирает один исходник для публикации. Текущая публикация не меняется автоматически.</div>`)}${panel('Состояние позиции', `${stateFacts}${technicalDetails}<div class="padded"><p>Локальные изменения сравниваются с последним успешным состоянием публикации (теги, встроенная обложка, текст песни); версия кэша и наблюдение за файлом не подменяют снимок публикации.</p>${publicationActions}${otherActions}</div>`)}</div>` + panel('Сравнение тегов', tagMatrix(release, track, file, findProviderTrack(track.providerTrackId), track.published));
}

function makeGroupPlan(g = group()) {
  const target = currentTarget(g);
  return { mode: g.mode, groupId: g.id, target: structuredClone(target), localReleaseId: targetLocalRelease(g, target)?.id || null, assignments: structuredClone(draft(g)), inventoryVersion: ui.inventoryVersion };
}

function makeReleasePlan(release, trackId = null) {
  return { mode: 'local', groupId: null, target: structuredClone(release), localReleaseId: release.id, assignments: Object.fromEntries(release.tracks.filter(track => track.sourceId && demoFiles[track.sourceId]?.available && demoFiles[track.sourceId]?.locations?.length).map(track => [track.id, { sourceIds: [...(track.sourceIds || [track.sourceId])].filter(id => demoFiles[id]?.available && demoFiles[id]?.locations?.length), selectedSourceId: track.sourceId, providerTrackId: track.providerTrackId, method: 'Текущий выбранный источник', included: !trackId || track.id === trackId }])), inventoryVersion: ui.inventoryVersion };
}

function planRows(plan) {
  const existing = localRelease(plan.localReleaseId);
  return plan.target.tracks.map(targetTrack => {
    const assignment = plan.assignments[targetTrack.id];
    const track = existing?.tracks.find(item => item.position === targetTrack.position);
    const candidateFile = assignment && demoFiles[selectedSourceId(assignment)];
    const file = candidateFile?.available && candidateFile?.locations?.length ? candidateFile : null;
    const provider = assignment?.providerTrackId && findProviderTrack(assignment.providerTrackId);
    const nextTrack = plan.mode === 'local' ? { ...(track || targetTrack), recordingId: provider?.track.recordingId || track?.recordingId } : { ...targetTrack, sourceId: file?.id };
    const nextTags = tagsForLocal(plan.mode === 'local' ? existing || plan.target : plan.target, nextTrack);
    const previous = track?.published;
    const unchanged = previous && file && previous.sourceId === file.id && previous.codec === file.codec && JSON.stringify(previous.tags) === JSON.stringify(nextTags);
    return { targetTrack, assignment, track, file, provider, nextTags, previous, action: !assignment?.included || !file ? 'Пропустить' : unchanged ? 'Без изменений' : previous ? 'Заменить' : 'Создать', downgrade: Boolean(previous?.codec === 'FLAC' && file?.codec === 'MP3' && assignment?.included) };
  });
}

function publicationDestination(release, track) {
  return {
    container: 'MKA',
    path: track.published?.path || `/srv/music/${artistName(release.artistId)}/${release.year} - ${release.title}/${String(track.position).padStart(2, '0')} - ${track.title}.mka`
  };
}

const trackSourceIds = track => [...new Set([...(track?.sourceIds || []), ...(track?.sourceId ? [track.sourceId] : [])])];
const trackHasAudio = track => trackSourceIds(track).some(id => demoFiles[id]?.available && demoFiles[id]?.locations?.length) || Boolean(track?.published);
const availabilityMatchesTrack = track => ui.availabilityFilter === 'all' || (ui.availabilityFilter === 'available' ? trackHasAudio(track) : !trackHasAudio(track));
const availabilityRowClass = track => ui.availabilityFilter === 'all' ? '' : availabilityMatchesTrack(track) ? 'filter-match' : 'filter-muted';
const availabilityRowHint = track => ui.availabilityFilter === 'all' ? '' : `<span class="sub">${availabilityMatchesTrack(track) ? 'По фильтру' : 'Вне фильтра'}</span>`;

function pendingSourceAssociations(plan, rows = planRows(plan)) {
  const existing = localRelease(plan.localReleaseId);
  return rows.filter(row => {
    const assigned = associatedSourceIds(row.assignment).filter(id => demoFiles[id]?.available && demoFiles[id]?.locations?.length);
    if (!assigned.length) return false;
    const track = existing?.tracks.find(item => item.position === row.targetTrack.position);
    const persisted = new Set(trackSourceIds(track));
    return assigned.some(id => !persisted.has(id));
  });
}

function publications() {
  if (!ui.plan) ui.plan = makeGroupPlan();
  const plan = ui.plan;
  const rows = planRows(plan);
  const active = rows.filter(row => row.assignment?.included && row.file);
  const changed = active.filter(row => row.action !== 'Без изменений');
  const linkChanges = pendingSourceAssociations(plan, rows).length;
  const g = plan.groupId && incomingGroups.find(item => item.id === plan.groupId);
  const publishedFileIds = new Set(active.map(row => row.file.id));
  const kept = g ? [...new Set(g.fileIds)].filter(id => !publishedFileIds.has(id)).length : 0;
  const stale = plan.inventoryVersion !== ui.inventoryVersion;
  const canConfirm = !stale && (changed.length > 0 || linkChanges > 0);
  return header(`Предпросмотр: ${plan.target.title}`, 'Контекстный предпросмотр операции. Это не отдельный список публикаций.', `${link(plan.groupId ? `match/${plan.groupId}` : `release/${plan.localReleaseId}`, 'Вернуться к объекту', 'button')}`) +
    (stale ? note('Инвентарь источников изменился. Создайте новый предпросмотр перед подтверждением.', 'warning') : '') +
    `<div class="inline-summary">${badge(`${active.length} / ${plan.target.tracks.length} выбрано`, 'success')}<span>${changed.length} изменений</span>${linkChanges ? `<span>${linkChanges} позиций: сохранить связи</span>` : ''}<span>${kept} файлов без публикации останутся во входящих</span><span>MKA · перепаковка без перекодирования</span></div>` +
    panel('Позиции и последствия', table(['Включить', '№ / трек', 'Источник', 'Сейчас опубликовано', 'Будет записано', 'Действие'], rows.map(row => [`<input type="checkbox" data-plan-include="${row.targetTrack.id}" aria-label="Включить ${esc(row.targetTrack.title)}" ${row.assignment?.included && row.file ? 'checked' : ''} ${!row.file ? 'disabled' : ''}>`, `${row.targetTrack.position} · ${esc(row.targetTrack.title)}`, row.file ? `${esc(row.file.name)}<span class="sub">${row.file.codec}</span>` : 'Нет файла', row.previous ? `${row.previous.codec}<span class="sub">${showTag(row.previous.tags.TITLE)}</span>` : 'Нет публикации', `${showTag(row.nextTags.TITLE)}<span class="sub">${showTag(row.nextTags.ALBUM)}</span>`, badge(row.action, row.action === 'Пропустить' ? '' : 'info')]))) +
    note(`<strong>Исходники сохранятся.</strong> ${plan.target.tracks.length - active.length} позиций не включены. Их существующие публикации, если есть, тоже сохранятся. Неразмеченные файлы не удаляются и не считаются обработанными.`) +
    `<div class="panel-foot mono">Целевой каталог: /srv/music/${esc(artistName(plan.target.artistId))}/${esc(plan.target.year)} - ${esc(plan.target.title)}/<br>Имена: 01 - название.mka · В этом макете файлы на диске не создаются.</div><div class="commit-bar"><div><strong>${changed.length ? `${changed.length} файлов изменится` : 'Аудиофайлы не изменятся'}</strong><span class="sub">${linkChanges ? `${linkChanges} позиций получат новые связи. ` : ''}${active.length ? `${active.length - changed.length} выбранных позиций уже актуальны.` : ''}</span></div>${button('publish-plan', !changed.length && linkChanges ? 'Подтвердить изменения' : 'Подтвердить публикацию', canConfirm ? '' : 'disabled', true)}</div>`;
}

function bulkPreviewScopeKey() {
  return ui.contextPreviewTrackId ? `track:${ui.contextPreviewTrackId}` : selectedTrackIds().sort().join('|');
}

function bulkPreviewTargets() {
  const selected = ui.contextPreviewTrackId
    ? [findLocalTrack(ui.contextPreviewTrackId)].filter(Boolean)
    : selectedTrackIds().map(id => findLocalTrack(id)).filter(Boolean);
  return [...new Map(selected.map(item => [item.track.id, item])).values()];
}

function bulkPreview() {
  const unique = bulkPreviewTargets();
  const rows = unique.map(({ release, track }) => {
    track.metadataChanged = metadataChanged(track, release);
    const file = demoFiles[track.sourceId]?.available && demoFiles[track.sourceId]?.locations?.length ? demoFiles[track.sourceId] : null;
    const usable = Boolean(file || track.published);
    const action = !track.published ? file ? 'Создать' : 'Пропустить' : track.metadataChanged ? 'Обновить' : 'Без изменений';
    const reason = !usable ? 'Нет выбранного источника и текущей публикации; другой источник не выбирается' : action === 'Без изменений' ? 'Метаданные совпадают со снимком публикации' : file ? 'Выбранный источник доступен' : 'Можно применить к существующей публикации без источника';
    const destination = publicationDestination(release, track);
    return { release, track, file, action, reason, nextTags: providerTags(release, track), destination };
  });
  ui.bulkRows = rows;
  ui.bulkPreviewVersion = ui.inventoryVersion;
  ui.bulkPreviewScope = bulkPreviewScopeKey();
  const returnRoute = ui.contextPreviewTrackId ? `track/${ui.contextPreviewTrackId}` : 'library';
  const counts = Object.fromEntries(['Создать', 'Обновить', 'Без изменений', 'Пропустить'].map(action => [action, rows.filter(row => row.action === action).length]));
  const unprocessable = rows.filter(row => !row.file && !row.track.published).length;
  const tableRows = rows.map(({ release, track, file, action, reason, nextTags, destination }) => {
    const effect = track.published
      ? `Звук и качество останутся прежними (${track.published.codec}).${file && file.codec !== track.published.codec ? ` Выбранный источник ${file.codec} в этом действии не применяется.` : ''}`
      : file
        ? `Новая публикация из выбранного источника (${file.codec}).`
        : 'Публикация не будет создана.';
    return [`${link(`release/${release.id}`, release.title)} · ${link(`track/${track.id}`, `${track.position}. ${track.title}`)}`, `${file?.path || 'Нет выбранного источника'}<span class="sub">${file?.codec || '—'}</span>`, track.published ? `${esc(destination.path)}<span class="sub">${destination.container} · сейчас ${track.published.codec}</span>` : `${esc(destination.path)}<span class="sub">${destination.container} · будущий файл</span>`, `${action} · ${reason}<span class="sub">${effect}</span>`, `${showTag(nextTags.TITLE)}<span class="sub">${showTag(nextTags.ALBUM)}</span>`];
  });
  return header('Предпросмотр медиатеки', 'Показаны существующие позиции выбранных объектов; повторяющиеся треки объединены. Новые сущности и альтернативные источники не выбираются.', link(returnRoute, ui.contextPreviewTrackId ? 'Вернуться к треку' : 'Вернуться в медиатеку', 'button')) +
    `<div class="inline-summary">${badge(`Создать ${counts['Создать']}`, 'info')}${badge(`Обновить ${counts['Обновить']}`, 'warning')}${badge(`Без изменений ${counts['Без изменений']}`, 'success')}${badge(`Пропустить ${counts['Пропустить']}`, 'warning')}<span>${unprocessable} не обрабатываемых</span></div>` +
    panel('Позиции · источник, публикация и метаданные', table(['Позиция', 'Источник / формат', 'Путь публикации / формат', 'Действие и причина', 'Снимок метаданных'], tableRows)) +
    note('Для уже опубликованных позиций это действие обновляет только метаданные: опубликованный звук и его качество остаются прежними. Даже если выбран более качественный источник, он не будет применён здесь. Для новых публикаций используется выбранный источник. Изменение качества остаётся отдельным действием на странице позиции.', 'warning') +
    `${ui.bulkPreviewVersion !== ui.inventoryVersion ? note('Инвентарь источников изменился. Вернитесь к объекту и создайте новый предпросмотр.', 'warning') : ''}<div class="actions">${button('apply-bulk-metadata', 'Опубликовать / обновить', rows.some(row => row.action === 'Обновить' || row.action === 'Создать') && ui.bulkPreviewVersion === ui.inventoryVersion ? '' : 'disabled', true)}${button('refresh-provider-cache', 'Обновить кэш провайдера без записи')}</div>`;
}

function applyPlan(plan, automatic = false) {
  let release = localRelease(plan.localReleaseId);
  const rows = planRows(plan);
  if (!release) {
    release = { ...structuredClone(plan.target), id: `import-${plan.target.id}`, providerId: plan.mode === 'provider' ? plan.target.id : null, tracks: plan.target.tracks.map(track => ({ ...track, id: `import-${plan.target.id}-t${track.position}`, sourceId: null, providerTrackId: null, published: null })) };
    localReleases.push(release); plan.localReleaseId = release.id;
  }
  let changed = 0;
  for (const row of rows) {
    const track = release.tracks.find(item => item.position === row.targetTrack.position);
    if (!track) continue;
    const assigned = automatic && row.previous ? [] : associatedSourceIds(row.assignment);
    const usableAssigned = assigned.filter(id => demoFiles[id]?.available && demoFiles[id]?.locations?.length);
    if (usableAssigned.length) track.sourceIds = [...new Set([...trackSourceIds(track).filter(id => demoFiles[id]?.available && demoFiles[id]?.locations?.length), ...usableAssigned])];
    if (!row.assignment?.included || !row.file || row.action === 'Без изменений') continue;
    if (automatic && row.previous && !(row.previous.codec === 'MP3' && row.file.codec === 'FLAC')) continue;
    track.sourceId = selectedSourceId(row.assignment);
    if (!(automatic && row.previous)) {
      track.providerTrackId = row.assignment.providerTrackId || null;
      if (row.provider) track.recordingId = row.provider.track.recordingId;
    }
    const destination = publicationDestination(release, track);
    const audioOnlyUpgrade = automatic && Boolean(row.previous);
    if (audioOnlyUpgrade) {
      track.published = { ...row.previous, codec: row.file.codec, sourceId: row.file.id, readAt: 'Только что · демо' };
    } else {
      track.published = { tags: structuredClone(row.nextTags), coverHash: track.coverHash || null, lyrics: track.lyrics || null, codec: row.file.codec, container: destination.container, sourceId: row.file.id, readAt: 'Только что · демо', path: destination.path };
      track.metadataBaseline = structuredClone(track.published.tags);
      track.metadataBaselineExtras = { coverHash: track.published.coverHash, lyrics: track.published.lyrics };
    }
    changed++;
  }
  return changed;
}

const demoDirectories = {
  '/': ['mnt', 'srv', 'var'],
  '/mnt': ['archive', 'audio', 'backup', 'inbox', 'music', 'projects', 'samples', 'shared', 'staging'],
  '/mnt/archive': [],
  '/mnt/inbox': ['Northern Lines', 'Road Notes', 'Small Hours'],
  '/mnt/audio': ['incoming', 'masters'],
  '/mnt/music': ['Albums', 'Compilations'],
  '/mnt/music/Albums': ['Artist A', 'Artist B'],
  '/mnt/music/Compilations': ['Road Notes'],
  '/srv': ['media', 'music'],
  '/srv/media': ['library'],
  '/srv/music': ['Albums', 'Compilations'],
  '/srv/music/Albums': ['Northern Lines', 'Small Hours'],
  '/srv/music/Compilations': ['Road Notes'],
  '/var': ['lib'],
  '/var/lib': ['melotrove'],
  '/var/lib/melotrove': ['cache', 'tools'],
  '/var/lib/melotrove/tools': []
};
const demoUnreadableDirectories = new Set(['/mnt/archive']);

function parentDirectory(path) {
  if (path === '/') return '/';
  const parts = path.split('/').filter(Boolean);
  parts.pop();
  return `/${parts.join('/')}` || '/';
}

function setPickerPath(path) {
  const picker = ui.folderPicker;
  if (!picker) return;
  picker.path = path;
  picker.error = '';
  picker.requestedPath = '';
  picker.editingPath = false;
  renderFolderPicker();
}

function renderFolderPicker() {
  const picker = ui.folderPicker;
  if (!picker) return;
  const restoreDialogFocus = folderDialog.open && folderDialog.contains(document.activeElement);
  const path = picker.path;
  const folders = demoDirectories[path];
  const pieces = path === '/' ? [] : path.split('/').filter(Boolean);
  const crumbs = [path === '/' ? `<button type="button" class="folder-current-path" data-folder-action="edit-path" aria-current="location" aria-label="Редактировать путь" title="Редактировать путь">Сервер <span aria-hidden="true">✎</span></button>` : `<button type="button" data-folder-action="path" data-path="/">Сервер</button>`, ...pieces.map((piece, index) => {
    const crumbPath = `/${pieces.slice(0, index + 1).join('/')}`;
    if (crumbPath === path) return `<span aria-hidden="true">/</span><button type="button" class="folder-current-path" data-folder-action="edit-path" aria-current="location" aria-label="Редактировать путь" title="Редактировать путь">${esc(piece)} <span aria-hidden="true">✎</span></button>`;
    return `<span aria-hidden="true">/</span><button type="button" data-folder-action="path" data-path="${esc(crumbPath)}">${esc(piece)}</button>`;
  })].join('');
  let listing;
  if (demoUnreadableDirectories.has(path)) {
    listing = `<div class="folder-state error-state"><strong>Каталог недоступен</strong><span>Демо-ответ: сервер не может прочитать этот каталог. Выберите другой путь или вернитесь назад.</span></div>`;
  } else if (!folders) {
    listing = `<div class="folder-state error-state"><strong>Путь не найден</strong><span>В демонстрационном дереве такого каталога нет. Проверьте путь или выберите папку из списка.</span></div>`;
  } else if (!folders.length) {
    listing = `<div class="folder-state"><strong>В этой папке нет вложенных каталогов</strong><span>Вы можете выбрать текущую папку.</span></div>`;
  } else {
    listing = `<div class="folder-list" role="list" aria-label="Подкаталоги">${folders.map(folder => {
      const childPath = `${path === '/' ? '' : path}/${folder}`;
      return `<button class="folder-row" type="button" role="listitem" data-folder-action="path" data-path="${esc(childPath)}"><span class="folder-glyph" aria-hidden="true">▰</span><span class="folder-name">${esc(folder)}</span><span class="folder-chevron" aria-hidden="true">›</span></button>`;
    }).join('')}</div>`;
  }
  const canChoose = Boolean(folders) && !demoUnreadableDirectories.has(path) && !picker.error;
  const pathEditor = `<form id="folder-path-form" class="folder-path-form" ${picker.editingPath ? '' : 'hidden'}><label class="sr-only" for="folder-path">Абсолютный серверный путь</label><input id="folder-path" name="path" value="${esc(picker.requestedPath || path)}" autocomplete="off" spellcheck="false" aria-label="Абсолютный серверный путь" aria-invalid="${picker.error ? 'true' : 'false'}"></form>`;
  document.querySelector('#folder-picker-content').innerHTML = `<header class="folder-picker-head"><div class="folder-picker-title"><h2 id="folder-picker-title">Папка на сервере</h2>${badge('Демо')}</div><button type="button" class="icon-close" data-folder-action="cancel" aria-label="Закрыть выбор папки">×</button></header><p id="folder-picker-help" class="folder-picker-help">Каталоги на сервере MeloTrove — не файлы устройства.</p><nav class="folder-navigation" aria-label="Навигация по каталогам"><button type="button" class="folder-up" data-folder-action="up" aria-label="На уровень выше" title="На уровень выше" ${path === '/' ? 'disabled' : ''}><span aria-hidden="true">↑</span></button><div class="folder-breadcrumbs" role="group" aria-label="Путь к папке">${crumbs}</div></nav>${pathEditor}<div class="folder-listing" aria-live="polite">${picker.error ? `<div class="folder-state error-state"><strong>Путь не найден</strong><span>${esc(picker.error)}</span></div>` : listing}</div><footer class="folder-picker-foot"><div class="actions">${button('folder-cancel', 'Отмена')}${button('folder-choose', 'Выбрать папку', canChoose ? '' : 'disabled', true)}</div></footer>`;
  if (restoreDialogFocus) {
    const focusTarget = picker.editingPath ? document.querySelector('#folder-path') : document.querySelector('.folder-up');
    focusTarget?.focus({ preventScroll: true });
  }
}

function openFolderPicker(target, currentPath) {
  const startPath = typeof currentPath === 'string' && demoDirectories[currentPath] ? currentPath : '/';
  ui.folderPicker = { target, path: startPath, error: '', requestedPath: '', editingPath: false };
  renderFolderPicker();
  folderDialog.showModal();
  document.querySelector('.folder-current-path').focus();
}

function sources() {
  const roots = ui.sourceRoots || [
    ['Входящие', '/mnt/inbox', 'staged', 'Только чтение', badge('Доступен', 'success'), '26.09.2026 10:20', link('review', 'Разобрать группы', 'button')],
    ['Архивный диск', '/mnt/archive', 'in_place', 'Только чтение', badge('Недоступен', 'warning'), '25.09.2026 18:40', button('check-source', 'Проверить')]
  ];
  const modeControl = (mode, index) => `<label class="sr-only" for="source-mode-${index}">Режим обработки: ${esc(roots[index][0])}</label><select id="source-mode-${index}" data-source-mode="${index}"><option value="in_place" ${mode === 'in_place' ? 'selected' : ''}>На месте</option><option value="staged" ${mode === 'staged' ? 'selected' : ''}>Через рабочую копию</option></select><span class="sub">Изменение влияет на будущую обработку.</span>${mode === 'staged' ? `<span class="sub">Общая рабочая папка: ${ui.sourceWorkDirectory ? `<code>${esc(ui.sourceWorkDirectory)}</code>` : '<span class="status warning">Не выбрана</span>'} · ${link('settings', 'Настроить')}</span>` : ''}`;
  const addForm = ui.sourceAddOpen ? `<div class="padded source-add-form"><div class="two-columns"><label class="field" for="source-name">Имя источника<input id="source-name" value="${esc(ui.sourceName || '')}" placeholder="Например, Музыкальная библиотека"></label><div class="field"><label for="source-path">Каталог на сервере</label><div class="path-field-control"><input id="source-path" value="${esc(ui.sourcePath)}" readonly aria-describedby="source-path-help">${button('pick-source-path', 'Выбрать папку')}</div><span class="sub" id="source-path-help">Только каталог; файлы не загружаются.</span></div></div><fieldset class="field"><legend>Режим обработки · выберите явно</legend><label><input type="radio" name="source-mode" value="in_place" ${ui.sourceMode === 'in_place' ? 'checked' : ''}> На месте</label><p class="sub">Анализ выполняется по исходному файлу; сам источник остаётся только для чтения.</p><label><input type="radio" name="source-mode" value="staged" ${ui.sourceMode === 'staged' ? 'checked' : ''}> Через рабочую копию</label><p class="sub">Для обработки создаётся принадлежащая работе рабочая копия аудио в общей рабочей папке сервера. Папка требуется до обработки, но не для добавления источника.</p></fieldset><p class="sub"><strong>Политика источника:</strong> только чтение — независимо от выбранного режима.</p><div class="actions">${button('save-source', 'Добавить источник', '', true)}${button('cancel-source', 'Отмена')}</div></div>` : '';
  const rows = roots.map(([name, path, mode, readOnly, availability, checked, action], index) => [esc(name), `<code>${esc(path)}</code>`, modeControl(mode, index), esc(readOnly), availability, checked, `<div class="actions">${action}${button('process-source', 'Обработать · демо', `data-mode="${esc(mode)}" data-path="${esc(path)}"`)}</div>`]);
  return header('Источники', 'Серверные пути, политика источника — только чтение. Режим обработки выбирается отдельно; файлы не загружаются перетаскиванием.', `${button('source-add', 'Добавить источник', '', true)}${button('source-scan', 'Сканировать · демо')}`) + (ui.sourceAddOpen ? panel('Новый источник', addForm) : '') + panel('Подключённые каталоги', table(['Имя', 'Путь', 'Режим обработки', 'Политика источника', 'Доступность', 'Последняя проверка', ''], rows)) + sourceRootDemo() + note('Недоступность исходного диска не удаляет существующие управляемые публикации.') + note(`Общая рабочая папка рабочих копий: ${ui.sourceWorkDirectory ? `<code>${esc(ui.sourceWorkDirectory)}</code>` : 'не выбрана'} · ${link('settings', 'Настроить в Настройках')}`, ui.sourceWorkDirectory ? '' : 'warning');
}

function removeSourceRoot(rootId) {
  if (ui.removedSourceRoots.has(rootId)) { notify('Этот каталог источника уже удалён из инвентаря.'); return; }
  const path = rootId === 'root-inbox' ? '/mnt/inbox' : '/mnt/music';
  const locations = Object.values(demoFiles).flatMap(file => file.locations || []).filter(location => location.rootId === rootId);
  const plan = { rootId, path, count: locations.length, locationIds: locations.map(location => location.id).sort() };
  if (rootId === 'root-library' && ui.rootOperationActive) { notify('Удаление отклонено: для этого каталога источника выполняется операция.'); return; }
  confirm(`Удалить каталог источника ${path}?`, `<p>Путь: <code>${esc(path)}</code><br>Записей о файлах в инвентаре: <strong>${plan.count}</strong>.</p><p>Удаляются только запись каталога, связанные с ней записи о файлах и сведения об источнике. Физические исходники, объекты медиатеки и текущие публикации сохраняются.</p><p>Связь позиции с источником сохраняется, если у файла осталась запись в другом каталоге. При удалении последней записи о файле связи с ним снимаются; кэш анализа по SHA-256 остаётся. Выбранный исходник очищается без автоматической замены.</p>${note('Перед подтверждением список файлов и наличие выполняемой операции будут перепроверены. Демо не обращается к реальной файловой системе.')}`, () => {
    const freshLocations = Object.values(demoFiles).flatMap(file => file.locations || []).filter(location => location.rootId === plan.rootId);
    if (plan.rootId === 'root-library' && ui.rootOperationActive) { notify('Подтверждение устарело: началась операция; каталог не удалён.'); return; }
    if (freshLocations.length !== plan.count || freshLocations.map(location => location.id).sort().join('|') !== plan.locationIds.join('|')) { notify('Подтверждение устарело: список файлов в инвентаре изменился. Пересмотрите данные.'); render(false); return; }
    for (const file of Object.values(demoFiles)) {
      if (!file.locations) continue;
      file.locations = file.locations.filter(location => location.rootId !== rootId);
      if (!file.locations.length) {
        file.available = false;
        if (ui.selectedFile === file.id) ui.selectedFile = null;
        for (const incomingGroup of incomingGroups) {
          incomingGroup.fileIds = incomingGroup.fileIds.filter(id => id !== file.id);
          for (const assignments of Object.values(incomingGroup.drafts || {})) {
            for (const assignment of Object.values(assignments)) {
              if (assignment.sourceIds) assignment.sourceIds = assignment.sourceIds.filter(id => id !== file.id);
              if (assignment.fileId === file.id) assignment.fileId = null;
              if (assignment.selectedSourceId === file.id) assignment.selectedSourceId = null;
              if (assignment.sourceMeta) delete assignment.sourceMeta[file.id];
            }
          }
        }
        for (const { track } of allTracks()) {
          if (!trackSourceIds(track).includes(file.id)) continue;
          track.sourceIds = trackSourceIds(track).filter(id => id !== file.id);
          if (track.sourceId === file.id) track.sourceId = null;
        }
        if (!file.sha256) delete demoFiles[file.id];
      }
    }
    ui.removedSourceRoots.add(rootId);
    ui.inventoryVersion++;
    ui.bulkPreviewVersion = null;
    notify(`Демо: каталог ${path} удалён из инвентаря. Файлы на диске, публикации и медиатека не изменились; другой исходник автоматически не выбран.`);
    render(false);
  });
}

function operations() {
  const demoDeletes = ui.demoDeleteOperations.map(operation => [esc(operation.target), 'Удаление медиатеки', badge('В очереди · демо', 'warning'), esc(operation.scope), `${operation.publications} строк публикаций; ${operation.paths.length} принадлежащих приложению файлов в подтверждённой области`, 'Ожидает · запрос не меняет демонстрационные данные']);
  return header('Текущие операции', 'Незавершённая работа; завершённой истории здесь нет.') + panel('Требует внимания', table(['Объект', 'Операция', 'Фаза', 'Причина', 'Последствие', ''], [
    ['Signals · позиция 06', 'Публикация набора позиций', badge('Ошибка', 'error'), 'EACCES: нет записи во временном каталоге', 'Обычный повтор заново подготавливает публикацию; это не принудительная перепубликация', button('retry', 'Повторить')],
    ...(!findLocalTrack(recoveryFixture.trackId)?.track.recovered ? [['Northern Lines · позиция 01', 'Прерванная публикация', badge('Прервана', 'warning'), 'Файл установлен, результат не зарегистрирован', 'Сверить теги, технические параметры и разрешение операции', button('recover-publication', 'Восстановить регистрацию', `data-track="${recoveryFixture.trackId}"`)]] : [])
  ])) + panel('Удаление из медиатеки · демонстрационные запросы', table(['Объект', 'Операция', 'Состояние', 'Подтверждённая область', 'Файлы / публикации', 'Результат'], demoDeletes)) + note('Восстановление может зарегистрировать подходящий текущий файл по тегам, техническим параметрам и актуальному разрешению; совпадение с исходником не проверяется. Отдельное действие «Явно перепубликовать» полностью подготавливает файл из выбранного исходника, даже если данные не изменились. Здесь только демонстрация, реальные файловые операции и гарантии безопасности не проверяются.', 'warning');
}

function libraryDeletionPlan(kind, id) {
  if (kind === 'publication') {
    const context = findLocalTrack(id);
    if (!context?.track.published) return null;
    const publication = context.track.published;
    return {
      target: `${context.release.title} · ${String(context.track.position).padStart(2, '0')} · ${context.track.title}`,
      scope: 'Только выбранная публикация; позиция, издание, композиция, записи провайдера и другие публикации сохраняются.',
      publications: 1,
      paths: [publication.path],
      dependencies: []
    };
  }
  if (kind === 'release') {
    const release = localRelease(id);
    if (!release) return null;
    const publications = release.tracks.filter(track => track.published);
    const recordings = [...new Map(release.tracks.filter(track => track.recordingId).map(track => [track.recordingId, { id: track.recordingId, title: track.title }])).values()];
    return {
      target: release.title || 'Релиз без названия',
      scope: `${release.tracks.length} позиций релиза; самостоятельные композиции, записи провайдера и публикации других релизов сохраняются.`,
      publications: publications.length,
      paths: publications.map(track => track.published.path),
      dependencies: [
        { title: 'Релиз (будет удалён)', items: [release.title || 'Релиз без названия'] },
        { title: 'Композиции (сохраняются)', items: recordings.map(recording => `${recording.title} · ${recording.id}`) },
        { title: 'Позиции релиза (будут удалены; включая неопубликованные)', items: release.tracks.map(track => `${release.title} · ${String(track.position).padStart(2, '0')} · ${track.title}${track.published ? ' · опубликована' : ' · без публикации'}`) }
      ]
    };
  }
  const artist = demoArtist(id);
  if (!artist) return null;
  const releases = localReleases.filter(release => release.artistId === id || release.credits?.some(credit => credit.artistId === id));
  const directRecordingIds = new Set(allTracks().filter(({ track }) => track.artistId === id).map(({ track }) => track.recordingId).filter(Boolean));
  const affectedTracks = new Map();
  for (const release of releases) for (const track of release.tracks) affectedTracks.set(track.id, { release, track });
  for (const item of allTracks()) if (directRecordingIds.has(item.track.recordingId)) affectedTracks.set(item.track.id, item);
  const affected = [...affectedTracks.values()];
  const publications = affected.filter(({ track }) => track.published);
  const recordings = [...new Map(affected.filter(({ track }) => track.recordingId).map(({ track }) => [track.recordingId, { id: track.recordingId, title: track.title, delete: directRecordingIds.has(track.recordingId) }])).values()];
  return {
    target: artist.name,
    scope: `${releases.length} связанных релизов; ${directRecordingIds.size} напрямую связанных композиций; ${affectedTracks.size} позиций (включая позиции этих композиций в сохраняемых релизах). Остальные сущности медиатеки и все записи провайдера сохраняются.`,
    publications: publications.length,
    paths: publications.map(({ track }) => track.published.path),
    dependencies: [
      { title: 'Релизы (будут удалены)', items: releases.map(release => release.title || 'Релиз без названия') },
      { title: 'Композиции (напрямую связанные с артистом будут удалены; прочие сохраняются)', items: recordings.map(recording => `${recording.title} · ${recording.id} · ${recording.delete ? 'будет удалён' : 'сохранится'}`) },
      ...releases.map(release => ({ title: `Позиции релиза «${release.title}» (будут удалены; включая неопубликованные)`, items: release.tracks.map(track => `${String(track.position).padStart(2, '0')} · ${track.title}${track.published ? ' · опубликована' : ' · без публикации'}`) })),
      { title: 'Позиции удаляемых композиций в сохраняемых релизах (будут удалены)', items: affected.filter(({ release, track }) => !releases.includes(release) && directRecordingIds.has(track.recordingId)).map(({ release, track }) => `${release.title} · ${String(track.position).padStart(2, '0')} · ${track.title}${track.published ? ' · опубликована' : ' · без публикации'}`) }
    ].filter(section => section.items.length)
  };
}

function requestLibraryDeletion(kind, id) {
  const plan = libraryDeletionPlan(kind, id);
  if (!plan) return;
  const paths = plan.paths.length
    ? `<ul class="delete-paths">${plan.paths.map(path => `<li><code>${esc(path)}</code></li>`).join('')}</ul>`
    : '<p>Текущих публикаций в этой области нет.</p>';
  const dependencies = (plan.dependencies || []).map(section => `<h3>${esc(section.title)}</h3><ul>${section.items.map(item => `<li>${esc(item)}</li>`).join('')}</ul>`).join('');
  const operation = { target: plan.target, scope: plan.scope, publications: plan.publications, paths: plan.paths };
  confirm(`Удалить: ${plan.target}?`, `<p>${esc(plan.scope)}</p><p>Перед удалением строк будут безопасно удалены только файлы публикаций, принадлежащие приложению (${plan.publications}). После этого удаляются строки публикаций и указанные сущности медиатеки. Записи провайдера сохраняются; чужие/общие файлы не затрагиваются.</p>${dependencies}${paths}${note('Асинхронная операция. Прототип только ставит демонстрационный запрос в список операций: реальные файлы и демонстрационные записи не удаляются.')}`, () => {
    ui.demoDeleteOperations.push(operation);
    ui.notice = `Демо: запрос удаления «${plan.target}» принят как асинхронная операция. Данные прототипа и файлы не изменены.`;
    location.hash = 'operations';
  });
}

function outputDirectorySettings() {
  const blocked = ['running', 'claimed'].includes(ui.outputDemoExecution);
  const samePath = !ui.outputCandidate || ui.outputCandidate === '/srv/music';
  const executionLabel = { idle: 'Нет активных операций', queued: 'Есть только ожидающие операции', running: 'Выполняется операция', claimed: 'Операция уже захвачена обработчиком' }[ui.outputDemoExecution];
  const statusNote = blocked
    ? note(`Смена заблокирована: ${executionLabel.toLowerCase()}.`, 'warning')
    : ui.outputDemoExecution === 'queued'
      ? note('Ожидающие задачи не блокируют смену; при подтверждении все очереди и задания будут сброшены.', 'warning')
      : note('Активных операций нет в выбранном демонстрационном состоянии.');
  return panel('Каталог публикации', `<div class="padded output-directory-settings">
    <div class="field"><label for="settings-output-current">Текущий каталог публикации · только чтение</label><input id="settings-output-current" value="/srv/music" readonly></div>
    <div class="field"><label for="settings-output-candidate">Новый путь · кандидат, ещё не применён</label><div class="path-field-control"><input id="settings-output-candidate" value="${esc(ui.outputCandidate)}" placeholder="Выберите папку на сервере" readonly aria-describedby="settings-output-candidate-help">${button('pick-output-path', 'Выбрать папку')}</div><span class="sub" id="settings-output-candidate-help">Выбор папки только задаёт кандидата. Отмена в окне выбора ничего не меняет.</span></div>
    <div class="field"><label for="output-demo-execution">Состояние выполнения · только для демонстрации</label><select id="output-demo-execution"><option value="idle" ${ui.outputDemoExecution === 'idle' ? 'selected' : ''}>Нет активных операций</option><option value="queued" ${ui.outputDemoExecution === 'queued' ? 'selected' : ''}>Есть ожидающие операции</option><option value="running" ${ui.outputDemoExecution === 'running' ? 'selected' : ''}>Выполняется операция</option><option value="claimed" ${ui.outputDemoExecution === 'claimed' ? 'selected' : ''}>Операция захвачена обработчиком</option></select><span class="sub">Это демонстрационный переключатель состояния, не настройка продукта. Сейчас: ${esc(executionLabel)}.</span></div>
    ${statusNote}
    <div class="output-reset-warning"><strong>Что произойдёт при подтверждении смены</strong><ul><li>Сменится каталог публикации: <code>/srv/music</code> → <code>${esc(ui.outputCandidate || '—')}</code>.</li><li>Все очереди и задания будут сброшены; ожидающие задачи смене не препятствуют.</li><li>Все ссылки БД на прежний каталог публикации, включая строки публикаций и ссылки на размещённые в нём служебные файлы, будут удалены.</li><li>Физические файлы в старом каталоге не удаляются.</li><li>Автоматической повторной публикации не будет.</li><li>Инвентарь источников, сущности библиотеки, связи с источниками и анализы сохраняются.</li><li>Общая рабочая папка рабочих копий анализа источников и её рабочие копии аудио сохраняются; их учёт в БД (принадлежность, соответствие источнику и пригодность) и незавершённые запрошенные шаги анализа не сбрасываются.</li><li>Сброшенная обработка не возобновляется автоматически ни вследствие сброса, ни после последующего перезапуска; явный запуск может переиспользовать пригодную копию.</li></ul></div>
    <div class="actions">${button('confirm-output-reset', 'Подтвердить смену каталога публикации', blocked || samePath ? 'disabled' : '', true)}</div>
    ${samePath && ui.outputCandidate ? '<span class="sub">Кандидат совпадает с текущим каталогом публикации: менять нечего, сброс не запускается.</span>' : ''}
    ${note('Статический макет: реального сброса нет. Текущий путь, очереди, БД, файлы и демонстрационные публикации не изменяются.')}
  </div>`);
}

function settings() {
  return header('Настройки', 'Тема доступна в верхней панели. Рабочие настройки продукта сохраняются в БД; здесь демонстрация.', button('check-updates', 'Проверить обновления', '', true)) +
    `<div class="genre-library-panel">${panel('Библиотека жанров', `${window.GenreLibraryPrototype.statusMarkup()}${window.GenreLibraryPrototype.controls('Загрузить / обновить каталог в демо')}`)}</div><div class="two-columns">${outputDirectorySettings()}${panel('Рабочая папка анализа источников', `<div class="padded"><div class="field"><label for="settings-source-work-path">Общая папка для рабочих копий аудио</label><div class="path-field-control"><input id="settings-source-work-path" value="${esc(ui.sourceWorkDirectory)}" readonly aria-describedby="settings-source-work-help">${button('pick-source-work-path', 'Выбрать папку')}</div><span class="sub" id="settings-source-work-help">Серверный путь, общий для всех источников с рабочими копиями; отдельный от каталога публикации. ${ui.sourceWorkDirectory ? `Текущий выбор: <code>${esc(ui.sourceWorkDirectory)}</code>.` : 'Не задана.'}</span></div><p>Настройте её до начала обработки через рабочие копии. Первый запуск и добавление источника не требуют выбора. В реальном продукте смена запрещена, пока папку используют операции или в ней остаются сохранённые копии; автоматического переноса нет.</p>${note('Статический макет не проверяет файловую систему и не моделирует занятые операции или сохранённые копии; путь не проверен.')}</div>`)}${panel('Инструменты · Linux amd64', table(['Инструмент', 'Активная версия', 'Источник', 'Проверено'], [['ffmpeg', ui.toolsUpdated ? '8.0.1' : '8.0', 'BtbN/FFmpeg-Builds', 'SHA-256 если опубликован + -version'], ['ffprobe', ui.toolsUpdated ? '8.0.1' : '8.0', 'BtbN/FFmpeg-Builds', 'SHA-256 если опубликован + -version'], ['fpcalc', '1.5.1', 'AcoustID Chromaprint', '-version']]) + `<div class="panel-foot"><div class="field"><label for="settings-tools-path">Постоянный каталог инструментов</label><div class="path-field-control"><input id="settings-tools-path" value="${esc(ui.toolsPath)}" readonly aria-describedby="settings-tools-path-help">${button('pick-tools-path', 'Выбрать папку')}</div><span class="sub" id="settings-tools-path-help">Каталог на сервере для управляемых инструментов.</span></div>Версии условные. Проверка только уведомляет; установка и откат явные.<div class="actions">${ui.toolsChecked && !ui.toolsUpdated ? button('install-tools', 'Установить обновление') : ''}${ui.toolsUpdated ? button('rollback', 'Откатить к 8.0') : ''}</div></div>`)}${panel('Обработка и публикация', facts([['Полный уверенный релиз', 'Автоматически сопоставить и опубликовать'], ['Частичный / неоднозначный', 'Ручной разбор; частичная публикация разрешена'], ['Исходники', 'Сохранить; неразмеченные остаются во входящих'], ['Оценка', 'Политика matching-rapidfuzz-v1; применимость ≥0.70. Макет не вычисляет уверенность'], ['Формат / каталог', 'MKA · /srv/music'], ['Метаданные / тексты', 'MusicBrainz / LRCLIB']]))}</div>`;
}

function setup() {
  const steps = ['Каталог', 'Инструменты', 'Публикация', 'Итог'];
  const content = [
    `<div class="field"><label for="tools-path">Постоянный каталог инструментов</label><div class="path-field-control"><input id="tools-path" value="${esc(ui.toolsPath)}" readonly aria-describedby="path-help">${button('pick-tools-path', 'Выбрать папку')}</div><span class="sub" id="path-help">Абсолютный путь на сервере, не на компьютере с браузером.</span></div><div class="actions">${button('check-path', 'Проверить путь')}</div>`,
    table(['Инструмент', 'Версия · демо', 'Проверка'], [['ffmpeg', '8.0', 'SHA-256 если опубликован · -version'], ['ffprobe', '8.0', 'SHA-256 если опубликован · -version'], ['fpcalc', '1.5.1', '-version']]) + note('Каждый исполняемый файл проверяется через -version; SHA-256 проверяется, только если он опубликован. В этом макете скачивания нет.'),
    facts([['Каталог', '<code>/srv/music</code>'], ['Режим примера', 'MKA · перепаковка без перекодирования'], ['Исходники', 'Только чтение'], ['Граница продукта', 'Также предусмотрен исходный формат; макет демонстрирует MKA']]) + `<section class="setup-genre-library"><h3>Библиотека жанров · необязательно для продолжения</h3>${window.GenreLibraryPrototype.statusMarkup()}${window.GenreLibraryPrototype.controls()}</section>`,
    facts([['MusicBrainz', 'Публичный сервис · поддержка собственного размещения в спецификации'], ['LRCLIB', 'Тексты'], ['Авторизация', 'Внешний прокси авторизации'], ['База данных', 'Подключена · демо'], ['Частичные релизы', 'Разрешены']])
  ];
  return header('Первый запуск', 'Требуется рабочая конфигурация инструментов. Версии, доступность и пути демонстрационные.') + `<div class="stepper">${steps.map((step, i) => `<span class="${i === ui.setupStep ? 'active' : ''}">${i + 1}. ${step}</span>`).join('')}</div>` + panel(steps[ui.setupStep], `<div class="padded">${content[ui.setupStep]}<div class="actions">${ui.setupStep ? button('setup-back', 'Назад') : ''}${button('setup-next', ui.setupStep === 3 ? 'Завершить демонстрацию' : 'Продолжить', ui.setupStep === 0 && !ui.pathChecked ? 'disabled' : '', true)}</div></div>`);
}

function components() {
  return header('Компоненты · плотный интерфейс', 'Обе темы используют одинаковую геометрию и семантические состояния.') + panel('Состояния и действия', `<div class="padded actions">${badge('Высокая уверенность', 'success')}${badge('Есть отличия', 'warning')}${badge('Ошибка', 'error')}${badge('Без связи с провайдером')}${button('sample', 'Основное действие', '', true)}${button('sample', 'Вторичное действие')}<button disabled>Нет назначения</button></div>`) + panel('Файл для назначения', `<div class="padded">${fileChip(demoFiles['north-f3'])}</div>`) + panel('Техническая таблица', table(['Поле', 'Исходник', 'Провайдер', 'Различие'], [['TITLE', 'Northbound (live?)', 'Northbound', badge('Версия записи не подтверждена', 'warning')], ['DURATION', '3:58', '3:46', '+12 с']])) + note('Политика matching-rapidfuzz-v1 утверждена; этот макет не рассчитывает уверенность или её вклады. Демонстрационный вердикт подписан явно. Цвет не заменяет текст.');
}

function route() { return location.hash.slice(1).split('/'); }

function render(resetScroll = true) {
  const [requested, id, scope] = route();
  const name = requested || 'review';
  if (name === 'match' && incomingGroups.some(item => item.id === id) && ui.groupId !== id) {
    ui.groupId = id; ui.selectedTarget = null; ui.selectedFile = null; ui.recordingTarget = null; ui.providerResults = null; ui.providerQuery = group().mode === 'provider' ? currentTarget().title : '';
  }
  const renderers = { review, match, library, local: () => window.LocalProviderPrototype.render(), artist: () => artistPage(id), release: () => releasePage(id), 'create-release': manualReleasePage, track: () => trackPage(id), tags: () => tagsPage(id, scope), sources, publications, 'publication-preview': bulkPreview, 'publication-list': library, operations, settings, setup, components };
  const page = Object.hasOwn(renderers, name) ? name : 'review';
  const activeElement = document.activeElement;
  const focusId = activeElement?.id;
  const focusSelector = activeElement instanceof HTMLElement && view.contains(activeElement) ? [...activeElement.attributes].filter(attribute => attribute.name.startsWith('data-')).map(attribute => `[${attribute.name}="${CSS.escape(attribute.value)}"]`).join('') : '';
  const focusRadio = activeElement instanceof HTMLInputElement && activeElement.type === 'radio' && view.contains(activeElement) ? `input[type="radio"][name="${CSS.escape(activeElement.name)}"][value="${CSS.escape(activeElement.value)}"]` : '';
  const focusRow = activeElement instanceof Element ? activeElement.closest('[data-drop]')?.getAttribute('data-drop') : null;
  captureDetails(view);
  view.innerHTML = renderers[page]();
  preserveDetails(view);
  syncSelectionControls(view);
  if (page === 'create-release') window.initManualReleaseForm(view.querySelector('#editor-view'), ui.manualDraft, addManualRelease, draft => { ui.manualDraft = structuredClone(draft); }, confirm);
  document.querySelector('#notice').textContent = ui.notice;
  const heading = view.querySelector('h1');
  document.title = `${heading.textContent} · MeloTrove · макет v2`;
  document.querySelector('#breadcrumb').textContent = `${page === 'match' ? 'Входящие / ' : ''}${heading.textContent}`;
  const section = ['artist', 'release', 'track', 'tags', 'create-release', 'publication-list', 'publication-preview', 'publications'].includes(page) ? 'library' : page === 'match' ? 'review' : page;
  document.querySelectorAll('[data-nav]').forEach(item => {
    if (item.dataset.nav === section) item.setAttribute('aria-current', 'page'); else item.removeAttribute('aria-current');
  });
  document.querySelector('#review-count').textContent = String(incomingGroups.filter(item => !item.processed).length);
  if (resetScroll) { document.querySelector('main').scrollTop = 0; heading.focus({ preventScroll: true }); }
  else {
    const focusTarget = (focusId && document.getElementById(focusId)) || (focusRadio && view.querySelector(focusRadio)) || (focusSelector && view.querySelector(focusSelector)) || (focusRow && view.querySelector(`[data-drop="${CSS.escape(focusRow)}"] button`));
    if (focusTarget instanceof HTMLElement) focusTarget.focus({ preventScroll: true });
  }
}

function notify(text) { ui.notice = text; document.querySelector('#notice').textContent = text; }

function confirm(title, body, action) {
  document.querySelector('#dialog-title').textContent = title;
  document.querySelector('#dialog-body').innerHTML = body;
  dialog.returnValue = '';
  confirmAction = action;
  dialog.showModal();
}

function selectCandidate(id) {
  const g = group();
  g.mode = 'provider'; g.candidateId = id;
  ui.selectedFile = null; ui.selectedTarget = null; ui.recordingTarget = null;
  ui.notice = 'Выбрано другое издание. У каждого издания собственный черновик назначений; публикации не изменены.';
  render(false);
}

document.addEventListener('click', event => {
  if (!(event.target instanceof Element)) return;
  const target = event.target.closest('button');
  if (!target) return;
  const metadataAction = target.dataset.artistMetadata;
  if (metadataAction) {
    const panel = target.closest('[data-artist-metadata-panel]');
    const artist = panel && demoArtist(route()[1]);
    if (!panel || !artist) return;
    if (metadataAction === 'main' && target.dataset.mode === 'edit') {
      setArtistMetadataMode(panel, true);
      panel.querySelector('[name="name"]').focus({ preventScroll: true });
    } else if (metadataAction === 'cancel') {
      panel.querySelector('[data-artist-genre-dialog]').close();
      resetArtistMetadataForm(panel, artist);
      setArtistMetadataMode(panel, false);
      panel.querySelector('[data-artist-metadata="main"]').focus({ preventScroll: true });
    } else if (metadataAction === 'main' && target.dataset.mode === 'save') {
      const form = panel.querySelector('[data-artist-form]');
      const text = name => String(form.querySelector(`[name="${name}"]`)?.value || '').trim();
      if (!text('name')) {
        form.querySelector('[name="name"]').focus({ preventScroll: true });
        notify('Имя артиста не может быть пустым.');
        return;
      }
      artist.name = text('name');
      artist.sort_name = text('sort_name');
      artist.disambiguation = text('disambiguation');
      artist.tags = text('tags');
      if (form.dataset.genresDirty === 'true' && window.GenreLibraryPrototype?.catalogue?.().length) {
        const unavailable = artistGenreIds(artist).filter(id => !window.GenreLibraryPrototype.get(id));
        artist.genres = [...new Set([...unavailable, ...window.GenreLibraryPrototype.selectedIds(form, 'genreIds')])];
      }
      panel.querySelector('[data-artist-genres]').textContent = artistGenreNames(artist);
      document.querySelector('[data-artist-heading]').textContent = artist.name;
      document.title = `${artist.name} · MeloTrove · макет v2`;
      document.querySelector('#breadcrumb').textContent = artist.name;
      setArtistMetadataMode(panel, false);
      panel.querySelector('[data-artist-metadata="main"]').focus({ preventScroll: true });
      notify('Метаданные артиста сохранены только в объекте демо. Публикации и теги композиций не изменялись.');
    }
    return;
  }
  if (target.matches('[data-artist-genres]')) {
    const panel = target.closest('[data-artist-metadata-panel]');
    const artist = panel && demoArtist(route()[1]);
    const dialog = panel?.querySelector('[data-artist-genre-dialog]');
    const form = panel?.querySelector('[data-artist-form]');
    if (!artist || !dialog || !form) return;
    const ids = form.dataset.genresDirty === 'true' && window.GenreLibraryPrototype?.catalogue?.().length
      ? window.GenreLibraryPrototype.selectedIds(form, 'genreIds')
      : artistGenreIds(artist);
    const picker = window.GenreLibraryPrototype?.renderPicker
      ? window.GenreLibraryPrototype.renderPicker(ids, 'genreIds')
      : '<fieldset class="genre-picker" disabled><legend>Жанры</legend><p class="muted">Каталог недоступен. Можно сохранить запись без изменения жанров.</p></fieldset>';
    panel.querySelector('[data-artist-genre-picker]').innerHTML = `${picker}${window.GenreLibraryPrototype?.catalogue?.().length ? '' : '<button type="button" data-action="genre-library-load">Загрузить каталог жанров в демо</button>'}`;
    dialog.showModal();
    target.setAttribute('aria-expanded', 'true');
    return;
  }
  if (target.matches('[data-artist-genre-close]')) {
    const dialog = target.closest('[data-artist-genre-dialog]');
    dialog?.close();
    return;
  }
  if (target.dataset.folderAction) {
    switch (target.dataset.folderAction) {
      case 'cancel': folderDialog.close('cancel'); break;
      case 'path': setPickerPath(target.dataset.path); break;
      case 'edit-path': {
        const picker = ui.folderPicker;
        if (picker) { picker.editingPath = true; picker.requestedPath = picker.path; renderFolderPicker(); document.querySelector('#folder-path').focus(); document.querySelector('#folder-path').select(); }
        break;
      }
      case 'up': if (ui.folderPicker && ui.folderPicker.path !== '/') setPickerPath(parentDirectory(ui.folderPicker.path)); break;
    }
    return;
  }
  if (target.dataset.file) { ui.selectedFile = ui.selectedFile === target.dataset.file ? null : target.dataset.file; render(false); return; }
  if (target.dataset.candidate) { selectCandidate(target.dataset.candidate); return; }
  if (target.dataset.inspect || target.dataset.evidence) {
    ui.selectedTarget = target.dataset.inspect || target.dataset.evidence; ui.selectedFile = null; ui.inspector = target.dataset.evidence ? 'evidence' : 'tags'; render(false); document.querySelector('#match-inspector').scrollIntoView({ block: 'start' }); return;
  }
  if (target.dataset.inspector) { ui.inspector = target.dataset.inspector; render(false); return; }
  if (target.dataset.scope) {
    ui.tagScope = target.dataset.scope;
    if (route()[0] === 'tags') { location.hash = `tags/${route()[1]}/${ui.tagScope}`; } else render(false);
    return;
  }
  if (target.dataset.libraryView) { ui.libraryView = target.dataset.libraryView; render(false); document.querySelector(`[data-library-view="${ui.libraryView}"]`).focus(); return; }
  if (target.dataset.action === 'clear-library-selection') { ui.librarySelection.clear(); ui.libraryParentSelection.clear(); ui.excludedLibraryTracks.clear(); render(false); return; }
  switch (target.dataset.action) {
    case 'preview-bulk': ui.contextPreviewTrackId = null; location.hash = 'publication-preview'; break;
    case 'remove-root': removeSourceRoot(target.dataset.root); break;
    case 'recover-publication': {
      const context = findLocalTrack(target.dataset.track);
      if (!context) break;
      confirm('Зарегистрировать восстановленный результат?', `<p>Известный зафиксированный путь: <code>${esc(recoveryFixture.targetPath)}</code></p><p>Проверки демо-сценария: теги ${recoveryFixture.tagsMatch ? 'совпадают' : 'не совпадают'}, технические параметры ${recoveryFixture.technicalMatch ? 'совпадают' : 'не совпадают'}, актуальность разрешения ${recoveryFixture.authorityCurrent ? 'актуальна' : 'изменилась'}. Доказательство совпадения исходника: не требуется.</p><p>Если любая обязательная проверка не пройдена, результат не регистрируют и выполняют обычный полный повтор.</p>${note('Демо не читает реальный файл и не подтверждает гарантии работы с файловой системой и безопасности.')}`, () => { if (!recoveryFixture.tagsMatch || !recoveryFixture.technicalMatch || !recoveryFixture.authorityCurrent || !context.track.published) { notify('Проверка восстановления не пройдена; требуется обычный повтор.'); return; } context.track.metadataBaseline = structuredClone(context.track.published.tags); context.track.metadataBaselineExtras = { coverHash: context.track.published.coverHash || null, lyrics: context.track.published.lyrics || null }; context.track.recovered = true; notify('Демо: текущая публикация зарегистрирована после восстановления; снимок метаданных заменён без сохранения истории.'); render(false); }); break;
    }
    case 'explicit-republish': {
      const context = findLocalTrack(target.dataset.track);
      if (!context) break;
      const file = demoFiles[context.track.sourceId];
      if (!file) { notify('Для явной перепубликации нужно выбрать исходник. Альтернативный вариант автоматически не выбирается.'); break; }
      confirm('Явно перепубликовать из выбранного исходника?', `<p>${esc(context.track.title)} · ${esc(file.path)} · ${file.codec}</p><p>Полная подготовка и замена выполняются, даже если данные не изменились. Это отдельное действие, а не обычный повтор.</p>${context.track.published?.codec === 'FLAC' && file.codec === 'MP3' ? '<label class="inline-check"><input type="checkbox" id="confirm-downgrade" required>Понижение качества подтверждаю</label>' : ''}${note('Демонстрация: файловая подготовка и запись не выполняются.')}`, () => { if (context.track.published?.codec === 'FLAC' && file.codec === 'MP3' && !document.querySelector('#confirm-downgrade')?.checked) { notify('Понижение отменено: требуется явное подтверждение.'); return; } const { release, track } = context; track.published = { ...(track.published || {}), tags: structuredClone(providerTags(release, track)), coverHash: track.coverHash || null, lyrics: track.lyrics || null, codec: file.codec, container: 'MKA', sourceId: file.id, readAt: 'Только что · явная перепубликация демо', path: track.published?.path || `/srv/music/${artistName(release.artistId)}/${release.year} - ${release.title}/${String(track.position).padStart(2, '0')} - ${track.title}.mka` }; track.metadataBaseline = structuredClone(track.published.tags); track.metadataBaselineExtras = { coverHash: track.published.coverHash, lyrics: track.published.lyrics }; track.observedFile = null; notify('Демо: явная перепубликация выполнена; текущее состояние и снимок метаданных заменены.'); render(false); }); break;
    }
    case 'refresh-provider-track': {
      const context = findLocalTrack(target.dataset.track);
      if (context) { context.track.providerTitle = `${context.track.title} · кэш провайдера обновлён`; context.track.providerCacheVersion = (context.track.providerCacheVersion || 1) + 1; notify('Кэш провайдера обновлён отдельно. Публикация и снимок метаданных не изменились.'); render(false); }
      break;
    }
    case 'quality-upgrade-demo': {
      const context = findLocalTrack(target.dataset.track);
      const file = context && demoFiles[context.track.sourceId];
      if (!context || !file || context.track.published?.codec !== 'MP3' || file.codec !== 'FLAC') { notify('Демо улучшения качества доступно только при выбранном FLAC-исходнике и текущей публикации MP3.'); break; }
      const currentTags = structuredClone(context.track.published.tags);
      const currentBaseline = structuredClone(context.track.metadataBaseline || currentTags);
      const currentBaselineExtras = structuredClone(context.track.metadataBaselineExtras || { coverHash: context.track.published.coverHash || null, lyrics: context.track.published.lyrics || null });
      confirm('Улучшить только аудио?', `<p>Выбранный исходник: ${esc(file.path)} · ${file.codec}. Метаданные, встроенная обложка и текст песни текущей публикации сохраняются без изменений.</p>${note('Это демонстрация изменения только аудио; файл-исходник не меняется.')}`, () => { context.track.published = { ...context.track.published, codec: file.codec, sourceId: file.id, tags: currentTags, readAt: 'Только что · демо изменения качества аудио' }; context.track.metadataBaseline = currentBaseline; context.track.metadataBaselineExtras = currentBaselineExtras; notify('Демо: изменилось только аудио. Теги, обложка, текст песни и прежний снимок метаданных публикации сохранены.'); render(false); }); break;
    }
    case 'refresh-provider-cache': notify('Обновление кэша провайдера меняет только кэш; файлы публикации и снимок метаданных не меняются.'); break;
    case 'apply-bulk-metadata': {
       if (ui.bulkPreviewVersion !== ui.inventoryVersion || ui.bulkPreviewScope !== bulkPreviewScopeKey()) { notify('Предпросмотр устарел: изменился инвентарь или выбранный объект. Создайте новый.'); render(false); break; }
       const selected = bulkPreviewTargets();
      const unique = [...new Map(selected.map(item => [item.track.id, item])).values()];
      const applicable = unique.filter(({ release, track }) => track.published ? metadataChanged(track, release) : Boolean(demoFiles[track.sourceId]));
      if (!applicable.length) { notify('Нет опубликованных позиций с изменившимися метаданными.'); break; }
      const applicableIds = new Set(applicable.map(({ track }) => track.id));
      const rows = (ui.bulkRows || applicable.map(({ release, track }) => ({ release, track, action: track.published ? 'Обновить' : 'Создать', nextTags: providerTags(release, track), destination: publicationDestination(release, track) }))).filter(row => applicableIds.has(row.track.id));
       const previewScope = ui.bulkPreviewScope;
       confirm(`Применить изменения для ${russianCount(rows.length, 'позиции', 'позиции', 'позиций')}?`, `<p>Для новых публикаций используется только выбранный исходник. У уже опубликованных позиций изменятся только метаданные: звук и качество останутся прежними. Даже если выбран более качественный исходник, это действие его не применяет. Изменение качества доступно отдельным действием на странице позиции. Защищённые от перезаписи вручную заданные поля сохраняются.</p>${table(['Позиция', 'Новый TITLE', 'Путь публикации / формат'], rows.map(({ release, track, destination }) => [`${release.title} · ${track.title}`, showTag(providerTags(release, track).TITLE), `${esc(destination.path)}<span class="sub">${destination.container}</span>`]))}${note('После успешного применения текущее состояние становится новым снимком метаданных; предыдущий снимок не сохраняется.')}`, () => { if (ui.bulkPreviewVersion !== ui.inventoryVersion || previewScope !== bulkPreviewScopeKey()) { notify('Предпросмотр устарел: изменился инвентарь или выбранный объект; действие не выполнено.'); render(false); return; } for (const { release, track, destination } of rows) { const tags = providerTags(release, track); if (!track.published) { const file = demoFiles[track.sourceId]; if (!file?.available || !file.locations?.length) continue; track.published = { tags: structuredClone(tags), coverHash: track.coverHash || null, lyrics: track.lyrics || null, codec: file.codec, container: destination.container, sourceId: file.id, readAt: 'Только что · массовое обновление демо', path: destination.path }; } else { track.published.tags = structuredClone(tags); track.published.coverHash = track.coverHash || null; track.published.lyrics = track.lyrics || null; } track.metadataBaseline = structuredClone(track.published.tags); track.metadataBaselineExtras = { coverHash: track.published.coverHash || null, lyrics: track.published.lyrics || null }; } notify(`Демо: текущая публикация обновлена у ${rows.length} позиций; снимок метаданных заменён.`); render(false); }); break;
    }
    case 'run-auto': {
      let count = 0;
      for (const g of incomingGroups) {
        const assignments = draft(g);
        const targetRelease = currentTarget(g);
        if (!g.autoEligible || g.processed || g.mode !== 'provider' || targetRelease.tracks.length !== g.fileIds.length || !targetRelease.tracks.every(track => assignmentConfidence(assignments[track.id]) === 'high' && assignments[track.id]?.included && assignmentMethod(assignments[track.id]).startsWith('Авто'))) continue;
        count += applyPlan(makeGroupPlan(g), true); g.processed = true;
      }
      ui.notice = count ? `Демо: автоматически опубликовано ${count} позиций Small Hours. Неполные группы остались в ручном разборе. Файловые операции не выполнялись.` : 'Нет новых полностью уверенных релизов. Автопубликация не повторяет и не понижает качество.';
      render(false); break;
    }
    case 'local-mode': group().mode = 'local'; ui.selectedTarget = null; ui.selectedFile = null; ui.recordingTarget = null; render(false); break;
    case 'unassign-source': {
      const assignments = draft();
      const assignment = assignments[target.dataset.target];
      if (!assignment) break;
      assignment.sourceIds = associatedSourceIds(assignment).filter(id => id !== target.dataset.source);
      if (selectedSourceId(assignment) === target.dataset.source) assignment.selectedSourceId = null;
      assignment.method = assignmentMethod(assignment);
      assignment.confidence = assignmentConfidence(assignment);
      if (!assignment.sourceIds.length) delete assignments[target.dataset.target];
      ui.notice = 'Связь снята в черновике. Выбор другого источника или публикации не выполняется.';
      render(false); break;
    }
    case 'assign-selected': if (ui.selectedFile) assignFile(ui.selectedFile, target.dataset.target); break;
    case 'find-recording': ui.recordingTarget = target.dataset.target; ui.recordingQuery = currentTarget().tracks.find(track => track.id === ui.recordingTarget).title; ui.recordingResults = null; render(false); document.querySelector('#recording-query').focus(); break;
    case 'close-recording': ui.recordingTarget = null; render(false); break;
    case 'bind-recording': {
      const assignment = draft()[ui.recordingTarget];
      if (!assignment) { notify('Сначала назначьте аудиофайл этой локальной позиции.'); break; }
      assignment.providerTrackId = target.dataset.recording; assignment.method = 'Вручную · композиция';
      const selected = selectedSourceId(assignment);
      if (selected && assignment.sourceMeta?.[selected]) assignment.sourceMeta[selected].method = assignment.method;
      ui.selectedTarget = ui.recordingTarget; ui.recordingTarget = null;
      ui.notice = 'Связь с композицией добавлена в черновик. Название и порядок локального сборника сохранены.';
      render(false); break;
    }
    case 'preview-group': ui.plan = makeGroupPlan(); location.hash = 'publications'; break;
    case 'preview-release': ui.plan = makeReleasePlan(localRelease(target.dataset.release)); location.hash = 'publications'; break;
    case 'preview-track': { const context = findLocalTrack(target.dataset.track); ui.plan = makeReleasePlan(context.release, context.track.id); location.hash = 'publications'; break; }
    case 'preview-track-metadata': ui.contextPreviewTrackId = target.dataset.track; location.hash = 'publication-preview'; break;
    case 'publish-plan': {
      if (ui.plan?.inventoryVersion !== ui.inventoryVersion) { notify('Инвентарь источников изменился. Создайте новый предпросмотр.'); render(false); break; }
      const rows = planRows(ui.plan).filter(row => row.assignment?.included && row.file && row.action !== 'Без изменений');
      const links = pendingSourceAssociations(ui.plan);
      if (!rows.length && !links.length) break;
      const downgrade = rows.some(row => row.downgrade);
      const title = rows.length ? `Опубликовать ${rows.length} позиций?` : `Сохранить связи ${links.length} позиций?`;
      confirm(title, `<p>${esc(ui.plan.target.title)}. ${rows.length ? 'Будут изменены только выбранные строки плана. Остальные позиции и существующие файлы сохраняются.' : 'Аудиофайлы и текущие публикации не изменяются.'}</p>${links.length ? `<p>${links.length} позиций получат новые связи с источниками; выбранный источник и текущая публикация сохраняются.</p>` : ''}${downgrade ? '<label class="inline-check"><input type="checkbox" id="confirm-downgrade" required>Подтверждаю замену сжатия без потерь на сжатие с потерями</label>' : ''}<p>Неразмеченные файлы останутся во входящих. Все исходники сохраняются.</p>${note('Демонстрация: запись аудиофайлов не выполняется.')}`, () => {
        if (ui.plan?.inventoryVersion !== ui.inventoryVersion) { notify('Предпросмотр устарел из-за изменения инвентаря; публикация не подтверждена.'); render(false); return; }
        const linkCount = pendingSourceAssociations(ui.plan).length;
        const count = applyPlan(ui.plan);
        ui.notice = count ? `Демо: ${count} позиций опубликовано.${linkCount ? ` Связи ${linkCount} позиций сохранены.` : ''} Невыбранные позиции и неразмеченные файлы не изменены.` : `Демо: связи ${linkCount} позиций сохранены. Аудиофайлы и текущие публикации не изменены.`;
        render(false);
      }); break;
    }
    case 'edit-title': {
      const { track } = findLocalTrack(target.dataset.track);
      confirm('Изменить только локальные теги', `<label class="field" for="local-title">Название позиции<input id="local-title" value="${esc(track.title)}" required></label><p>Исходник, кэш провайдера и текущая публикация останутся прежними.</p>`, () => { track.title = document.querySelector('#local-title').value.trim() || track.title; ui.notice = 'Локальное название изменено. Сравните с текущей публикацией; её теги ещё прежние.'; render(false); }); break;
    }
    case 'delete-library': requestLibraryDeletion(target.dataset.kind, target.dataset.id); break;
    case 'source-scan': notify('Демо: сканирование поставлено в очередь. Рабочая папка не требуется для перечисления файлов; копирование и анализ выполняются отдельно. Реальные каталоги не читались.'); break;
    case 'process-source': {
      if (target.dataset.mode === 'staged' && !ui.sourceWorkDirectory) { notify('Демо: обработка через рабочую копию не начата — сначала задайте общую рабочую папку в Настройках. Источники и файлы не читались.'); break; }
      notify(`Демо: предпосылка выбора рабочей папки для ${target.dataset.path} выполнена. Реальные файлы, доступность источника и права не проверялись; копирование и анализ не запускались.`); break;
    }
    case 'check-source': notify('Демо: /mnt/archive недоступен. Управляемые публикации сохранены.'); break;
    case 'source-add': ui.sourceAddOpen = true; ui.sourceName = ''; ui.sourcePath = '/mnt/music'; ui.sourceMode = ''; render(false); document.querySelector('#source-name').focus(); break;
    case 'pick-source-path': openFolderPicker('source', ui.sourcePath); break;
    case 'cancel-source': ui.sourceAddOpen = false; render(false); break;
    case 'save-source': {
      const name = document.querySelector('#source-name').value.trim();
      if (!name) { document.querySelector('#source-name').setAttribute('aria-invalid', 'true'); notify('Укажите имя источника.'); break; }
      if (!ui.sourceMode) { notify('Явно выберите режим обработки: «На месте» или «Через рабочую копию».'); document.querySelector('input[name="source-mode"]')?.focus(); break; }
      const roots = ui.sourceRoots || [
        ['Входящие', '/mnt/inbox', 'staged', 'Только чтение', badge('Доступен', 'success'), '26.09.2026 10:20', link('review', 'Разобрать группы', 'button')],
        ['Архивный диск', '/mnt/archive', 'in_place', 'Только чтение', badge('Недоступен', 'warning'), '25.09.2026 18:40', button('check-source', 'Проверить')]
      ];
      roots.push([name, ui.sourcePath, ui.sourceMode, 'Только чтение', badge('Демо · не проверен'), '—', badge('Требует проверки', 'warning')]);
      ui.sourceRoots = roots;
      ui.sourceAddOpen = false;
      ui.notice = `Демо: источник добавлен только в макет, режим «${ui.sourceMode === 'staged' ? 'Через рабочую копию' : 'На месте'}». Серверный каталог не открывался и не сканировался; рабочая папка требуется только перед обработкой.`;
      ui.sourceMode = '';
      render(false);
      break;
    }
    case 'pick-tools-path': openFolderPicker('tools', ui.toolsPath); break;
    case 'pick-source-work-path': openFolderPicker('source-work', ui.sourceWorkDirectory || undefined); break;
    case 'pick-output-path': openFolderPicker('output', ui.outputCandidate || '/srv/music'); break;
    case 'confirm-output-reset': {
      const candidatePath = ui.outputCandidate;
      if (!candidatePath || candidatePath === '/srv/music') {
        notify('Выберите путь, отличный от текущего каталога публикации.');
        break;
      }
      if (['running', 'claimed'].includes(ui.outputDemoExecution)) {
        notify('Смена каталога публикации заблокирована: выполняется или захвачена операция.');
        render(false);
        break;
      }
      confirm('Подтвердить смену каталога публикации?', `<p><strong>/srv/music → ${esc(candidatePath)}</strong></p><p>Все очереди и задания будут сброшены. Ожидающие задачи не блокируют смену.</p><p>Все ссылки БД на прежний каталог публикации, включая строки публикаций и ссылки на размещённые в нём служебные файлы, будут удалены. Физические файлы в старом каталоге не удаляются. Автоматической повторной публикации не будет.</p><p>Инвентарь источников, сущности библиотеки, связи с источниками и анализы сохраняются.</p><p>Общая рабочая папка рабочих копий анализа источников и её рабочие копии аудио сохраняются вместе с учётом в БД (принадлежность, соответствие источнику и пригодность) и незавершёнными запрошенными шагами анализа. Сброшенная обработка не возобновляется автоматически ни вследствие сброса, ни после последующего перезапуска; явный запуск может переиспользовать пригодную копию.</p>${note('Подтверждение только демонстрирует взаимодействие. Текущий каталог публикации, очереди, БД, файлы и демонстрационные публикации останутся без изменений.')}`, () => {
        if (['running', 'claimed'].includes(ui.outputDemoExecution)) {
          notify('Подтверждение устарело: состояние изменилось на выполняющуюся или захваченную операцию. Смена не выполнена.');
          render(false);
          return;
        }
        if (ui.outputCandidate !== candidatePath) {
          notify('Подтверждение устарело: демонстрационные параметры изменились. Смена не выполнена.');
          render(false);
          return;
        }
        notify('В макете текущий каталог публикации, очереди и демонстрационные публикации не изменены; реального сброса нет.');
        render(false);
      });
      break;
    }
    case 'folder-cancel': folderDialog.close('cancel'); break;
    case 'folder-choose': {
      const picker = ui.folderPicker;
      if (!picker) break;
      const returnAction = picker.target === 'tools' ? 'pick-tools-path' : picker.target === 'source-work' ? 'pick-source-work-path' : picker.target === 'output' ? 'pick-output-path' : 'pick-source-path';
      const chosenPath = picker.path;
      if (picker.target === 'tools') { ui.toolsPath = chosenPath; ui.pathChecked = false; ui.notice = 'Демо: выбран серверный каталог. Проверка прав и конфигурации не выполнялась.'; }
      else if (picker.target === 'source-work') { ui.sourceWorkDirectory = chosenPath; ui.notice = 'Демо: общая рабочая папка выбрана и сохранена только в памяти макета. Использование и файлы не проверялись.'; }
      else if (picker.target === 'output') { ui.outputCandidate = chosenPath; ui.notice = `Демо: выбран новый каталог публикации ${chosenPath}. Текущий путь не изменён; серверный каталог не проверялся.`; }
      else { ui.sourcePath = chosenPath; ui.notice = 'Демо: путь источника выбран. Реальный каталог не проверялся.'; }
      folderDialog.close('choose');
      render(false);
      document.querySelector(`[data-action="${returnAction}"]`)?.focus({ preventScroll: true });
      break;
    }
    case 'retry': notify('Демо: обычный повтор повторяет полную подготовку после восстановления прав. Он не является принудительной перепубликацией.'); break;
    case 'check-updates': ui.toolsChecked = true; ui.notice = 'Демо: есть обновление FFmpeg/ffprobe. Оно не установлено автоматически.'; render(false); break;
    case 'install-tools': confirm('Установить обновление инструментов?', '<p>Скачать из доверенного HTTPS-источника; проверить опубликованный SHA-256, если он доступен; запустить <code>-version</code> у каждого исполняемого файла. Подписи и дополнительные политики здесь не предполагаются. Действие демонстрационное.</p>', () => { ui.toolsUpdated = true; render(false); }); break;
    case 'rollback': confirm('Откатить инструменты к 8.0?', '<p>Будет выбрана предыдущая проверенная версия. Действие демонстрационное.</p>', () => { ui.toolsUpdated = false; render(false); }); break;
    case 'check-path': ui.toolsPath = document.querySelector('#tools-path').value.trim(); ui.pathChecked = ui.toolsPath.startsWith('/') && ui.toolsPath.length > 1; document.querySelector('#tools-path').setAttribute('aria-invalid', String(!ui.pathChecked)); document.querySelector('[data-action="setup-next"]').disabled = !ui.pathChecked; notify(ui.pathChecked ? 'Абсолютный путь принят в демонстрации. Права и постоянный том не проверялись.' : 'Введите абсолютный путь, например /var/lib/melotrove/tools.'); break;
    case 'setup-back': ui.setupStep--; render(false); break;
    case 'setup-next': if (ui.setupStep === 3) location.hash = 'review'; else { ui.setupStep++; render(false); } break;
    case 'sample': notify('Состояние компонента. Доменные данные не изменены.'); break;
  }
});

document.addEventListener('click', event => {
  if (!(event.target instanceof Element)) return;
  if (event.target.closest('.artist-genre-dialog [data-genre-add], .artist-genre-dialog [data-genre-remove]')) {
    const form = event.target.closest('[data-artist-form]');
    if (form) form.dataset.genresDirty = 'true';
  }
}, true);

document.addEventListener('genre-library-change', () => {
  if (['settings', 'setup'].includes(route()[0])) render(false);
  if (route()[0] === 'artist') {
    const artist = demoArtist(route()[1]);
    const panel = document.querySelector('[data-artist-metadata-panel]');
    const dialog = panel?.querySelector('[data-artist-genre-dialog]');
    if (artist && panel && dialog?.open) {
      const form = panel.querySelector('[data-artist-form]');
      const ids = form.dataset.genresDirty === 'true'
        ? window.GenreLibraryPrototype.selectedIds(form, 'genreIds')
        : artistGenreIds(artist);
      panel.querySelector('[data-artist-genre-picker]').innerHTML = window.GenreLibraryPrototype.renderPicker(ids, 'genreIds');
    }
  }
});

document.addEventListener('close', event => {
  if (!(event.target instanceof HTMLDialogElement) || !event.target.matches('[data-artist-genre-dialog]')) return;
  const panel = event.target.closest('[data-artist-metadata-panel]');
  const trigger = panel?.querySelector('[data-artist-genres]');
  trigger?.setAttribute('aria-expanded', 'false');
  trigger?.focus({ preventScroll: true });
}, true);

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return;
  const panel = document.querySelector('[data-artist-metadata-panel].editing');
  if (!panel) return;
  event.preventDefault();
  const artist = demoArtist(route()[1]);
  if (!artist) return;
  panel.querySelector('[data-artist-genre-dialog]').close();
  resetArtistMetadataForm(panel, artist);
  setArtistMetadataMode(panel, false);
  panel.querySelector('[data-artist-metadata="main"]').focus({ preventScroll: true });
});

document.addEventListener('submit', event => {
  if (!(event.target instanceof HTMLFormElement)) return;
  if (event.target.matches('[data-artist-form]')) {
    event.preventDefault();
    event.target.closest('[data-artist-metadata-panel]')?.querySelector('[data-artist-metadata="main"][data-mode="save"]')?.click();
    return;
  }
  if (event.target.id === 'folder-path-form') {
    event.preventDefault();
    const picker = ui.folderPicker;
    if (!picker) return;
    const requestedPath = document.querySelector('#folder-path').value.trim().replace(/\/+$/, '') || '/';
    picker.requestedPath = requestedPath;
    if (!requestedPath.startsWith('/')) picker.error = 'Путь должен быть абсолютным и начинаться с /.';
    else if (!Object.hasOwn(demoDirectories, requestedPath)) picker.error = 'Такой путь отсутствует в локальном демо-дереве.';
    else {
      picker.path = requestedPath;
      picker.error = '';
      picker.requestedPath = '';
      picker.editingPath = false;
    }
    renderFolderPicker();
    if (picker.editingPath) document.querySelector('#folder-path').focus();
    return;
  }
  if (event.target.id === 'provider-search') {
    event.preventDefault(); ui.providerQuery = document.querySelector('#provider-query').value;
    const terms = normalized(ui.providerQuery).split(' ').filter(Boolean);
    ui.providerResults = providerReleases.filter(release => terms.every(term => normalized(`${release.title} ${artistName(release.artistId)} ${release.catalog} ${release.id} ${release.year}`).includes(term)));
    render(false); document.querySelector('#provider-query').focus();
  }
  if (event.target.id === 'recording-search') {
    event.preventDefault(); ui.recordingQuery = document.querySelector('#recording-query').value;
    const terms = normalized(ui.recordingQuery).split(' ').filter(Boolean);
    ui.recordingResults = providerReleases.flatMap(release => release.tracks.map(track => ({ release, track }))).filter(({ release, track }) => terms.every(term => normalized(`${track.title} ${artistName(track.artistId)} ${release.title}`).includes(term)));
    render(false); document.querySelector('#recording-query').focus({ preventScroll: true }); document.querySelector('#recording-search').parentElement.scrollIntoView({ block: 'start' });
  }
});

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || event.target?.id !== 'folder-path' || !ui.folderPicker?.editingPath) return;
  event.preventDefault();
  event.stopPropagation();
  const picker = ui.folderPicker;
  picker.editingPath = false;
  picker.requestedPath = '';
  picker.error = '';
  renderFolderPicker();
  document.querySelector('.folder-current-path').focus();
});

document.addEventListener('change', event => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  if (target.id === 'output-demo-execution') {
    ui.outputDemoExecution = target.value;
    render(false);
    return;
  }
  if (target.dataset.trackSource) {
    const context = findLocalTrack(target.dataset.trackSource);
    if (context && trackSourceIds(context.track).includes(target.value)) {
      context.track.sourceId = target.value;
      ui.notice = 'Выбран источник для следующей публикации. Текущая публикация не изменена.';
      render(false);
    }
  }
  if (target.dataset.selectedSource) {
    const assignment = draft()[target.dataset.selectedSource];
    if (assignment) {
      assignment.selectedSourceId = target.value;
      assignment.method = assignmentMethod(assignment);
      assignment.confidence = assignmentConfidence(assignment);
      render(false);
    }
  }
  if (target.dataset.include) { draft()[target.dataset.include].included = target.checked; render(false); }
  if (target.dataset.planInclude) { ui.plan.assignments[target.dataset.planInclude].included = target.checked; render(false); }
  if (target.name === 'source-mode') { ui.sourceMode = target.value; render(false); }
  if (target.dataset.sourceMode !== undefined) {
    if (!ui.sourceRoots) ui.sourceRoots = [
      ['Входящие', '/mnt/inbox', 'staged', 'Только чтение', badge('Доступен', 'success'), '26.09.2026 10:20', link('review', 'Разобрать группы', 'button')],
      ['Архивный диск', '/mnt/archive', 'in_place', 'Только чтение', badge('Недоступен', 'warning'), '25.09.2026 18:40', button('check-source', 'Проверить')]
    ];
    const root = ui.sourceRoots?.[Number(target.dataset.sourceMode)];
    if (root) { root[2] = target.value; ui.notice = 'Режим обработки изменён только в демо; он применяется к будущей обработке и не меняет политику источника «только чтение».'; render(false); }
  }
  if (target.id === 'root-operation-active') { ui.rootOperationActive = target.checked; }
  if (target.dataset.absentEdition) { ui.absentEdition[target.dataset.absentEdition] = target.value || null; render(false); }
  if (target.dataset.selectionChildren) {
    const kind = target.dataset.selectArtist ? 'artist' : 'release';
    const id = target.dataset.selectArtist || target.dataset.selectRelease;
    setParentSelection(kind, id, target.checked, JSON.parse(target.dataset.selectionChildren));
    render(false);
    return;
  }
  if (target.dataset.selectTrack) {
    setTrackSelection([target.dataset.selectTrack], target.checked);
    render(false);
    return;
  }
  if (target.id === 'differences-only') { ui.differencesOnly = target.checked; render(false); }
  if (target.id === 'publication-filter') { ui.publicationFilter = target.value; document.querySelector('#catalog-content').innerHTML = catalogRows(); syncSelectionControls(document.querySelector('#catalog-content')); }
  if (target.id === 'availability-filter') { ui.availabilityFilter = target.value; document.querySelector('#catalog-content').innerHTML = catalogRows(); syncSelectionControls(document.querySelector('#catalog-content')); }
  if (target.id === 'tag-file') location.hash = `tags/${target.value}/${ui.tagScope}`;
});

document.addEventListener('input', event => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement)) return;
  if (target.id === 'library-search') { ui.libraryQuery = target.value; document.querySelector('#catalog-content').innerHTML = catalogRows(); syncSelectionControls(document.querySelector('#catalog-content')); }
  if (target.id === 'tools-path') { ui.toolsPath = target.value; ui.pathChecked = false; document.querySelector('[data-action="setup-next"]').disabled = true; }
  if (target.id === 'source-path') ui.sourcePath = target.value;
  if (target.id === 'source-name') { ui.sourceName = target.value; target.removeAttribute('aria-invalid'); }
});

document.addEventListener('dragstart', event => {
  const chip = event.target instanceof Element ? event.target.closest('[data-file]') : null;
  if (!chip || !event.dataTransfer || route()[0] !== 'match') return;
  draggedFile = chip.dataset.file; event.dataTransfer.setData('text/plain', `music-file:${draggedFile}`); event.dataTransfer.effectAllowed = 'copyMove'; chip.classList.add('dragging');
});
document.addEventListener('dragover', event => {
  const row = event.target instanceof Element ? event.target.closest('[data-drop]') : null;
  if (row && draggedFile) { event.preventDefault(); row.classList.add('drag-over'); if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'; }
});
document.addEventListener('dragleave', event => {
  const row = event.target instanceof Element ? event.target.closest('[data-drop]') : null;
  if (row && (!(event.relatedTarget instanceof Node) || !row.contains(event.relatedTarget))) row.classList.remove('drag-over');
});
document.addEventListener('drop', event => {
  if (route()[0] !== 'match') return;
  event.preventDefault();
  const row = event.target instanceof Element ? event.target.closest('[data-drop]') : null;
  const value = event.dataTransfer?.getData('text/plain') || '';
  if (row && draggedFile && value === `music-file:${draggedFile}`) assignFile(draggedFile, row.dataset.drop);
  else notify('Назначать можно только известные файлы текущей входной группы. Загрузка внешних файлов не поддерживается.');
  draggedFile = null; document.querySelectorAll('.drag-over,.dragging').forEach(item => item.classList.remove('drag-over', 'dragging'));
});
document.addEventListener('dragend', () => { draggedFile = null; document.querySelectorAll('.drag-over,.dragging').forEach(item => item.classList.remove('drag-over', 'dragging')); });
document.addEventListener('keydown', event => { if (event.key === 'Escape' && !dialog.open && ui.selectedFile) { ui.selectedFile = null; render(false); notify('Выбор файла отменён.'); } });
dialog.addEventListener('close', () => { if (dialog.returnValue === 'confirm' && confirmAction) confirmAction(); confirmAction = null; });
window.addEventListener('hashchange', () => { if (location.hash === '#content') { document.querySelector('main').focus(); return; } ui.notice = ''; render(); });
render(false);
