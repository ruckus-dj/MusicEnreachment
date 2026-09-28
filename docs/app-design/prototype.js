/// <reference path="./catalog-data.js" />
const view = document.querySelector('#view');
const dialog = document.querySelector('#confirmation');
const ui = { groupId: 'north', selectedFile: null, selectedTarget: null, inspector: 'tags', tagScope: 'track', differencesOnly: false, libraryView: 'artists', libraryQuery: '', publicationFilter: 'all', providerQuery: 'Northern Lines', providerResults: null, recordingQuery: '', recordingResults: null, recordingTarget: null, plan: null, notice: '', toolsChecked: false, toolsUpdated: false, setupStep: 0, toolsPath: '/var/lib/music/tools', pathChecked: false };
let confirmAction = null;
let draggedFile = null;
const esc = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
const badge = (text, tone = '') => `<span class="status ${tone}">${esc(text)}</span>`;
const button = (action, text, attrs = '', primary = false) => `<button type="button" data-action="${action}" ${attrs} class="${primary ? 'primary' : ''}">${text}</button>`;
const link = (route, text, className = '') => `<a href="#${route}" class="${className}">${esc(text)}</a>`;
const duration = seconds => `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, '0')}`;
const cover = (release, size = '') => `<span class="cover ${release.color || 'clay'} ${size}" aria-hidden="true">${esc(release.title?.[0] || release.name?.[0] || 'M')}</span>`;
const panel = (title, content, extra = '') => `<section class="panel"><div class="panel-heading"><h2>${title}</h2>${extra}</div>${content}</section>`;
const note = (text, tone = '') => `<div class="note ${tone}">${text}</div>`;
const header = (title, description, actions = '') => `<div class="page-header"><div><h1 tabindex="-1">${esc(title)}</h1><p class="muted">${description}</p></div><div class="actions">${actions}</div></div>`;
const table = (heads, rows, className = '') => `<div class="table-wrap ${className}"><table><thead><tr>${heads.map(head => `<th scope="col">${head}</th>`).join('')}</tr></thead><tbody>${rows.length ? rows.map(row => `<tr>${row.map((cell, i) => `<td data-label="${esc(heads[i].replace(/<[^>]*>/g, ''))}">${cell}</td>`).join('')}</tr>`).join('') : `<tr><td colspan="${heads.length}"><div class="empty">Нет результатов. Измените запрос или фильтр.</div></td></tr>`}</tbody></table></div>`;
const facts = items => `<dl class="facts">${items.map(([key, value]) => `<div><dt>${esc(key)}</dt><dd>${value}</dd></div>`).join('')}</dl>`;
const artistName = id => demoArtist(id)?.name || 'Неизвестный артист';
const allTracks = () => localReleases.flatMap(release => release.tracks.map(track => ({ release, track })));
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

function currentTarget(g = group()) {
  return g.mode === 'local' ? localRelease(g.releaseId) : providerReleases.find(release => release.id === g.candidateId);
}

function draft(g = group()) {
  const target = currentTarget(g);
  const key = g.mode === 'local' ? 'local' : g.candidateId;
  if (!g.drafts[key]) {
    const assignments = {};
    for (const track of target.tracks) {
      const file = g.fileIds.map(id => demoFiles[id]).find(item => g.mode === 'local' ? item.id === track.sourceId : item.providerTrackId === track.id && item.confidence === 'high');
      if (file) assignments[track.id] = { fileId: file.id, method: g.mode === 'local' ? 'Из исходной группы' : 'Авто · тестовый verdict', confidence: file.confidence, providerTrackId: g.mode === 'local' ? track.providerTrackId : track.id, included: true };
    }
    g.drafts[key] = assignments;
  }
  return g.drafts[key];
}

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
  return `<div class="evidence-summary">${badge(assignment?.method?.startsWith('Вручную') ? 'Назначено оператором' : file.confidence === 'high' ? 'Высокая уверенность · fixture' : 'Недостаточная уверенность · fixture', file.confidence === 'high' ? 'success' : 'warning')}<strong>Числовой score: не рассчитан</strong><span class="muted">Формула и веса не утверждены.</span></div>${table(['Признак', 'Исходное значение', 'Кандидат', 'Результат / отличие', 'Вес / вклад'], [
    ['Название', esc(rawTitle), esc(target.title), compare(rawTitle, target.title), 'Не задан'],
    ['Артист', esc(rawArtist), esc(remoteArtist), compare(rawArtist, remoteArtist), 'Не задан'],
    ['Длительность', duration(file.duration), duration(target.duration), badge(`${delta > 0 ? '+' : ''}${delta} с`, delta ? 'warning' : 'success'), 'Не задан'],
    ['Номер позиции', showTag(file.tags.TRACKNUMBER), String(target.position), file.tags.TRACKNUMBER?.[0] === String(target.position) ? 'Совпадает' : 'Отличается / не применим к сборнику', 'Не задан'],
    ['Recording ID', showTag(file.tags.MUSICBRAINZ_TRACKID), esc(target.recordingId || 'Не указан'), 'В исходнике нет MBID; совпадение не подтверждено ID', 'Не задан'],
    ['Fingerprint', esc(file.fingerprint || 'Нет результата'), 'AcoustID / Chromaprint', 'Не используется как доказанное совпадение', 'Не задан']
  ])}<div class="panel-foot">Показано реальное сравнение демонстрационных значений. Нормализация: NFKC, регистр, повторные пробелы. Это не формула matching. Ручное назначение не повышает score автоматически.</div>`;
}

function review() {
  const rows = incomingGroups.map(g => {
    const target = currentTarget(g);
    const assignments = draft(g);
    const used = new Set(Object.values(assignments).map(item => item.fileId));
    const published = targetLocalRelease(g, target)?.tracks.filter(track => track.published).length || 0;
    return [`<div class="entity">${cover(localRelease(g.releaseId))}<div>${link(`match/${g.id}`, g.title)}<span class="sub">${artistName(g.artistId)} · <span class="mono">/mnt/inbox/${g.id}</span></span></div></div>`, `${g.fileIds.length} файлов`, `${Object.keys(assignments).length} / ${target.tracks.length}`, `${g.fileIds.length - used.size}`, badge(g.processed ? 'Опубликовано автоматически' : g.autoEligible ? 'Готов к автоматике' : 'Ручная обработка', g.processed || g.autoEligible ? 'success' : 'warning'), `${published} / ${target.tracks.length}`, `<span class="small">${esc(g.reason)}</span>`, link(`match/${g.id}`, 'Открыть', 'button')];
  });
  return header('Входящие альбомы', 'Группы из исходных тегов → поиск издания → назначения → публикация. Неполные данные допустимы.', button('run-auto', 'Запустить обработку · демо', '', true)) +
    `<div class="flow-strip"><span><b>1</b> Сгруппировать файлы</span><span><b>2</b> Сопоставить</span><span><b>3</b> Все позиции уверенные → автоматически</span><span><b>4</b> Иначе → ручной разбор, в том числе частичный</span></div>` +
    panel('Текущие группы', table(['Альбом / папка', 'Входящие', 'Назначено', 'Свободно', 'Режим', 'Опубликовано', 'Причина', ''], rows)) +
    `<div class="two-columns">${panel('Как пройти ручной сценарий', '<ol class="compact-list"><li>Откройте Northern Lines: 7 уверенных назначений уже заполнены.</li><li>Перетащите 03 — Northbound (live?) на позицию 03. Проверьте отличие длительности.</li><li>Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.</li><li>Предпросмотр → подтвердить публикацию выбранных позиций.</li></ol>')}${panel('Сборник, которого нет у провайдера', `<p class="padded">Road Notes сохраняет собственное название и порядок. Для каждой позиции можно найти recording в другом альбоме. Альбом провайдера не становится названием сборника.</p><div class="panel-foot">${link('match/mix', 'Открыть локальный сборник', 'button')}</div>`)}</div>`;
}

function candidateList() {
  const g = group();
  const candidates = ui.providerResults || providerReleases.filter(release => g.mode === 'provider' && release.title.toLowerCase().includes(currentTarget(g).title.toLowerCase()));
  return `<form id="provider-search" class="search-form"><label for="provider-query">Поиск releases · текстом</label><div class="input-action"><input id="provider-query" name="query" value="${esc(ui.providerQuery)}" placeholder="Артист, альбом, каталог…"><button type="submit">Найти</button></div></form><p class="small muted inset">Локальные fixtures MusicBrainz, не реальный API.</p><div class="candidate-list">${candidates.length ? candidates.map(release => `<button class="candidate ${g.mode === 'provider' && g.candidateId === release.id ? 'selected' : ''}" data-candidate="${release.id}" aria-pressed="${g.mode === 'provider' && g.candidateId === release.id}"><span class="entity">${cover(release)}<span><strong>${esc(release.title)}</strong><span class="sub">${artistName(release.artistId)} · ${release.year} · ${release.country}</span></span></span><span class="sub">${release.medium} · ${release.tracks.length} позиций · ${esc(release.catalog)}</span><span class="mono">${release.id}</span></button>`).join('') : '<div class="empty">Ничего не найдено. Попробуйте название композиции в локальном режиме.</div>'}</div><div class="panel-foot">${button('local-mode', 'Локальный релиз / сборник', `aria-pressed="${g.mode === 'local'}"`)}</div>`;
}

function fileChip(file, compact = false) {
  return `<button class="file-chip ${ui.selectedFile === file.id ? 'selected' : ''}" draggable="true" data-file="${file.id}" aria-pressed="${ui.selectedFile === file.id}" title="Выбрать или перетащить файл"><span class="grip" aria-hidden="true">⠿</span><span><strong>${esc(file.name)}</strong><span class="sub">${file.codec} · ${duration(file.duration)}${compact ? '' : ` · ${file.codec === 'FLAC' ? '24 bit / 48 kHz' : '320 kbps'} · ${file.size}`}</span></span></button>`;
}

function match() {
  const g = group();
  const target = currentTarget(g);
  const assignments = draft(g);
  if (!target.tracks.some(track => track.id === ui.selectedTarget)) ui.selectedTarget = target.tracks[0].id;
  const used = new Set(Object.values(assignments).map(item => item.fileId));
  const free = g.fileIds.filter(id => !used.has(id));
  const included = Object.values(assignments).filter(item => item.included).length;
  const rows = target.tracks.map(track => {
    const assignment = assignments[track.id];
    const file = assignment && demoFiles[assignment.fileId];
    const remote = g.mode === 'local' ? findProviderTrack(assignment?.providerTrackId) : { release: target, track };
    return `<tr class="drop-row ${track.id === ui.selectedTarget ? 'inspected' : ''} ${file ? 'assigned' : 'unassigned'}" data-drop="${track.id}"><td data-label="Выбор"><input type="checkbox" data-include="${track.id}" aria-label="Публиковать ${esc(track.title)}" ${assignment?.included ? 'checked' : ''} ${!assignment ? 'disabled' : ''}></td><td data-label="№" class="mono">${String(track.position).padStart(2, '0')}</td><td data-label="Композиция"><button class="text-button" data-inspect="${track.id}">${esc(track.title)}</button><span class="sub">${artistName(track.artistId)}${g.mode === 'local' ? ` · ${remote ? esc(remote.release.title) : 'Без provider-связи'}` : ''}</span></td><td data-label="Длительность" class="mono">${duration(track.duration)}</td><td data-label="Наш файл">${file ? `<div class="assigned-file">${fileChip(file, true)}${button('unassign', '×', `data-target="${track.id}" aria-label="Снять назначение ${esc(track.title)}"`)}</div>` : '<span class="drop-hint">Перетащите файл сюда</span>'}${ui.selectedFile ? button('assign-selected', file ? 'Заменить выбранным' : 'Назначить выбранный', `data-target="${track.id}"`) : ''}</td><td data-label="Score / доказательства">${file ? `<button class="score-button" data-evidence="${track.id}">${badge(assignment.method.startsWith('Вручную') ? 'Вручную' : assignment.confidence === 'high' ? 'Высокий*' : 'Низкий*', assignment.confidence === 'high' ? 'success' : 'warning')}<span class="sub">Детали</span></button>` : badge('Нет файла')}${g.mode === 'local' ? button('find-recording', 'Найти recording', `data-target="${track.id}"`) : ''}</td></tr>`;
  }).join('');
  const inspected = target.tracks.find(track => track.id === ui.selectedTarget);
  const assignment = assignments[inspected.id];
  const file = demoFiles[ui.selectedFile || assignment?.fileId];
  const provider = g.mode === 'local' ? findProviderTrack(assignment?.providerTrackId) : { release: target, track: inspected };
  const existing = targetLocalRelease(g, target);
  const localTrack = existing?.tracks.find(track => track.position === inspected.position);
  const projectedTrack = localTrack || { ...inspected, published: null };
  const inspectorContent = ui.inspector === 'evidence' ? evidence(file, provider?.track || inspected, assignment) : tagMatrix(existing || target, projectedTrack, file, provider, localTrack?.published, 'Артист/альбом: значения выбранного файла');
  return header(`Разбор: ${g.title}`, `${g.mode === 'local' ? 'Локальные позиции; recordings можно брать из разных альбомов.' : 'Выбранное издание → его позиции. Наши файлы находятся отдельно справа.'}`, link('review', 'Все входящие', 'button')) +
    `<div class="inline-summary">${badge(g.mode === 'local' ? 'Сборник без provider-релиза' : `${target.year} · ${target.country} · ${target.catalog}`, 'info')}<span><b>${Object.keys(assignments).length}/${target.tracks.length}</b> назначено</span><span><b>${free.length}</b> файлов свободно</span><span>Только чтение: <code>/mnt/inbox/${g.id}</code></span></div>` +
    `<div class="matching-grid">${panel('Найти издание', candidateList())}<section class="panel target-panel"><div class="panel-heading"><div><h2>${esc(target.title)}</h2><span class="sub">${artistName(target.artistId)} · ${target.tracks.length} позиций · ${g.mode === 'local' ? 'Локальный порядок' : 'Носитель 1'}</span></div>${badge('Черновик назначений')}</div><div class="table-wrap"><table class="mapping-table"><thead><tr><th scope="col">✓</th><th scope="col">№</th><th scope="col">Композиция</th><th scope="col">Длит.</th><th scope="col">Наш файл / drop</th><th scope="col">Score*</th></tr></thead><tbody>${rows}</tbody></table></div><div class="panel-foot">* Уровни уверенности заданы для демонстрации. Числа и веса не выдумываются. Нажмите на уровень, чтобы увидеть различия.</div></section><section class="panel file-pool"><div class="panel-heading"><h2>Наши файлы</h2>${badge(`${free.length} свободно`)}</div><div class="file-list" data-pool="true">${free.length ? free.map(id => fileChip(demoFiles[id])).join('') : '<div class="empty">Все файлы назначены.</div>'}</div><div class="panel-foot">Перетащите на строку слева.<br>Или выберите файл → «Назначить выбранный». Escape снимает выбор.<br><strong>Это не загрузка файлов с компьютера.</strong></div></section></div>` +
    (ui.recordingTarget ? recordingSearch() : '') +
    `<div id="match-inspector">${panel(`Инспектор · ${esc(inspected.title)}`, `<div class="inspector-tabs"><button data-inspector="tags" aria-pressed="${ui.inspector === 'tags'}">Сравнение тегов</button><button data-inspector="evidence" aria-pressed="${ui.inspector === 'evidence'}">Почему такое совпадение</button>${ui.selectedFile ? `<span class="small">Для назначения выбран: ${esc(demoFiles[ui.selectedFile].name)}</span>` : ''}</div>${inspectorContent}`)}</div>` +
    `<div class="commit-bar"><div><strong>${included} позиций выбрано для публикации</strong><span class="sub">${target.tracks.length - Object.keys(assignments).length} пустых позиций не блокируют публикацию. ${free.length} файлов останутся неразмеченными во входящих.</span></div>${button('preview-group', 'Предпросмотр публикации', included ? '' : 'disabled', true)}</div>`;
}

function recordingSearch() {
  const target = currentTarget().tracks.find(track => track.id === ui.recordingTarget);
  const candidates = ui.recordingResults || [];
  return panel(`Поиск recording для «${esc(target.title)}»`, `<form id="recording-search" class="search-form inline"><label for="recording-query">Название / артист</label><input id="recording-query" name="query" value="${esc(ui.recordingQuery)}"><button type="submit">Найти композиции</button>${button('close-recording', 'Закрыть')}</form>${table(['Композиция', 'Артист', 'Найдена в релизе', 'Длительность', ''], candidates.map(({ release, track }) => [esc(track.title), artistName(track.artistId), `${esc(release.title)} · ${release.year}`, duration(track.duration), button('bind-recording', 'Связать recording', `data-recording="${track.id}"`)]))}<div class="panel-foot">Связывается только recording. Название сборника, его год, порядок и локальные позиции не заменяются чужим альбомом.</div>`);
}

function assignFile(fileId, targetId) {
  const g = group();
  if (!g.fileIds.includes(fileId) || !demoFiles[fileId]?.available || !currentTarget().tracks.some(track => track.id === targetId)) return;
  const assignments = draft();
  const previous = Object.entries(assignments).filter(([id, item]) => item.fileId === fileId && id !== targetId);
  const occupied = assignments[targetId];
  if (occupied?.fileId === fileId) return;
  const apply = keepPrevious => {
    if (!keepPrevious) for (const [id] of previous) delete assignments[id];
    assignments[targetId] = { fileId, method: 'Вручную · drag/click', confidence: demoFiles[fileId].confidence, providerTrackId: g.mode === 'local' ? occupied?.providerTrackId || null : targetId, included: true };
    ui.selectedFile = null; ui.selectedTarget = targetId;
    ui.notice = `Назначен ${demoFiles[fileId].name}. Это черновик, публикация не изменена.`;
    render(false);
  };
  if (occupied || previous.length) {
    confirm('Изменить назначение?', `<p>${occupied ? `Файл ${esc(demoFiles[occupied.fileId].name)} будет снят с этой позиции и вернётся в свободные, если больше нигде не назначен.` : 'Файл уже назначен другой позиции.'}</p>${previous.length ? '<label class="inline-check"><input id="keep-previous" type="checkbox">Использовать файл повторно, сохранив прежнее назначение</label><p class="small muted">Без флажка файл будет перенесён. Модель допускает использование одного варианта в нескольких позициях.</p>' : ''}`, () => apply(Boolean(document.querySelector('#keep-previous')?.checked)));
  } else apply(false);
}

function catalogRows() {
  const query = normalized(ui.libraryQuery);
  const publicationMatches = track => ui.publicationFilter === 'all' || (ui.publicationFilter === 'published' ? Boolean(track.published) : !track.published);
  if (ui.libraryView === 'artists') {
    const artists = demoArtists.filter(artist => normalized(`${artist.name} ${artist.genre}`).includes(query) && localReleases.some(release => release.artistId === artist.id && release.tracks.some(publicationMatches)));
    return `<div class="artist-grid">${artists.length ? artists.map(artist => {
      const releases = localReleases.filter(release => release.artistId === artist.id);
      const tracks = releases.flatMap(release => release.tracks);
      return `<a class="artist-card" href="#artist/${artist.id}">${cover(artist, 'medium')}<span><strong>${esc(artist.name)}</strong><span class="sub">${releases.length} релизов · ${tracks.length} позиций</span><span class="sub">${tracks.filter(track => track.published).length} опубликовано · ${esc(artist.genre)}</span></span></a>`;
    }).join('') : '<div class="empty">Артистов не найдено.</div>'}</div>`;
  }
  if (ui.libraryView === 'albums') {
    const releases = localReleases.filter(release => normalized(`${release.title} ${artistName(release.artistId)} ${release.catalog}`).includes(query) && release.tracks.some(publicationMatches));
    return table(['Альбом', 'Артист', 'Год / издание', 'Позиции', 'Аудио', 'Публикация', 'Провайдер'], releases.map(release => [`<div class="entity">${cover(release)}${link(`release/${release.id}`, release.title)}</div>`, link(`artist/${release.artistId}`, artistName(release.artistId)), `${release.year} · ${release.country || 'Локальный'}<span class="sub">${esc(release.catalog)}</span>`, release.tracks.length, `${release.tracks.filter(track => track.sourceId).length} доступно`, `${release.tracks.filter(track => track.published).length} / ${release.tracks.length}`, release.providerId ? badge('MusicBrainz', 'success') : badge('Локальный сборник')]));
  }
  const tracks = allTracks().filter(({ release, track }) => normalized(`${track.title} ${artistName(track.artistId)} ${release.title}`).includes(query) && publicationMatches(track));
  return trackTable(tracks, true);
}

function library() {
  const all = allTracks();
  return header('Медиатека', `${demoArtists.length} артистов · ${localReleases.length} релизов · ${all.length} позиций · ${all.filter(({ track }) => track.published).length} опубликовано`, link('review', 'Разобрать входящие', 'button')) +
    `<div class="catalog-toolbar"><div class="segments" aria-label="Вид медиатеки">${[['artists', 'Артисты'], ['albums', 'Все альбомы'], ['tracks', 'Все треки']].map(([id, title]) => `<button data-library-view="${id}" aria-pressed="${ui.libraryView === id}">${title}</button>`).join('')}</div><label class="search-label"><span class="sr-only">Поиск в медиатеке</span><input type="search" id="library-search" value="${esc(ui.libraryQuery)}" placeholder="Артист, альбом, трек…"></label><label class="inline-field">Публикация<select id="publication-filter"><option value="all">Все</option><option value="published" ${ui.publicationFilter === 'published' ? 'selected' : ''}>Опубликованные</option><option value="unpublished" ${ui.publicationFilter === 'unpublished' ? 'selected' : ''}>Не опубликованные</option></select></label></div><div id="catalog-content">${catalogRows()}</div>`;
}

function trackTable(items, withAlbum = false) {
  const heads = ['№', 'Композиция', ...(withAlbum ? ['Альбом / артист'] : []), 'Длит.', 'Источник', 'Параметры', 'Файл', 'Публикация', ''];
  return table(heads, items.map(({ release, track }) => {
    const file = demoFiles[track.sourceId];
    return [String(track.position).padStart(2, '0'), `${link(`track/${track.id}`, track.title)}<span class="sub">${artistName(track.artistId)}</span>`, ...(withAlbum ? [`${link(`release/${release.id}`, release.title)}<span class="sub">${artistName(release.artistId)} · ${release.year}</span>`] : []), `<span class="mono">${duration(track.duration)}</span>`, file ? badge(file.codec, file.codec === 'FLAC' ? 'success' : '') : badge('Нет файла', 'warning'), file ? `<span class="mono">${file.codec === 'FLAC' ? '24 / 48k' : '320 kbps'}</span>` : '—', `<span class="filename">${esc(file?.name || '—')}</span>`, track.published ? badge(`${track.published.container} · ${track.published.codec}`, 'success') : badge('Нет'), link(`track/${track.id}`, 'Теги')];
  }));
}

function artistPage(id) {
  const artist = demoArtist(id) || demoArtists[0];
  const releases = localReleases.filter(release => release.artistId === artist.id);
  return header(artist.name, `${releases.length} релизов · ${releases.reduce((sum, release) => sum + release.tracks.length, 0)} позиций · ${artist.genre}`, link('library', 'Вся медиатека', 'button')) +
    releases.map((release, index) => `<details class="album-disclosure" ${index === 0 ? 'open' : ''}><summary><span class="entity">${cover(release)}<span><strong>${esc(release.title)}</strong><span class="sub">${release.year} · ${release.tracks.length} позиций · ${release.label || 'Локальный'} · ${release.catalog}</span></span></span><span>${badge(`${release.tracks.filter(track => track.published).length}/${release.tracks.length} опубликовано`, 'success')}</span></summary><div class="album-actions">${link(`release/${release.id}`, 'Открыть альбом', 'button')}${link(`tags/${release.tracks[0].id}/artist`, 'Сравнить теги артиста', 'button')}${link(`tags/${release.tracks[0].id}/release`, 'Теги альбома', 'button')}</div>${trackTable(release.tracks.map(track => ({ release, track })))}</details>`).join('');
}

function releasePage(id) {
  const release = localRelease(id) || localReleases[0];
  const tracks = release.tracks;
  return `<div class="release-header">${cover(release, 'large')}<div><h1 tabindex="-1">${esc(release.title)}</h1><p>${link(`artist/${release.artistId}`, artistName(release.artistId))} · ${release.year} · ${release.country || 'Локальный сборник'} · ${esc(release.label)} · ${esc(release.catalog)}</p><div class="inline-summary">${badge(release.providerId ? `MusicBrainz · ${release.providerId}` : 'Нет provider-релиза', release.providerId ? 'success' : '')}<span>${tracks.length} позиций</span><span>${tracks.filter(track => track.sourceId).length} источников</span><span>${tracks.filter(track => track.published).length} опубликовано</span></div></div><div class="actions">${button('preview-release', 'План публикации', `data-release="${release.id}"`, true)}${link(`tags/${tracks[0].id}/release`, 'Сравнить теги', 'button')}</div></div>` +
    panel('Треки · носитель 1', trackTable(tracks.map(track => ({ release, track }))), badge(`${duration(tracks.reduce((sum, track) => sum + track.duration, 0))} всего`)) +
    panel('Метаданные: исходник / провайдер / локально / публикация', `<div class="panel-foot">${link(`tags/${tracks[0].id}/release`, 'Открыть подробное сравнение альбома')} · Для опубликованных тегов выбран конкретный файл, а не усреднённое значение.</div>${tagMatrix(release, tracks[0], demoFiles[tracks[0].sourceId], findProviderTrack(tracks[0].providerTrackId), tracks[0].published)}`);
}

function tagsPage(trackId, scope) {
  const context = findLocalTrack(trackId) || allTracks()[0];
  if (tagFields[scope]) ui.tagScope = scope;
  const { release, track } = context;
  const provider = findProviderTrack(track.providerTrackId);
  return header(`Теги: ${track.title}`, 'Артист и альбом в опубликованной колонке — теги выбранного аудиофайла. Несовпадения не скрываются.', link(`release/${release.id}`, 'К альбому', 'button')) +
    panel('Контекст сравнения', `<div class="context-picker"><label for="tag-file">Файл / позиция</label><select id="tag-file">${release.tracks.map(item => `<option value="${item.id}" ${item.id === track.id ? 'selected' : ''}>${String(item.position).padStart(2, '0')} · ${esc(item.title)} · ${item.published ? 'опубликован' : 'без публикации'}</option>`).join('')}</select><span class="small muted">Кэш провайдера: 26.09.2026 10:20 · ID демонстрационные</span></div>${tagMatrix(release, track, demoFiles[track.sourceId], provider, track.published)}`) +
    note('В колонке публикации показан отдельный снимок чтения существующего файла. Изменение локальных тегов не изменяет эту колонку до явной публикации.');
}

function trackPage(id) {
  const { release, track } = findLocalTrack(id) || allTracks()[0];
  const file = demoFiles[track.sourceId];
  return header(`${String(track.position).padStart(2, '0')} · ${track.title}`, `${artistName(track.artistId)} / ${release.title} · позиция релиза, не общий recording`, link(`release/${release.id}`, 'К трек-листу', 'button')) +
    `<div class="two-columns">${panel('Аудиоисточник', facts([['Путь', `<span class="mono">${esc(file?.path || 'Не назначен')}</span>`], ['Формат', file ? `${file.codec} · ${duration(file.duration)} · ${file.size}` : '—'], ['Recording', esc(track.recordingId || 'Без provider-связи')], ['Фактическая публикация', track.published ? `${track.published.container} · ${track.published.codec} · ${esc(track.published.readAt)}` : 'Не опубликован']]))}${panel('Раздельные состояния', `<div class="padded"><p>Исходник читается, локальные значения редактируются, публикация меняется только отдельной операцией.</p><div class="actions">${button('edit-title', 'Изменить локальное название', `data-track="${track.id}"`)}${button('preview-track', 'План этой позиции', `data-track="${track.id}"`, true)}</div></div>`)}</div>` + panel('Сравнение тегов', tagMatrix(release, track, file, findProviderTrack(track.providerTrackId), track.published));
}

function makeGroupPlan(g = group()) {
  const target = currentTarget(g);
  return { mode: g.mode, groupId: g.id, target: structuredClone(target), localReleaseId: targetLocalRelease(g, target)?.id || null, assignments: structuredClone(draft(g)) };
}

function makeReleasePlan(release, trackId = null) {
  return { mode: 'local', groupId: null, target: structuredClone(release), localReleaseId: release.id, assignments: Object.fromEntries(release.tracks.filter(track => track.sourceId).map(track => [track.id, { fileId: track.sourceId, providerTrackId: track.providerTrackId, method: 'Текущий выбранный источник', included: !trackId || track.id === trackId }])) };
}

function planRows(plan) {
  const existing = localRelease(plan.localReleaseId);
  return plan.target.tracks.map(targetTrack => {
    const assignment = plan.assignments[targetTrack.id];
    const track = existing?.tracks.find(item => item.position === targetTrack.position);
    const file = assignment && demoFiles[assignment.fileId];
    const provider = assignment?.providerTrackId && findProviderTrack(assignment.providerTrackId);
    const nextTrack = plan.mode === 'local' ? { ...(track || targetTrack), recordingId: provider?.track.recordingId || track?.recordingId } : { ...targetTrack, sourceId: file?.id };
    const nextTags = tagsForLocal(plan.mode === 'local' ? existing || plan.target : plan.target, nextTrack);
    const previous = track?.published;
    const unchanged = previous && file && previous.sourceId === file.id && previous.codec === file.codec && JSON.stringify(previous.tags) === JSON.stringify(nextTags);
    return { targetTrack, assignment, track, file, provider, nextTags, previous, action: !assignment?.included || !file ? 'Пропустить' : unchanged ? 'Без изменений' : previous ? 'Заменить' : 'Создать', downgrade: Boolean(previous?.codec === 'FLAC' && file?.codec === 'MP3' && assignment?.included) };
  });
}

function publications() {
  if (!ui.plan) ui.plan = makeGroupPlan();
  const plan = ui.plan;
  const rows = planRows(plan);
  const active = rows.filter(row => row.assignment?.included && row.file);
  const changed = active.filter(row => row.action !== 'Без изменений');
  const g = plan.groupId && incomingGroups.find(item => item.id === plan.groupId);
  const kept = g ? g.fileIds.filter(id => !active.some(row => row.file.id === id)).length : 0;
  return header(`План: ${plan.target.title}`, 'Частичный релиз допустим. Выбранные позиции публикуются, остальные файлы и позиции не изменяются.', link(plan.groupId ? `match/${plan.groupId}` : `release/${plan.localReleaseId}`, 'Вернуться к назначениям', 'button')) +
    `<div class="inline-summary">${badge(`${active.length} / ${plan.target.tracks.length} выбрано`, 'success')}<span>${changed.length} изменений</span><span>${kept} файлов без публикации останутся во входящих</span><span>MKA · remux без перекодирования</span></div>` +
    panel('Позиции и последствия', table(['Включить', '№ / трек', 'Источник', 'Сейчас опубликовано', 'Будет записано', 'Действие'], rows.map(row => [`<input type="checkbox" data-plan-include="${row.targetTrack.id}" aria-label="Включить ${esc(row.targetTrack.title)}" ${row.assignment?.included ? 'checked' : ''} ${!row.file ? 'disabled' : ''}>`, `${row.targetTrack.position} · ${esc(row.targetTrack.title)}`, row.file ? `${esc(row.file.name)}<span class="sub">${row.file.codec}</span>` : 'Нет файла', row.previous ? `${row.previous.codec}<span class="sub">${showTag(row.previous.tags.TITLE)}</span>` : 'Нет публикации', `${showTag(row.nextTags.TITLE)}<span class="sub">${showTag(row.nextTags.ALBUM)}</span>`, badge(row.action, row.action === 'Пропустить' ? '' : 'info')]))) +
    note(`<strong>Исходники сохранятся.</strong> ${plan.target.tracks.length - active.length} позиций не включены. Их существующие публикации, если есть, тоже сохранятся. Неразмеченные файлы не удаляются и не считаются обработанными.`) +
    `<div class="panel-foot mono">Целевой каталог: /srv/music/${esc(artistName(plan.target.artistId))}/${esc(plan.target.year)} - ${esc(plan.target.title)}/<br>Имена: 01 - название.mka · В этом макете файлы на диске не создаются.</div><div class="commit-bar"><div><strong>${changed.length} файлов изменится</strong><span class="sub">${active.length - changed.length} выбранных позиций уже актуальны.</span></div>${button('publish-plan', 'Подтвердить публикацию', changed.length ? '' : 'disabled', true)}</div>`;
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
    if (!row.assignment?.included || !row.file || row.action === 'Без изменений') continue;
    if (automatic && row.previous && !(row.previous.codec === 'MP3' && row.file.codec === 'FLAC')) continue;
    const track = release.tracks.find(item => item.position === row.targetTrack.position);
    track.sourceId = row.file.id;
    track.providerTrackId = row.assignment.providerTrackId || null;
    if (row.provider) track.recordingId = row.provider.track.recordingId;
    track.published = { tags: structuredClone(row.nextTags), codec: row.file.codec, container: 'MKA', sourceId: row.file.id, readAt: 'Только что · демо', path: `/srv/music/${artistName(release.artistId)}/${release.year} - ${release.title}/${String(track.position).padStart(2, '0')} - ${track.title}.mka` };
    changed++;
  }
  return changed;
}

function sources() {
  return header('Источники', 'Серверные пути, только чтение. Файлы не загружаются через drag-and-drop.', button('source-scan', 'Сканировать · демо')) + panel('Подключённые каталоги', table(['Имя', 'Путь', 'Режим', 'Доступность', 'Последняя проверка', ''], [['Входящие', '<code>/mnt/inbox</code>', 'Read-only', badge('Доступен', 'success'), '26.09.2026 10:20', link('review', 'Разобрать группы', 'button')], ['Архивный диск', '<code>/mnt/archive</code>', 'Read-only', badge('Отключён', 'warning'), '25.09.2026 18:40', button('check-source', 'Проверить')]])) + note('Недоступность исходного диска не удаляет существующие управляемые публикации.');
}

function operations() {
  return header('Текущие операции', 'Только незавершённая работа. Завершённой истории здесь нет.') + panel('Требует внимания', table(['Объект', 'Операция', 'Фаза', 'Причина', 'Последствие', ''], [['Signals · позиция 06', 'Публикация track bundle', badge('failed', 'error'), 'EACCES: нет записи в staging', 'Текущая копия сохранена; остальные позиции не затронуты', button('retry', 'Повторить')]])) + note('Подготовка → staged → validated → exposed → cleaning. Повтор не должен создавать вторую управляемую копию.', 'warning');
}

function settings() {
  return header('Настройки', 'Тема доступна в верхней панели. Рабочие настройки продукта сохраняются в БД; здесь демонстрация.', button('check-updates', 'Проверить обновления', '', true)) +
    `<div class="two-columns">${panel('Инструменты · Linux amd64', table(['Инструмент', 'Активная версия', 'Источник', 'Проверено'], [['ffmpeg', ui.toolsUpdated ? '8.0.1' : '8.0', 'BtbN/FFmpeg-Builds', 'digest + --version'], ['ffprobe', ui.toolsUpdated ? '8.0.1' : '8.0', 'BtbN/FFmpeg-Builds', 'digest + --version'], ['fpcalc', '1.5.1', 'AcoustID Chromaprint', 'digest + --version']]) + `<div class="panel-foot"><code>/var/lib/music/tools</code> · persistent directory<br>Версии условные. Проверка только уведомляет; установка и откат явные.<div class="actions">${ui.toolsChecked && !ui.toolsUpdated ? button('install-tools', 'Установить обновление') : ''}${ui.toolsUpdated ? button('rollback', 'Откатить к 8.0') : ''}</div></div>`)}${panel('Обработка и публикация', facts([['Полный уверенный релиз', 'Автоматически сопоставить и опубликовать'], ['Частичный / неоднозначный', 'Ручной разбор; частичная публикация разрешена'], ['Исходники', 'Сохранить; неразмеченные остаются во входящих'], ['Score', 'Формула, веса и порог ещё не утверждены'], ['Формат / каталог', 'MKA · /srv/music'], ['Метаданные / тексты', 'MusicBrainz / LRCLIB']]))}</div>`;
}

function setup() {
  const steps = ['Каталог', 'Инструменты', 'Публикация', 'Итог'];
  const content = [
    `<label class="field" for="tools-path">Persistent tools-directory<input id="tools-path" value="${esc(ui.toolsPath)}" aria-describedby="path-help"><span class="sub" id="path-help">Абсолютный путь на сервере, не на компьютере с браузером.</span></label>${button('check-path', 'Проверить путь')}`,
    table(['Инструмент', 'Версия · демо', 'Проверка'], [['ffmpeg', '8.0', badge('Готов', 'success')], ['ffprobe', '8.0', badge('Готов', 'success')], ['fpcalc', '1.5.1', badge('Готов', 'success')]]) + note('В реальном Setup инструменты скачиваются автоматически после выбора каталога. В этом макете скачивания нет.'),
    facts([['Каталог', '<code>/srv/music</code>'], ['Режим примера', 'MKA remux · без перекодирования'], ['Исходники', 'Только чтение'], ['Граница продукта', 'Также предусмотрен исходный формат; макет демонстрирует MKA']]),
    facts([['MusicBrainz', 'Публичный сервис · поддержка self-hosted в спецификации'], ['LRCLIB', 'Тексты'], ['Авторизация', 'Внешний auth-proxy'], ['База данных', 'Подключена · демо'], ['Частичные релизы', 'Разрешены']])
  ];
  return header('Первый запуск', 'Требуется рабочая конфигурация инструментов. Версии, доступность и пути демонстрационные.') + `<div class="stepper">${steps.map((step, i) => `<span class="${i === ui.setupStep ? 'active' : ''}">${i + 1}. ${step}</span>`).join('')}</div>` + panel(steps[ui.setupStep], `<div class="padded">${content[ui.setupStep]}<div class="actions">${ui.setupStep ? button('setup-back', 'Назад') : ''}${button('setup-next', ui.setupStep === 3 ? 'Завершить демонстрацию' : 'Продолжить', ui.setupStep === 0 && !ui.pathChecked ? 'disabled' : '', true)}</div></div>`);
}

function components() {
  return header('Компоненты · плотный интерфейс', 'Обе темы используют одинаковую геометрию и семантические состояния.') + panel('Состояния и действия', `<div class="padded actions">${badge('Высокая уверенность', 'success')}${badge('Есть отличия', 'warning')}${badge('Ошибка', 'error')}${badge('Без provider-связи')}${button('sample', 'Основное действие', '', true)}${button('sample', 'Вторичное действие')}<button disabled>Нет назначения</button></div>`) + panel('Файл для назначения', `<div class="padded">${fileChip(demoFiles['north-f3'])}</div>`) + panel('Техническая таблица', table(['Поле', 'Исходник', 'Провайдер', 'Различие'], [['TITLE', 'Northbound (live?)', 'Northbound', badge('Версия записи не подтверждена', 'warning')], ['DURATION', '3:58', '3:46', '+12 с']])) + note('Числовой confidence не подставляется, пока неизвестна формула. Цвет не заменяет текст.');
}

function route() { return location.hash.slice(1).split('/'); }

function render(resetScroll = true) {
  const [requested, id, scope] = route();
  const name = requested || 'review';
  if (name === 'match' && incomingGroups.some(item => item.id === id) && ui.groupId !== id) {
    ui.groupId = id; ui.selectedTarget = null; ui.selectedFile = null; ui.recordingTarget = null; ui.providerResults = null; ui.providerQuery = group().mode === 'provider' ? currentTarget().title : '';
  }
  const renderers = { review, match, library, artist: () => artistPage(id), release: () => releasePage(id), track: () => trackPage(id), tags: () => tagsPage(id, scope), sources, publications, operations, settings, setup, components };
  const page = Object.hasOwn(renderers, name) ? name : 'review';
  const activeElement = document.activeElement;
  const focusId = activeElement?.id;
  const focusSelector = activeElement instanceof HTMLElement && view.contains(activeElement) ? [...activeElement.attributes].filter(attribute => attribute.name.startsWith('data-')).map(attribute => `[${attribute.name}="${CSS.escape(attribute.value)}"]`).join('') : '';
  const focusRow = activeElement instanceof Element ? activeElement.closest('[data-drop]')?.getAttribute('data-drop') : null;
  view.innerHTML = renderers[page]();
  document.querySelector('#notice').textContent = ui.notice;
  const heading = view.querySelector('h1');
  document.title = `${heading.textContent} · MusicEnreachment · макет v2`;
  document.querySelector('#breadcrumb').textContent = `${page === 'match' ? 'Входящие / ' : ''}${heading.textContent}`;
  const section = ['artist', 'release', 'track', 'tags'].includes(page) ? 'library' : page === 'match' ? 'review' : page;
  document.querySelectorAll('[data-nav]').forEach(item => {
    if (item.dataset.nav === section) item.setAttribute('aria-current', 'page'); else item.removeAttribute('aria-current');
  });
  document.querySelector('#review-count').textContent = String(incomingGroups.filter(item => !item.processed).length);
  if (resetScroll) { document.querySelector('main').scrollTop = 0; heading.focus({ preventScroll: true }); }
  else {
    const focusTarget = (focusId && document.getElementById(focusId)) || (focusSelector && view.querySelector(focusSelector)) || (focusRow && view.querySelector(`[data-drop="${CSS.escape(focusRow)}"] button`));
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
  switch (target.dataset.action) {
    case 'run-auto': {
      let count = 0;
      for (const g of incomingGroups) {
        const assignments = draft(g);
        const targetRelease = currentTarget(g);
        if (!g.autoEligible || g.processed || g.mode !== 'provider' || targetRelease.tracks.length !== g.fileIds.length || !targetRelease.tracks.every(track => assignments[track.id]?.confidence === 'high' && assignments[track.id]?.included && assignments[track.id]?.method.startsWith('Авто'))) continue;
        count += applyPlan(makeGroupPlan(g), true); g.processed = true;
      }
      ui.notice = count ? `Демо: автоматически опубликовано ${count} позиций Small Hours. Неполные группы остались в ручном разборе. Файловые операции не выполнялись.` : 'Нет новых полностью уверенных релизов. Автопубликация не повторяет и не понижает качество.';
      render(false); break;
    }
    case 'local-mode': group().mode = 'local'; ui.selectedTarget = null; ui.selectedFile = null; ui.recordingTarget = null; render(false); break;
    case 'unassign': delete draft()[target.dataset.target]; ui.notice = 'Назначение снято в черновике. Существующая публикация сохранена.'; render(false); break;
    case 'assign-selected': if (ui.selectedFile) assignFile(ui.selectedFile, target.dataset.target); break;
    case 'find-recording': ui.recordingTarget = target.dataset.target; ui.recordingQuery = currentTarget().tracks.find(track => track.id === ui.recordingTarget).title; ui.recordingResults = null; render(false); document.querySelector('#recording-query').focus(); break;
    case 'close-recording': ui.recordingTarget = null; render(false); break;
    case 'bind-recording': {
      const assignment = draft()[ui.recordingTarget];
      if (!assignment) { notify('Сначала назначьте аудиофайл этой локальной позиции.'); break; }
      assignment.providerTrackId = target.dataset.recording; assignment.method = 'Вручную · recording';
      ui.selectedTarget = ui.recordingTarget; ui.recordingTarget = null;
      ui.notice = 'Связь recording добавлена в черновик. Название и порядок локального сборника сохранены.';
      render(false); break;
    }
    case 'preview-group': ui.plan = makeGroupPlan(); location.hash = 'publications'; break;
    case 'preview-release': ui.plan = makeReleasePlan(localRelease(target.dataset.release)); location.hash = 'publications'; break;
    case 'preview-track': { const context = findLocalTrack(target.dataset.track); ui.plan = makeReleasePlan(context.release, context.track.id); location.hash = 'publications'; break; }
    case 'publish-plan': {
      const rows = planRows(ui.plan).filter(row => row.assignment?.included && row.file && row.action !== 'Без изменений');
      if (!rows.length) break;
      const downgrade = rows.some(row => row.downgrade);
      confirm(`Опубликовать ${rows.length} позиций?`, `<p>${esc(ui.plan.target.title)}. Будут изменены только выбранные строки плана. Остальные позиции и существующие файлы сохраняются.</p>${downgrade ? '<label class="inline-check"><input type="checkbox" id="confirm-downgrade" required>Подтверждаю замену lossless на lossy</label>' : ''}<p>Неразмеченные файлы останутся во входящих. Все исходники сохраняются.</p>${note('Демонстрация: запись аудиофайлов не выполняется.')}`, () => { const count = applyPlan(ui.plan); ui.notice = `Демо: ${count} позиций опубликовано. Невыбранные позиции и неразмеченные файлы не изменены.`; render(false); }); break;
    }
    case 'edit-title': {
      const { track } = findLocalTrack(target.dataset.track);
      confirm('Изменить только локальные теги', `<label class="field" for="local-title">Название позиции<input id="local-title" value="${esc(track.title)}" required></label><p>Исходник, кэш провайдера и текущая публикация останутся прежними.</p>`, () => { track.title = document.querySelector('#local-title').value.trim() || track.title; ui.notice = 'Локальное название изменено. Сравните с текущей публикацией; её теги ещё прежние.'; render(false); }); break;
    }
    case 'source-scan': notify('Демо: сканирование поставлено в очередь. Перейдите во входящие; реальные каталоги не читались.'); break;
    case 'check-source': notify('Демо: /mnt/archive недоступен. Управляемые публикации сохранены.'); break;
    case 'retry': notify('Демо: повтор остановлен на EACCES. Нужно восстановить права staging-каталога.'); break;
    case 'check-updates': ui.toolsChecked = true; ui.notice = 'Демо: есть обновление FFmpeg/ffprobe. Оно не установлено автоматически.'; render(false); break;
    case 'install-tools': confirm('Установить обновление инструментов?', '<p>Скачать из доверенного источника, проверить digest и версию, затем активировать. Предыдущая версия останется для отката. Действие демонстрационное.</p>', () => { ui.toolsUpdated = true; render(false); }); break;
    case 'rollback': confirm('Откатить инструменты к 8.0?', '<p>Будет выбрана предыдущая проверенная версия. Действие демонстрационное.</p>', () => { ui.toolsUpdated = false; render(false); }); break;
    case 'check-path': ui.toolsPath = document.querySelector('#tools-path').value.trim(); ui.pathChecked = ui.toolsPath.startsWith('/') && ui.toolsPath.length > 1; document.querySelector('#tools-path').setAttribute('aria-invalid', String(!ui.pathChecked)); document.querySelector('[data-action="setup-next"]').disabled = !ui.pathChecked; notify(ui.pathChecked ? 'Абсолютный путь принят в демонстрации. Права и persistent volume не проверялись.' : 'Введите абсолютный путь, например /var/lib/music/tools.'); break;
    case 'setup-back': ui.setupStep--; render(false); break;
    case 'setup-next': if (ui.setupStep === 3) location.hash = 'review'; else { ui.setupStep++; render(false); } break;
    case 'sample': notify('Состояние компонента. Domain-данные не изменены.'); break;
  }
});

document.addEventListener('submit', event => {
  if (!(event.target instanceof HTMLFormElement)) return;
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

document.addEventListener('change', event => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  if (target.dataset.include) { draft()[target.dataset.include].included = target.checked; render(false); }
  if (target.dataset.planInclude) { ui.plan.assignments[target.dataset.planInclude].included = target.checked; render(false); }
  if (target.id === 'differences-only') { ui.differencesOnly = target.checked; render(false); }
  if (target.id === 'publication-filter') { ui.publicationFilter = target.value; document.querySelector('#catalog-content').innerHTML = catalogRows(); }
  if (target.id === 'tag-file') location.hash = `tags/${target.value}/${ui.tagScope}`;
});

document.addEventListener('input', event => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement)) return;
  if (target.id === 'library-search') { ui.libraryQuery = target.value; document.querySelector('#catalog-content').innerHTML = catalogRows(); }
  if (target.id === 'tools-path') { ui.toolsPath = target.value; ui.pathChecked = false; document.querySelector('[data-action="setup-next"]').disabled = true; }
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
  else notify('Назначать можно только известные файлы текущей входной группы. Upload внешних файлов не поддерживается.');
  draggedFile = null; document.querySelectorAll('.drag-over,.dragging').forEach(item => item.classList.remove('drag-over', 'dragging'));
});
document.addEventListener('dragend', () => { draggedFile = null; document.querySelectorAll('.drag-over,.dragging').forEach(item => item.classList.remove('drag-over', 'dragging')); });
document.addEventListener('keydown', event => { if (event.key === 'Escape' && !dialog.open && ui.selectedFile) { ui.selectedFile = null; render(false); notify('Выбор файла отменён.'); } });
dialog.addEventListener('close', () => { if (dialog.returnValue === 'confirm' && confirmAction) confirmAction(); confirmAction = null; });
window.addEventListener('hashchange', () => { if (location.hash === '#content') { document.querySelector('main').focus(); return; } ui.notice = ''; render(); });
render(false);
