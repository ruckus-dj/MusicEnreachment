/* In-memory interactive Local provider screen for the standalone concept. */
window.LocalProviderPrototype = (() => {
  const artists = [
    { id: 'local-artist-vale', key: '45ba3b5a-0e0a-4ea9-bcc9-30ee90388e12', provider: 'Local', name: 'Mira Vale', disambiguation: '', genres: [], tags: 'independent' },
    { id: 'local-artist-trio', key: '0bb5a16e-0a96-4f11-a124-d04ea8a05f0f', provider: 'Local', name: 'Arden Trio', disambiguation: '', genres: [], tags: '' },
    { id: 'mb-artist-forma', key: 'mb-artist-forma', provider: 'MusicBrainz', name: 'Forma', disambiguation: 'Electronic project', genres: ['demo-genre-electronic'], genreNamesById: { 'demo-genre-electronic': 'electronic' }, tags: 'Berlin' },
    { id: 'sp-artist-forma', key: 'sp-artist-forma', provider: 'Spotify', name: 'Forma', disambiguation: '', genres: [], tags: '' }
  ];
  const releases = [
    { id: 'local-release-north', key: '614b2168-70ab-47e3-b02b-e324bb11fd87', provider: 'Local', release_group_title: 'Northern Lines', title: 'Northern Lines', artistIds: ['local-artist-vale'], genres: [], country_code: 'XE', release_date: '2021', label: 'Field Records', catalog_number: 'FR-021', status: '', packaging: '', barcode: '', disambiguation: '', primaryType: 'Album', secondaryTypes: [], language: 'eng', tracks: [{ recordingId: 'local-recording-green', titleOverride: '', durationOverrideMs: '' }] },
    { id: 'mb-release-hours', key: 'mb-release-hours', provider: 'MusicBrainz', release_group_title: 'Small Hours', title: 'Small Hours', artistIds: ['mb-artist-forma'], country_code: 'GB', release_date: '2018-05-11', label: 'Room Tone', catalog_number: 'RT-018', status: 'Official', packaging: 'CD', barcode: '', disambiguation: '' }
  ];
  const recordings = [
    { id: 'local-recording-green', key: 'f3d900a3-f4bf-43db-a96c-2804d00fe235', provider: 'Local', title: 'Green Room', artistIds: ['local-artist-trio'], duration_ms: '284000', disambiguation: '', explicit: '', isrcs: '', genres: [], tags: '' },
    { id: 'mb-recording-relay', key: 'mb-recording-relay', provider: 'MusicBrainz', title: 'Relay', artistIds: ['mb-artist-forma'], duration_ms: '243000', disambiguation: '', explicit: 'false', isrcs: 'USAAA2400001', genres: ['demo-genre-electronic'], genreNamesById: { 'demo-genre-electronic': 'electronic' }, tags: '' }
  ];
  const types = {
    artists: { label: 'Артисты', entity: 'Артист', create: 'Создать артиста', edit: 'Редактировать артиста', search: 'Поиск в артистах', items: artists },
    releases: { label: 'Релизы', entity: 'Релиз', create: 'Создать релиз', edit: 'Редактировать релиз', search: 'Поиск в релизах', items: releases },
    recordings: { label: 'Композиции', entity: 'Композиция', create: 'Создать композицию', edit: 'Редактировать композицию', search: 'Поиск в композициях', items: recordings }
  };
  const state = { type: 'artists', selectedId: 'local-artist-vale', query: '', editing: false, creating: false, notice: '' };
  const esc = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
  const uuid = () => crypto.randomUUID();
  const currentType = () => types[state.type];
  const plural = (count, one, few, many) => `${count} ${count % 10 === 1 && count % 100 !== 11 ? one : count % 10 >= 2 && count % 10 <= 4 && (count % 100 < 12 || count % 100 > 14) ? few : many}`;
  const localItems = type => type.items.filter(item => item.provider === 'Local');
  const selected = () => currentType().items.find(item => item.id === state.selectedId);
  const artistName = id => artists.find(artist => artist.id === id)?.name || 'Артист не указан';
  const recording = id => recordings.find(item => item.id === id);
  const formatDuration = milliseconds => milliseconds === '' || milliseconds === null || milliseconds === undefined ? '' : `${Math.floor(Number(milliseconds) / 60000)}:${String(Math.floor(Number(milliseconds) % 60000 / 1000)).padStart(2, '0')}`;
  const parseDuration = value => {
    const input = String(value ?? '').trim();
    if (!input) return null;
    if (/^\d+$/.test(input)) return Number(input) * 1000;
    const match = /^(\d+):(\d{1,2})$/.exec(input);
    if (!match || Number(match[2]) > 59) return NaN;
    return (Number(match[1]) * 60 + Number(match[2])) * 1000;
  };
  const providerArtistOptions = item => artists.map(artist => `<label class="local-provider-option"><input type="checkbox" name="artistIds" value="${esc(artist.id)}" ${item?.artistIds?.includes(artist.id) ? 'checked' : ''}><span>${esc(artist.name)} <span class="muted">· ${esc(artist.provider)}${artist.provider === 'Local' ? '' : ' · внешняя ссылка, только чтение'} · <code>${esc(artist.key)}</code></span></span></label>`).join('');
  const genrePicker = item => window.GenreLibraryPrototype.renderPicker(item?.genres || []);
  const genreNames = item => (item?.genres || []).map(id => window.GenreLibraryPrototype.get(id)?.name || item?.genreNamesById?.[id]).filter(Boolean).join(', ') || '—';
  const field = (label, name, value = '', required = false, type = 'text') => `<label class="field">${label}${required ? ' <span class="required">Обязательно</span>' : ' <span class="muted">необязательно</span>'}<input name="${name}" type="${type}" value="${esc(value)}" ${required ? 'required' : ''}></label>`;

  function trackRows(item) {
    const tracks = item?.tracks || [];
    return `<div class="local-provider-tracklist"><div class="local-provider-tracklist-heading"><strong>Носитель 1 · позиции</strong><button type="button" data-local-action="add-track">Добавить позицию</button></div>${tracks.length ? tracks.map((track, index) => {
      const selectedRecording = recording(track.recordingId);
      const options = recordings.map(candidate => `<option value="${esc(candidate.id)}" ${candidate.id === track.recordingId ? 'selected' : ''}>${esc(candidate.title)} · ${esc(candidate.provider)} · ${esc(candidate.key)}</option>`).join('');
      const durationOverride = track.durationInput ?? formatDuration(track.durationOverrideMs);
      return `<fieldset class="local-provider-track" data-track-index="${index}"><legend>Позиция ${index + 1}</legend><div class="local-provider-track-actions"><button type="button" data-local-action="track-up" data-index="${index}" aria-label="Переместить позицию ${index + 1} выше" ${index === 0 ? 'disabled' : ''}>↑</button><button type="button" data-local-action="track-down" data-index="${index}" aria-label="Переместить позицию ${index + 1} ниже" ${index === tracks.length - 1 ? 'disabled' : ''}>↓</button><button type="button" data-local-action="remove-track" data-index="${index}" aria-label="Удалить позицию ${index + 1}">Удалить</button></div><label class="field">Существующая композиция <select name="trackRecording" required><option value="">Выберите композицию…</option>${options}</select></label><p class="local-provider-track-default">По умолчанию: <strong>${esc(selectedRecording?.title || 'Композиция не выбрана')}</strong> · ${formatDuration(selectedRecording?.duration_ms) || 'длительность не указана'} · ${esc(selectedRecording?.artistIds?.map(artistName).join(', ') || 'артисты не указаны')}</p><div class="local-provider-fields">${field('Название в этой позиции · override', 'trackTitleOverride', track.titleOverride || '')}${field('Длительность · override', 'trackDurationOverride', durationOverride)}</div><small>Номер позиции определяется порядком строки. Длительность: целые секунды или М:СС.</small></fieldset>`;
    }).join('') : '<p class="empty">Позиции не добавлены. Пустой трек-лист допустим.</p>'}</div>`;
  }

  function listMarkup() {
    const terms = state.query.normalize('NFKC').toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
    const items = localItems(currentType()).filter(item => terms.every(term => `${item.name || ''} ${item.title || ''} ${item.release_group_title || ''} ${item.key}`.normalize('NFKC').toLocaleLowerCase().includes(term)));
    if (!items.some(item => item.id === state.selectedId)) state.selectedId = items[0]?.id || null;
    const rows = items.map(item => {
      const title = state.type === 'artists' ? item.name : item.title;
      const subtitle = state.type === 'releases' ? `${item.release_group_title} · ${item.artistIds.map(artistName).join(', ') || 'Артисты не указаны'}` : state.type === 'recordings' ? item.artistIds.map(artistName).join(', ') || 'Артисты не указаны' : `${genreNames(item)} · ${releases.filter(release => release.artistIds.includes(item.id)).length} связанных релизов`;
      return `<button type="button" class="local-provider-row ${item.id === state.selectedId ? 'selected' : ''}" data-local-action="select" data-id="${esc(item.id)}"><span><strong>${esc(title)}</strong><small>${esc(subtitle)}</small></span><span class="local-provider-origin">${esc(item.provider)}</span></button>`;
    }).join('');
    return `<div id="local-provider-list">${rows || '<div class="empty">Записей не найдено. Поиск ничего не создаёт автоматически.</div>'}</div>`;
  }

  function formMarkup(item) {
    if (!state.editing && !state.creating) return detailMarkup(item);
    const value = item || {};
    let fields = '';
    if (state.type === 'artists') {
      fields = `${field('Имя', 'name', value.name, true)}${field('Уточнение', 'disambiguation', value.disambiguation)}${genrePicker(value)}${field('Теги · через запятую', 'tags', value.tags)}`;
    } else if (state.type === 'releases') {
      fields = `${field('Название группы релизов', 'release_group_title', value.release_group_title, true)}${field('Название издания', 'title', value.title, true)}<div class="local-provider-fields">${field('Основной тип группы релизов', 'primaryType', value.primaryType)}${field('Дополнительные типы · через запятую', 'secondaryTypes', (value.secondaryTypes || []).join(', '))}${field('Язык', 'language', value.language)}${field('Страна', 'country_code', value.country_code)}${field('Дата', 'release_date', value.release_date)}${field('Статус', 'status', value.status)}${field('Упаковка', 'packaging', value.packaging)}${field('Лейбл', 'label', value.label)}${field('Каталожный номер', 'catalog_number', value.catalog_number)}${field('Штрихкод', 'barcode', value.barcode)}${field('Уточнение', 'disambiguation', value.disambiguation)}</div>${genrePicker(value)}<small>Жанры релиза относятся только к релизу и не наследуются артистами или композициями.</small><fieldset class="local-provider-links"><legend>Артисты релиза · ссылки на существующие provider_artist</legend>${providerArtistOptions(value)}<small>Выбор сохраняет provider ownership и native key; копия записи не создаётся.</small></fieldset>${trackRows(value)}<p class="muted">Script не вводится.</p>`;
    } else {
      fields = `${field('Название композиции', 'title', value.title, true)}${field('Длительность, мс', 'duration_ms', value.duration_ms, false, 'number')}${field('Уточнение', 'disambiguation', value.disambiguation)}<label class="field">Explicit <span class="muted">необязательно</span><select name="explicit"><option value="" ${value.explicit === '' ? 'selected' : ''}>Не указано</option><option value="true" ${value.explicit === 'true' ? 'selected' : ''}>Да</option><option value="false" ${value.explicit === 'false' ? 'selected' : ''}>Нет</option></select></label>${field('ISRC · через запятую', 'isrcs', value.isrcs)}${genrePicker(value)}${field('Теги · через запятую', 'tags', value.tags)}<fieldset class="local-provider-links"><legend>Исполнители · ссылки на существующие provider_artist</legend>${providerArtistOptions(value)}<small>Выбор сохраняет provider ownership и native key; копия записи не создаётся.</small></fieldset>`;
    }
    return `<form id="local-provider-form" class="local-provider-form"><div class="local-provider-form-heading"><div><h2>${state.creating ? currentType().create : currentType().edit}</h2><p class="muted">Только in-memory демонстрация. Обязательны лишь отмеченные поля DBML.</p></div></div>${fields}<div class="actions">${button('cancel', 'Отмена')}${button('save', state.creating ? currentType().create : 'Сохранить изменения', true)}</div></form>`;
  }

  function detailMarkup(item) {
    if (!item) return '<div class="empty">Выберите запись или явно создайте новую.</div>';
    const title = state.type === 'artists' ? item.name : item.title;
    let detail;
    if (state.type === 'artists') detail = [['Жанры', genreNames(item)], ['Теги', item.tags || '—'], ['Связанные релизы', releases.filter(release => release.artistIds.includes(item.id)).map(release => release.title).join(', ') || '—']];
    else if (state.type === 'releases') detail = [['Группа релизов', item.release_group_title], ['Артисты', item.artistIds.map(artistName).join(', ') || '—'], ['Жанры', genreNames(item)], ['Основной тип', item.primaryType || '—'], ['Дополнительные типы', item.secondaryTypes?.join(', ') || '—'], ['Язык', item.language || '—'], ['Позиции · носитель 1', String(item.tracks?.length || 0)], ['Дата', item.release_date || '—'], ['Страна', item.country_code || '—'], ['Лейбл', item.label || '—'], ['Каталожный номер', item.catalog_number || '—']];
    else detail = [['Артисты', item.artistIds.map(artistName).join(', ') || '—'], ['Длительность', item.duration_ms ? `${item.duration_ms} мс` : '—'], ['ISRC', item.isrcs || '—'], ['Жанры', genreNames(item)]];
    return `<div class="local-provider-detail-heading"><div><span class="eyebrow">${esc(item.provider)} · ${esc(currentType().entity)}</span><h2>${esc(title)}</h2></div>${item.provider === 'Local' ? button('edit', 'Редактировать') : ''}</div><dl class="local-provider-facts">${detail.map(([key, value]) => `<div><dt>${esc(key)}</dt><dd>${esc(value)}</dd></div>`).join('')}<div><dt>Provider key</dt><dd><code>${esc(item.key)}</code></dd></div></dl><div class="note">Редактирование metadata само по себе не запускает публикацию. Удаление и поведение зависимостей этим экраном не определяются.</div>`;
  }

  function button(action, label, primary = false) { return `<button type="button" data-local-action="${action}" class="${primary ? 'primary' : ''}">${label}</button>`; }

  function render() {
    const config = currentType();
    const localCount = localItems(config).length;
    const detail = state.editing || state.creating ? formMarkup(state.creating ? null : selected()) : formMarkup(selected());
    return `<div class="page-header"><div><h1 tabindex="-1">Собственный каталог</h1><p class="muted">Ручные записи provider Local · поиск, явное создание и редактирование</p></div><span class="demo-label">МАКЕТ · IN-MEMORY</span></div><div class="local-provider-intro">Собственный каталог — ручные записи provider Local, а не форма создания альбома медиатеки. Поиск не создаёт и не объединяет записи; сначала проверьте существующие providers.</div><div class="local-provider-tabs" role="tablist" aria-label="Тип записей собственного каталога">${Object.entries(types).map(([id, type]) => `<button type="button" role="tab" aria-selected="${state.type === id}" data-local-action="type" data-type="${id}">${type.label}<span>${localItems(type).length}</span></button>`).join('')}</div><div class="local-provider-workspace"><section class="panel local-provider-catalog"><div class="panel-heading"><h2>${config.label}</h2><span class="muted">${plural(localCount, 'запись', 'записи', 'записей')}</span></div><label class="local-provider-search">${config.search}<input type="search" id="local-provider-search" value="${esc(state.query)}" placeholder="Имя, название или provider key"></label><div class="local-provider-list-actions">${button('create', config.create, true)}</div>${listMarkup()}</section><section class="panel local-provider-editor"><div class="panel-heading"><h2>Запись</h2><span class="muted">Текущие данные · без истории</span></div><div class="local-provider-editor-content">${detail}</div></section></div><div class="local-provider-notice" role="status" aria-live="polite">${esc(state.notice || 'Демонстрационные изменения сохраняются в памяти этой страницы.')}</div>`;
  }

  function refresh() {
    const activeSearch = document.activeElement?.id === 'local-provider-search';
    const cursor = activeSearch ? document.activeElement.selectionStart : null;
    document.querySelector('#view').innerHTML = render();
    if (activeSearch) {
      const search = document.querySelector('#local-provider-search');
      search.focus({ preventScroll: true });
      search.setSelectionRange(cursor, cursor);
    }
  }

  function handleClick(action, target) {
    if (!action) return false;
    switch (action) {
      case 'type': state.type = target.dataset.type; state.query = ''; state.selectedId = localItems(currentType())[0]?.id || null; state.editing = false; state.creating = false; break;
      case 'select': state.selectedId = target.dataset.id; state.editing = false; state.creating = false; state.notice = ''; break;
      case 'create': state.selectedId = null; state.editing = false; state.creating = true; state.notice = ''; break;
      case 'edit': state.editing = true; state.creating = false; break;
      case 'cancel': state.editing = false; state.creating = false; break;
      case 'save': document.querySelector('#local-provider-form')?.requestSubmit(); return true;
      default: return false;
    }
    refresh();
    return true;
  }

  function handleSubmit(event) {
    if (event.target.id !== 'local-provider-form') return false;
    event.preventDefault();
    const form = new FormData(event.target);
    const creating = state.creating;
    const item = creating ? { id: uuid(), key: uuid(), provider: 'Local' } : selected();
    const text = name => String(form.get(name) || '').trim();
    const genreIds = window.GenreLibraryPrototype.selectedIds(event.target);
    const genreNamesById = Object.fromEntries(genreIds.map(id => [id, window.GenreLibraryPrototype.get(id)?.name]).filter(([, name]) => name));
    if (state.type === 'artists') Object.assign(item, { name: text('name'), disambiguation: text('disambiguation'), genres: genreIds, genreNamesById, tags: text('tags') });
    if (state.type === 'releases') {
      const trackRows = [...event.target.querySelectorAll('[data-track-index]')];
      const tracks = trackRows.map(row => ({
        recordingId: row.querySelector('[name="trackRecording"]').value,
        titleOverride: row.querySelector('[name="trackTitleOverride"]').value.trim(),
        durationOverrideMs: parseDuration(row.querySelector('[name="trackDurationOverride"]').value)
      }));
      const invalidDuration = trackRows.some(row => Number.isNaN(parseDuration(row.querySelector('[name="trackDurationOverride"]').value)));
      if (invalidDuration) {
        state.notice = 'Проверьте длительность override: используйте целые секунды или М:СС с секундами от 00 до 59.';
        refresh();
        return true;
      }
      Object.assign(item, { release_group_title: text('release_group_title'), title: text('title'), primaryType: text('primaryType'), secondaryTypes: text('secondaryTypes').split(',').map(value => value.trim()).filter(Boolean), language: text('language'), country_code: text('country_code'), release_date: text('release_date'), status: text('status'), packaging: text('packaging'), label: text('label'), catalog_number: text('catalog_number'), barcode: text('barcode'), disambiguation: text('disambiguation'), genres: genreIds, genreNamesById, artistIds: form.getAll('artistIds').map(String), tracks });
    }
    if (state.type === 'recordings') Object.assign(item, { title: text('title'), duration_ms: text('duration_ms'), disambiguation: text('disambiguation'), explicit: text('explicit'), isrcs: text('isrcs'), genres: genreIds, genreNamesById, tags: text('tags'), artistIds: form.getAll('artistIds').map(String) });
    if (state.creating) currentType().items.unshift(item);
    state.selectedId = item.id;
    state.creating = false;
    state.editing = false;
    state.notice = `Демо: ${creating ? 'запись создана' : 'изменения сохранены'} в памяти страницы. API и backend не вызываются.`;
    refresh();
    document.querySelector('.local-provider-detail-heading h2, .local-provider-form-heading h2')?.focus?.({ preventScroll: true });
    return true;
  }

  document.addEventListener('input', event => {
    if (event.target.id !== 'local-provider-search') return;
    state.query = event.target.value;
    refresh();
  });
  document.addEventListener('change', event => {
    if (!(event.target instanceof HTMLSelectElement) || event.target.name !== 'trackRecording') return;
    const selectedRecording = recording(event.target.value);
    const summary = event.target.closest('[data-track-index]').querySelector('.local-provider-track-default');
    summary.innerHTML = `По умолчанию: <strong>${esc(selectedRecording?.title || 'Композиция не выбрана')}</strong> · ${formatDuration(selectedRecording?.duration_ms) || 'длительность не указана'} · ${esc(selectedRecording?.artistIds?.map(artistName).join(', ') || 'артисты не указаны')}`;
  });
  document.addEventListener('click', event => {
    if (!(event.target instanceof Element) || !location.hash.startsWith('#local')) return;
    const target = event.target.closest('[data-local-action]');
    if (!target || !state.editing || state.type !== 'releases') return;
    const rows = [...document.querySelectorAll('#local-provider-form [data-track-index]')];
    const tracks = rows.map(row => ({ recordingId: row.querySelector('[name="trackRecording"]').value, titleOverride: row.querySelector('[name="trackTitleOverride"]').value, durationInput: row.querySelector('[name="trackDurationOverride"]').value }));
    const index = Number(target.dataset.index);
    if (target.dataset.localAction === 'add-track') tracks.push({ recordingId: '', titleOverride: '', durationInput: '' });
    else if (target.dataset.localAction === 'remove-track') tracks.splice(index, 1);
    else if (target.dataset.localAction === 'track-up' && index > 0) [tracks[index - 1], tracks[index]] = [tracks[index], tracks[index - 1]];
    else if (target.dataset.localAction === 'track-down' && index < tracks.length - 1) [tracks[index + 1], tracks[index]] = [tracks[index], tracks[index + 1]];
    else return;
    const form = document.querySelector('#local-provider-form');
    const formData = new FormData(form);
    const previewItem = { ...selected(), tracks };
    const tracklist = form.querySelector('.local-provider-tracklist');
    tracklist.outerHTML = trackRows(previewItem);
    event.stopImmediatePropagation();
    if (target.dataset.localAction === 'add-track') form.querySelector('[data-track-index]:last-of-type select')?.focus();
  });
  document.addEventListener('click', event => {
    if (!(event.target instanceof Element) || !location.hash.startsWith('#local')) return;
    const target = event.target.closest('[data-local-action]');
    if (target && handleClick(target.dataset.localAction, target)) event.stopImmediatePropagation();
  });
  document.addEventListener('submit', event => {
    if (handleSubmit(event)) event.stopImmediatePropagation();
  });

  return { render };
})();
