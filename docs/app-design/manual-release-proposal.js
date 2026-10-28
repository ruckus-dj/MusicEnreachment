window.initManualReleaseForm = (root, initial, onCreate, onDraft, askConfirm) => {
  const data = window.manualReleaseDemoData;
  const artists = root.querySelector('#artists-list');
  const media = root.querySelector('#media-list');
  const covers = root.querySelector('#covers-list');
  let nextId = 1;

  const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
  const makeId = prefix => `${prefix}-${nextId++}`;
  const list = (container, selector) => [...container.querySelectorAll(`:scope > ${selector}`)];
  const byId = (items, id) => items.find(item => item.id === id);
  const recordingName = id => byId(data.recordings, id)?.title || '';
  const sourceName = id => byId(data.sources, id)?.name || '';

  function itemActions(label) {
    return `<div class="repeat-actions"><button type="button" data-action="up" aria-label="Переместить ${label} выше">↑</button><button type="button" data-action="down" aria-label="Переместить ${label} ниже">↓</button><button type="button" data-action="remove" aria-label="Удалить ${label}">Удалить</button></div>`;
  }

  function artistRow(value = {}) {
    const id = makeId('artist');
    return `<div class="repeat-item" data-artist data-id="${id}">
      <div class="repeat-head"><strong>Артист</strong>${itemActions('артиста')}</div>
      <div class="repeat-fields">
        <div class="field"><label for="artist-name-${id}">Артист (поиск / новое имя)</label><input id="artist-name-${id}" data-value="name" list="artist-options" value="${escapeHTML(value.name)}" placeholder="Начните вводить имя" autocomplete="off"></div>
        <div class="field"><label for="artist-credit-${id}">Отображаемый credit</label><input id="artist-credit-${id}" data-value="credit" value="${escapeHTML(value.credit ?? value.name)}" placeholder="Имя в этом релизе"></div>
        <div class="field"><label for="artist-role-${id}">Роль / уточнение (если нужно)</label><input id="artist-role-${id}" data-value="role" value="${escapeHTML(value.role)}" placeholder="Например: вокал"></div>
      </div><span class="sub" data-artist-match>${artistMatch(value.name)}</span>
    </div>`;
  }

  function artistMatch(name) {
    const match = data.artists.find(artist => artist.name.toLocaleLowerCase() === String(name || '').trim().toLocaleLowerCase());
    return match ? `Выбран существующий артист: ${match.name} · демонстрационный каталог` : name ? 'Новое имя артиста · будет частью только этого демо-релиза' : 'Выберите артиста или введите новое имя';
  }

  function positionRow(value = {}) {
    const id = makeId('position');
    const existing = value.recordingMode === 'existing';
    const recordingOptions = data.recordings.map(recording => `<option value="${recording.id}" ${recording.id === value.recordingId ? 'selected' : ''}>${escapeHTML(recording.title)} — ${escapeHTML(recording.artists)} (${recording.duration})</option>`).join('');
    return `<div class="position-item" data-position data-id="${id}">
      <div class="repeat-head"><strong>Позиция</strong>${itemActions('позицию')}</div>
      <div class="repeat-fields">
        <div class="field"><label for="number-${id}">Отображаемый номер</label><input id="number-${id}" data-value="number" value="${escapeHTML(value.number)}" placeholder="Например, A1"></div>
        <div class="field"><label for="title-${id}">Название в этом релизе</label><input id="title-${id}" data-value="title" value="${escapeHTML(value.title)}"></div>
        <div class="field"><label for="duration-${id}">Длительность (если известна)</label><input id="duration-${id}" data-value="duration" value="${escapeHTML(value.duration)}" placeholder="Необязательно"></div>
      </div>
      <span class="field-label">Recording</span>
      <div class="choice-row" role="group" aria-label="Выбор recording">
        <label><input type="radio" name="recording-mode-${id}" value="new" ${existing ? '' : 'checked'} data-mode> Новый recording</label>
        <label><input type="radio" name="recording-mode-${id}" value="existing" ${existing ? 'checked' : ''} data-mode> Существующий recording</label>
      </div>
      <div class="field ${existing ? 'hidden' : ''}" data-new-recording><label for="new-recording-${id}">Название нового recording</label><input id="new-recording-${id}" data-value="newRecordingTitle" value="${escapeHTML(value.newRecordingTitle ?? (existing ? '' : value.title))}" placeholder="Можно оставить пустым"><span class="sub">Новый recording будет показан как черновая сущность, не сохранённая в приложении.</span></div>
      <div class="field ${existing ? '' : 'hidden'}" data-existing-recording><label for="recording-search-${id}">Поиск по демонстрационному каталогу</label><input class="recording-search" id="recording-search-${id}" data-recording-search placeholder="Фильтр названий / артистов" value="${escapeHTML(value.recordingSearch)}"><select data-recording-select aria-label="Существующий recording"><option value="">Выберите recording…</option>${recordingOptions}</select><span class="sub" data-recording-identity>Выбран: ${escapeHTML(recordingName(value.recordingId) || 'не выбран')}</span></div>
      <div class="source-box" data-source-box>
        <div class="panel-heading"><h3>Связанные исходники</h3><span class="sub">Назначения M:N · можно использовать повторно</span></div>
        <div class="source-chips" data-source-chips></div>
        <div class="source-tools"><select data-source-select aria-label="Выбрать вариант исходника"><option value="">Выберите вариант…</option>${data.sources.map(source => `<option value="${source.id}">${escapeHTML(source.name)} · ${escapeHTML(source.kind)}</option>`).join('')}</select><button type="button" data-action="assign-source">Добавить к позиции</button><button type="button" data-action="replace-sources">Заменить набор…</button></div>
      </div>
    </div>`;
  }

  function mediumRow(value = {}) {
    const id = makeId('medium');
    const positions = (value.positions || []).map(positionRow).join('');
    return `<div class="repeat-item" data-medium data-id="${id}">
      <div class="repeat-head"><strong>Носитель</strong>${itemActions('носитель')}</div>
      <div class="repeat-fields">
        <div class="field"><label for="medium-title-${id}">Название носителя (если нужно)</label><input id="medium-title-${id}" data-value="title" value="${escapeHTML(value.title)}"></div>
        <div class="field"><label for="medium-format-${id}">Тип / формат (если известно)</label><input id="medium-format-${id}" data-value="format" value="${escapeHTML(value.format)}"></div>
      </div>
      <div class="positions" data-positions><div class="panel-heading"><h3>Позиции</h3><button type="button" data-action="add-position">Добавить позицию</button></div>${positions || '<p class="empty-list">Позиции не добавлены.</p>'}</div>
    </div>`;
  }

  function updateOrder(container, selector, label) {
    const rows = list(container, selector);
    rows.forEach((item, index) => {
      item.querySelector('.repeat-head strong').textContent = `${label} ${index + 1}`;
      item.querySelector('[data-action="up"]').disabled = index === 0;
      item.querySelector('[data-action="down"]').disabled = index === rows.length - 1;
    });
  }

  function renderCovers(selected = '') {
    covers.innerHTML = data.covers.map(cover => `<div class="cover-card"><label><input type="radio" name="cover-choice" value="${cover.id}" ${cover.id === selected ? 'checked' : ''}><span class="cover-art ${cover.style}">${escapeHTML(cover.mark)}</span><span><strong>${escapeHTML(cover.title)}</strong><br><span class="sub">${escapeHTML(cover.source)}</span></span></label></div>`).join('');
  }

  function renderSourceChips(position, selectedSourceId = position.dataset.selectedSourceId || '') {
    const chipList = position.querySelector('[data-source-chips]');
    const sourceIds = JSON.parse(position.dataset.sourceIds || '[]');
    chipList.innerHTML = sourceIds.length ? sourceIds.map(id => {
      const source = byId(data.sources, id);
      return source ? `<span class="source-chip"><label><input type="radio" name="publish-source-${position.dataset.id}" value="${source.id}" data-selected-source ${source.id === selectedSourceId ? 'checked' : ''}><span><strong>${escapeHTML(source.name)}</strong><br>${escapeHTML(source.kind)} · ${escapeHTML(source.path)}</span></label><button type="button" data-action="unassign-source" data-source-id="${source.id}" aria-label="Снять ${escapeHTML(source.name)}">Снять</button></span>` : '';
    }).join('') : '<span class="sub">Исходники не выбраны для этой позиции.</span>';
    position.dataset.selectedSourceId = sourceIds.includes(selectedSourceId) ? selectedSourceId : '';
  }

  function initPosition(position, value = {}) {
    position.dataset.sourceIds = JSON.stringify(value.sourceIds || []);
    position.dataset.selectedSourceId = value.selectedSourceId || '';
    const select = position.querySelector('[data-recording-select]');
    if (value.recordingId) select.value = value.recordingId;
    updateRecording(position);
    renderSourceChips(position, value.selectedSourceId || '');
  }

  function updateRecording(position) {
    const search = position.querySelector('[data-recording-search]');
    const select = position.querySelector('[data-recording-select]');
    const needle = (search.value || '').trim().toLocaleLowerCase();
    [...select.options].forEach(option => {
      const recording = byId(data.recordings, option.value);
      option.hidden = Boolean(option.value) && !option.selected && Boolean(needle) && !`${recording?.title} ${recording?.artists}`.toLocaleLowerCase().includes(needle);
    });
    position.querySelector('[data-recording-identity]').textContent = `Выбран: ${recordingName(select.value) || 'не выбран'}${select.value ? ` · ${byId(data.recordings, select.value).artists}` : ''}`;
  }

  function setPositionSourceIds(position, ids) {
    const unique = [...new Set(ids)];
    position.dataset.sourceIds = JSON.stringify(unique);
    if (!unique.includes(position.dataset.selectedSourceId)) position.dataset.selectedSourceId = '';
    renderSourceChips(position);
    updateAssetStatus();
  }

  function updateAssetStatus() {
    const assignments = [...root.querySelectorAll('[data-position]')].reduce((count, position) => count + JSON.parse(position.dataset.sourceIds || '[]').length, 0);
    const cover = root.querySelector('input[name="cover-choice"]:checked');
    root.querySelector('#assets-status').textContent = `${assignments} назначений исходников в черновике · ${cover ? `выбрана обложка «${byId(data.covers, cover.value).title}»` : 'обложка не выбрана'}. Всё остаётся локальным демо-состоянием.`;
  }

  function demoDraft() {
    const release = localReleases[0];
    return {
      title: 'Blue Hour',
      metadata: { date: release.year, country: release.country, label: release.label, catalogNumber: release.catalog, type: 'Альбом' },
      artists: [{ name: demoArtist(release.artistId).name, credit: demoArtist(release.artistId).name, role: 'основной артист', artistId: release.artistId, isDemoCatalogArtist: true }],
      media: [{ title: 'Сторона A', format: 'Демо-носитель', positions: release.tracks.slice(0, 2).map(track => ({
        number: String(track.position), title: track.title, duration: `${Math.floor(track.duration / 60)}:${String(track.duration % 60).padStart(2, '0')}`,
        recordingMode: track.recordingId ? 'existing' : 'new', recordingId: track.recordingId || '', newRecordingTitle: track.title,
        sourceIds: track.sourceIds || (track.sourceId ? [track.sourceId] : []), selectedSourceId: track.sourceId || ''
      })) }],
      coverId: `cover-${release.id}`
    };
  }

  function loadDraft(draft) {
    nextId = 1;
    const metadata = draft.metadata || {};
    root.querySelector('#release-title').value = draft.title || '';
    root.querySelector('#release-date').value = metadata.date || '';
    root.querySelector('#release-country').value = metadata.country || '';
    root.querySelector('#release-label').value = metadata.label || '';
    root.querySelector('#release-catalog').value = metadata.catalogNumber || '';
    root.querySelector('#release-type').value = metadata.type || '';
    const artistValues = draft.artists || [];
    const mediumValues = draft.media || [];
    artists.innerHTML = artistValues.map(artistRow).join('') || '<p class="empty-list">Артисты не добавлены.</p>';
    media.innerHTML = mediumValues.map(mediumRow).join('');
    [...media.querySelectorAll('[data-position]')].forEach((position, index) => initPosition(position, mediumValues.flatMap(medium => medium.positions)[index]));
    if (!mediumValues.length) media.innerHTML = '<p class="empty-list">Носители не добавлены.</p>';
    renderCovers(draft.coverId || '');
    updateLists();
    updateAssetStatus();
  }

  function updateLists() {
    updateOrder(artists, '[data-artist]', 'Артист');
    updateOrder(media, '[data-medium]', 'Носитель');
    media.querySelectorAll('[data-positions]').forEach(container => updateOrder(container, '[data-position]', 'Позиция'));
  }

  function readOwnValues(item) {
    const values = {};
    item.querySelectorAll('[data-value]').forEach(input => {
      if (input.closest('[data-position], [data-medium], [data-artist]') === item) values[input.dataset.value] = input.value.trim();
    });
    return values;
  }

  function collect() {
    return {
      title: root.querySelector('#release-title').value.trim(),
      metadata: {
        date: root.querySelector('#release-date').value.trim(), country: root.querySelector('#release-country').value.trim(),
        label: root.querySelector('#release-label').value.trim(), catalogNumber: root.querySelector('#release-catalog').value.trim(),
        type: root.querySelector('#release-type').value.trim()
      },
      artists: list(artists, '[data-artist]').map(item => {
        const values = readOwnValues(item);
        const match = data.artists.find(artist => artist.name.toLocaleLowerCase() === (values.name || '').toLocaleLowerCase());
        return { ...values, artistId: match?.id || null, isDemoCatalogArtist: Boolean(match) };
      }),
      media: list(media, '[data-medium]').map(medium => ({
        ...readOwnValues(medium), positions: list(medium.querySelector('[data-positions]'), '[data-position]').map(position => {
          const mode = position.querySelector('[data-mode]:checked')?.value || 'new';
          const values = readOwnValues(position);
          const recordingId = mode === 'existing' ? position.querySelector('[data-recording-select]').value : '';
          return { ...values, recordingMode: mode, recordingId, recordingTitle: mode === 'existing' ? recordingName(recordingId) : values.newRecordingTitle, sourceIds: JSON.parse(position.dataset.sourceIds || '[]'), selectedSourceId: position.dataset.selectedSourceId || '' };
        })
      })),
      coverId: root.querySelector('input[name="cover-choice"]:checked')?.value || ''
    };
  }

  function moveItem(item, direction) {
    const selector = item.matches('[data-artist]') ? '[data-artist]' : item.matches('[data-medium]') ? '[data-medium]' : '[data-position]';
    const sibling = direction < 0 ? item.previousElementSibling : item.nextElementSibling;
    if (!sibling || !sibling.matches(selector)) return;
    if (direction < 0) item.parentElement.insertBefore(item, sibling); else item.parentElement.insertBefore(sibling, item);
    updateLists();
    item.querySelector('input').focus();
  }

  root.addEventListener('input', event => {
    if (event.target.matches('[data-recording-search]')) updateRecording(event.target.closest('[data-position]'));
    if (event.target.matches('[data-artist][data-value="name"], [data-value="name"]') && event.target.closest('[data-artist]')) {
      const row = event.target.closest('[data-artist]');
      const exact = data.artists.find(artist => artist.name.toLocaleLowerCase() === event.target.value.trim().toLocaleLowerCase());
      const credit = row.querySelector('[data-value="credit"]');
      if (exact && !credit.value) credit.value = exact.name;
      row.querySelector('[data-artist-match]').textContent = artistMatch(event.target.value);
    }
    onDraft(collect());
  });

  root.addEventListener('change', event => {
    if (event.target.matches('[data-mode]')) {
      const position = event.target.closest('[data-position]');
      position.querySelector('[data-new-recording]').classList.toggle('hidden', event.target.value !== 'new');
      position.querySelector('[data-existing-recording]').classList.toggle('hidden', event.target.value !== 'existing');
    }
    if (event.target.matches('[data-recording-select]')) updateRecording(event.target.closest('[data-position]'));
    if (event.target.matches('input[name="cover-choice"]')) updateAssetStatus();
    if (event.target.matches('[data-selected-source]')) {
      const position = event.target.closest('[data-position]');
      position.dataset.selectedSourceId = event.target.value;
    }
    onDraft(collect());
  });

  root.addEventListener('click', event => {
    const button = event.target.closest('button[data-action]');
    if (!button) return;
    const action = button.dataset.action;
    if (action === 'add-artist') {
      artists.querySelector('.empty-list')?.remove();
      artists.insertAdjacentHTML('beforeend', artistRow()); updateLists(); artists.lastElementChild.querySelector('[data-value="name"]').focus(); onDraft(collect()); return;
    }
    if (action === 'add-medium') {
      media.querySelector(':scope > .empty-list')?.remove();
      media.insertAdjacentHTML('beforeend', mediumRow()); updateLists(); media.lastElementChild.querySelector('[data-value="title"]').focus(); onDraft(collect()); return;
    }
    if (action === 'add-position') {
      const container = button.closest('[data-positions]');
      container.querySelector('.empty-list')?.remove();
      container.insertAdjacentHTML('beforeend', positionRow());
      const position = container.lastElementChild;
      initPosition(position); updateLists(); position.querySelector('[data-value="title"]').focus(); onDraft(collect()); return;
    }
    if (action === 'create-release') { onCreate(collect()); return; }
    if (action === 'reset-demo') { loadDraft(demoDraft()); onDraft(collect()); return; }
    if (action === 'clear-cover') { root.querySelectorAll('input[name="cover-choice"]').forEach(input => { input.checked = false; }); updateAssetStatus(); onDraft(collect()); return; }
    if (action === 'assign-source' || action === 'replace-sources') {
      const position = button.closest('[data-position]');
      const select = position.querySelector('[data-source-select]');
      if (!select.value) { select.focus(); root.querySelector('#assets-status').textContent = 'Сначала выберите демонстрационный вариант исходника.'; return; }
      const sourceIds = JSON.parse(position.dataset.sourceIds || '[]');
      if (action === 'replace-sources') {
        const selectedId = select.value;
        askConfirm('Заменить связанные исходники?', `<p>Заменить ${sourceIds.length} уже связанных вариантов на «${escapeHTML(sourceName(selectedId))}»? Публикации не изменятся; выбранный исходник будет снят, если его больше нет в наборе.</p>`, () => {
          setPositionSourceIds(position, [selectedId]);
          select.value = '';
          onDraft(collect());
          select.focus();
        });
        return;
      } else setPositionSourceIds(position, [...sourceIds, select.value]);
      select.value = '';
      onDraft(collect());
      return;
    }
    if (action === 'unassign-source') {
      const position = button.closest('[data-position]');
      setPositionSourceIds(position, JSON.parse(position.dataset.sourceIds || '[]').filter(id => id !== button.dataset.sourceId));
      onDraft(collect());
      (position.querySelector('[data-action="unassign-source"]') || position.querySelector('[data-source-select]')).focus();
      return;
    }
    const item = button.closest('[data-artist], [data-medium], [data-position]');
    if (!item) return;
    if (action === 'up' || action === 'down') { moveItem(item, action === 'up' ? -1 : 1); onDraft(collect()); return; }
    if (action === 'remove') {
      const container = item.parentElement;
      const selector = item.matches('[data-artist]') ? '[data-artist]' : item.matches('[data-medium]') ? '[data-medium]' : '[data-position]';
      const rows = list(container, selector); const index = rows.indexOf(item); const focusTarget = rows[index + 1] || rows[index - 1];
      item.remove();
      if (!list(container, selector).length) container.insertAdjacentHTML('beforeend', `<p class="empty-list">${selector === '[data-artist]' ? 'Артисты' : selector === '[data-medium]' ? 'Носители' : 'Позиции'} не добавлены.</p>`);
      updateLists(); updateAssetStatus();
      onDraft(collect());
      (focusTarget?.querySelector('input') || (selector === '[data-artist]' ? root.querySelector('[data-action="add-artist"]') : selector === '[data-medium]' ? root.querySelector('[data-action="add-medium"]') : container.querySelector('[data-action="add-position"]')))?.focus();
    }
  });

  root.addEventListener('change', event => {
    if (event.target.matches('[data-value="name"]') && event.target.closest('[data-artist]')) {
      const match = data.artists.find(artist => artist.name.toLocaleLowerCase() === event.target.value.trim().toLocaleLowerCase());
      const credit = event.target.closest('[data-artist]').querySelector('[data-value="credit"]');
      if (match && !credit.value) credit.value = match.name;
    }
  });

  root.insertAdjacentHTML('afterbegin', '<datalist id="artist-options"></datalist>');
  root.querySelector('#artist-options').innerHTML = data.artists.map(artist => `<option value="${escapeHTML(artist.name)}"></option>`).join('');
  loadDraft(initial || demoDraft());
};
