/* Offline-only fixture for the shared MusicBrainz genre catalogue concept. */
window.GenreLibraryPrototype = (() => {
  const fixture = [
    { id: 'demo-genre-ambient', name: 'ambient' },
    { id: 'demo-genre-folk', name: 'folk' },
    { id: 'demo-genre-contemporary-jazz', name: 'contemporary jazz' },
    { id: 'demo-genre-electronic', name: 'electronic' },
    { id: 'demo-genre-rock', name: 'rock' },
    { id: 'demo-genre-soul', name: 'soul' },
    { id: 'demo-genre-classical', name: 'classical' },
    { id: 'demo-genre-hip-hop', name: 'hip hop' }
  ];
  const state = { loaded: false, lastSuccess: '', lastFailure: '' };
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character]));
  const catalogue = () => state.loaded ? fixture : [];
  const get = id => catalogue().find(genre => genre.id === id);

  function load() {
    state.loaded = true;
    state.lastSuccess = 'демо · сейчас';
    document.dispatchEvent(new CustomEvent('genre-library-change'));
  }

  function statusMarkup() {
    return `<div class="genre-library-status"><strong>${state.loaded ? 'Каталог доступен в демонстрации' : 'Каталог недоступен'}</strong><p>${state.loaded ? `Последнее успешное обновление: ${escape(state.lastSuccess)}.` : 'Жанры не загружены. Выбор жанров недоступен; локальные записи можно сохранять без жанров.'}</p>${state.lastFailure ? `<p>Последняя неудачная попытка: ${escape(state.lastFailure)}.</p>` : ''}<small>В продукте каталог загружается с настроенного публичного MusicBrainz или self-hosted сервера. Офлайн-макет показывает небольшой набор вымышленных fixture-значений, не полный каталог MusicBrainz; сеть и обновления не выполняются.</small></div>`;
  }

  function controls(buttonLabel = 'Загрузить каталог в демо') {
    return `<div class="actions genre-library-controls"><button type="button" data-action="genre-library-load">${buttonLabel}</button><span class="muted">Автоматическое обновление: раз в неделю в продукте; в макете — только вручную.</span></div>`;
  }

  function renderPicker(selectedIds = [], name = 'genreIds', query = '') {
    const ids = [...new Set(selectedIds)].filter(id => get(id));
    if (!state.loaded) return `<fieldset class="genre-picker" disabled><legend>Жанры</legend><p class="muted">Каталог недоступен. Можно сохранить запись без жанров.</p></fieldset>`;
    const selected = ids.map(id => get(id)).filter(Boolean);
    const options = catalogue().filter(genre => !ids.includes(genre.id));
    return `<fieldset class="genre-picker"><legend>Жанры · MusicBrainz</legend><label class="field"><span>Поиск по каталогу</span><input type="search" data-genre-search value="${escape(query)}" placeholder="Начните вводить название" autocomplete="off"></label><div class="genre-picker-options" data-genre-options>${optionButtons(options, query)}</div><div class="genre-picker-selected" data-genre-selected>${selected.length ? selected.map(genre => `<span class="genre-chip">${escape(genre.name)}<button type="button" data-genre-remove="${escape(genre.id)}" aria-label="Убрать ${escape(genre.name)}">×</button><input type="hidden" name="${escape(name)}" value="${escape(genre.id)}"></span>`).join('') : '<span class="muted">Жанры не выбраны.</span>'}</div><small>Только канонические ID и названия из загруженного каталога; произвольный ввод не добавляет значение.</small></fieldset>`;
  }

  function optionButtons(options, query = '') {
    const needle = query.trim().toLocaleLowerCase();
    const matches = options.filter(genre => genre.name.toLocaleLowerCase().includes(needle));
    return matches.length ? matches.map(genre => `<button type="button" data-genre-add="${escape(genre.id)}">${escape(genre.name)}</button>`).join('') : '<span class="muted">Совпадений нет.</span>';
  }

  function selectedIds(form, name = 'genreIds') {
    return [...form.querySelectorAll(`input[type="hidden"][name="${name}"]`)].map(input => input.value).filter(id => get(id));
  }

  document.addEventListener('click', event => {
    if (!(event.target instanceof Element)) return;
    const loadButton = event.target.closest('[data-action="genre-library-load"]');
    if (loadButton) { load(); return; }
    const add = event.target.closest('[data-genre-add]');
    const remove = event.target.closest('[data-genre-remove]');
    const root = event.target.closest('.genre-picker');
    if (!root || (!add && !remove)) return;
    const ids = new Set(selectedIds(root.closest('form') || root));
    if (add && get(add.dataset.genreAdd)) ids.add(add.dataset.genreAdd);
    if (remove) ids.delete(remove.dataset.genreRemove);
    const search = root.querySelector('[data-genre-search]')?.value || '';
    const name = root.querySelector('input[type="hidden"][name]')?.name || 'genreIds';
    const host = root.parentElement;
    const temp = document.createElement('div');
    temp.innerHTML = renderPicker([...ids], name, search);
    root.replaceWith(temp.firstElementChild);
    const next = host.querySelector('.genre-picker');
    const input = next?.querySelector('[data-genre-search]');
    if (input) { input.focus({ preventScroll: true }); input.setSelectionRange(search.length, search.length); }
  });

  document.addEventListener('input', event => {
    if (!(event.target instanceof HTMLInputElement) || !event.target.matches('[data-genre-search]')) return;
    const root = event.target.closest('.genre-picker');
    const ids = selectedIds(root.closest('form') || root);
    const options = catalogue().filter(genre => !ids.includes(genre.id));
    root.querySelector('[data-genre-options]').innerHTML = optionButtons(options, event.target.value);
  });

  return { catalogue, get, load, statusMarkup, controls, renderPicker, selectedIds };
})();
