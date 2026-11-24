# Границы графического эталона

Самостоятельный React-макет — текущий графический эталон. Продуктовые правила
задаёт [единый контракт](../design/decisions.md), композицию экранов —
[описание интерфейса](02-screens.md). Наличие компонента или успешная проверка
не означает визуальной приёмки владельцем либо готовности production.

## Что моделируется

Оболочка, темы и RU/EN, медиатека, Local provider CRUD, входящие и назначения,
единая поверхность публикации, источники, текущие операции, Setup и Settings
работают с общим состоянием в памяти. Сетевые API и настоящая файловая система
не вызываются. Перезагрузка страницы сбрасывает domain-состояние. Исключение —
несохранённый несекретный текст настроек: браузерный черновик восстанавливается
в пределах вкладки, не превращаясь в сохранённую настройку. Конфликт с изменёнными
сохранёнными значениями проверяется на входных settings; настоящая синхронизация
устройств и серверная конкурентная запись остаются задачей production API.
`sessionStorage` соответствует обычной сессии вкладки, но восстановление закрытых
вкладок/сессий самим браузером может восстановить и его содержимое. Макет не
обещает гарантированного стирания браузерных данных после закрытия.

Matching использует явно демонстрационные verdict, не вычисляет настоящий
confidence. Recovery, tool verification, path checks и операции показывают
сценарии на фиксированных данных, а не обещают исполнение backend lifecycle.
Сводка и карточка релиза используют пути из демонстрационной модели; изменённый
шаблон публикации требует production-планировщика и не имитируется.
Проверки удаления исходника по всем назначенным сегментам и исходов managed
deletion выражены тестируемыми моделями; реальные unlink, commit БД и их сбои
не выполняются. В fixtures нет отдельного редактора границ сегментов: правило
cleanup показано в настройках, без добавления кнопок имитации или выдуманного
происхождения данных.
Реестр metadata описывает целевое покрытие; отсутствующее поле backend не отменяет
требование продукта. Fixtures и их ограничения — в [README](README.md).

Строка провайдерских жанров (`providerGenreBaseline`) в макете — только
контролируемая презентация принятого baseline: production получает его из явного
source channel, а fixture макета такого происхождения не содержит. `undefined` не
показывает строку, `null` — недоступность, пустой список — принятый пустой
результат; строка readonly и не участвует в черновике или сохранении локальных
жанров. Тесты используют реальные ссылки каталога жанров, не выдумывая provenance.

## Перенос исходников

`task design:parity` сравнивает байты файлов из
[реестра пар](../../tools/check-ui-parity.mjs) и выводит путь каждого расхождения.
Эта же проверка запускается в `task design:check` и через неё — в `task verify`.
Если согласованная работа над следующим дизайном временно опережает приложение,
запишите причину и обе контрольные суммы SHA-256 (`null` для ещё не перенесённого
файла приложения) для конкретной пары в
[`ui-parity-prototype-leads.json`](../../tools/ui-parity-prototype-leads.json).
Удалите запись при переносе. Это не исключение для расхождений, появившихся
сначала в приложении: проверка подтверждает указанные байты, а направление
изменений и ещё не перенесённый статус подтверждаются при ревью.

В таблице — уже перенесённый код, а не граница требования. Для обзора остального
запустите `task design:parity -- --inventory`. Проверка требует учесть каждый
новый файл в `src`: зарегистрировать пару либо явно добавить провайдер в
[реестр непарных файлов](../../tools/ui-parity-unpaired.txt). Этот реестр
также содержит пока не перенесённые файлы: запись в нём не подтверждает паритет
и не проверяет содержимое. Удаляйте записи при переносе. Тесты, сгенерированный
клиент и документация не входят в инвентарь UI. Представление не должно читать
моки или HTTP API: провайдеры передают данные и действия через типизированный
контракт.

| Область | Файлы макета относительно `src/` | Копии в `frontend/src/` |
| --- | --- | --- |
| Оболочка, язык и тема | `routes/ShellView.tsx`, `components/LanguageControl.tsx`, `components/ThemeControl.tsx` | Те же пути |
| Глобальные токены, типографика, фокус и цветовая схема | `app/tailwind.css`, `index.html` | `styles/index.css`, `index.html` (отличаются только заголовок и точка входа) |
| Базовая локализация и предпочтение темы | `i18n/{I18nProvider.tsx,createNamespace.ts,locale.ts,translator.ts,types.ts,namespaces/shell.ts}`, `components/themePreference.ts` | Те же пути |
| Источники: список, заголовок корня, сканирование и инвентарь | `features/sources/SourceRootList.tsx`, `SourceRootHeadingView.tsx`, `SourceScanPanel.tsx`, `SourceInventoryPanel.tsx`, `SourceInventoryView.tsx` | Те же пути |
| Источники: форма регистрации каталога | `features/sources/SourceRegistrationView.tsx` | Тот же путь |
| Источники: инспектор файла, этапы анализа и технический результат | `features/sources/SourceInspectorView.tsx`, `SourceAnalysisSteps.tsx`, `SourceTechnicalResult.tsx`, `sourceInspectorMessages.ts`, `sourceInspectorModel.ts` | Те же пути |
| Источники: порядок секций карточки корня | `features/sources/SourceRootDetailView.tsx` | Тот же путь |
| Источники: редактирование имени, пути и режима | `features/sources/SourceRootSettingsView.tsx` | Тот же путь |
| Источники: сводка корня, действие и подтверждение удаления | `features/sources/SourceRootOverviewView.tsx`, `SourceRootRemovalDialog.tsx` | Те же пути |
| Источники: пакетный выбор, подтверждение и результаты очистки временных копий | `features/sources/SourceArtifactCleanupView.tsx` | Тот же путь |
| Операции: список, фильтры, поиск, пагинация, раскрытие и подтверждение прерывания перепубликаций | `features/operations/OperationsList.tsx`, `OperationsScreenView.tsx`, `operationsScreenCopy.ts`, `MatchingRecheckAbandonDialog.tsx` | Те же пути |
| Входящие: обзор групп и действия с файлами | `features/incoming/IncomingGroupsOverviewView.tsx`, `IncomingFileActions.tsx`, `components/AppButton.tsx` | Те же пути |
| Выбор папки | `components/DirectoryPathFieldView.tsx`, `components/DirectoryPickerView.tsx`, `components/DirectoryPickerMessages.ts` | Те же пути |
| Сопоставление | `features/matching/`: `MatchingBoard.tsx`, `MatchingDnD.tsx`, `MatchingWorkspaceLayout.tsx`, `MatchingEvidenceInspector.tsx`, `MatchingPreview.tsx`, `ManualSearchView.tsx`, `ManualReleasePreparationView.tsx`, `evidenceOverrideMessages.ts`, `index.ts`, `manualReleasePreparationContract.ts`, `manualSearchContract.ts`, `matchingBoardMessages.ts`, `matchingBoardStyles.ts`, `matchingSourcePath.ts`, `scoring.ts`, `sourceFileOrder.ts`, `types.ts` | `features/matchingWorkspace/` |
| Сопоставление: секция поиска, подготовки и создания издания | `features/matching/MatchingReviewSearchView.tsx` | `features/matchingWorkspace/MatchingReviewSearchView.tsx` |
| Редактор Local-релиза | `features/local-release/`: `LocalReleaseEditor.tsx`, `LocalReleaseForm.tsx`, `LocalReleaseCancelDialog.tsx`, `LocalRecordingArtistField.tsx`, `LocalReleaseRecordingPicker.tsx`, `useLocalReleaseForm.ts`, `localReleaseFormModel.ts`, `localReleaseMessages.ts`, `localReleaseUi.tsx` | Те же пути |
| Собственный каталог: списки, карточки записей и поля форм артиста и композиции | `features/local-catalog/`: `OwnCatalogList.tsx`, `OwnCatalogPagination.tsx`, `OwnCatalogEditorShell.tsx`, `OwnCatalogDetailsView.tsx`, `OwnCatalogRecordFieldsView.tsx` | `features/own-catalog/` |
| Настройки: вкладки, общие параметры, папки, подтверждение сброса, провайдеры, каталог жанров | `features/settings/SettingsTabs.tsx`, `SettingsGeneralSection.tsx`, `SettingsFoldersSection.tsx`, `SettingsOutputResetDialog.tsx`, `SettingsProvidersSection.tsx`, `GenreCatalogView.tsx` | Те же пути |
| Настройки: каркас страницы, заголовок и подзаголовок | `features/settings/SettingsPageFrame.tsx` | Тот же путь |
| Настройки: раздел инструментов, карточки пакетов, подтверждение загрузки, поля и подтверждение переноса | `features/settings/SettingsToolsOverview.tsx`, `SettingsManagedPackageCards.tsx`, `SettingsToolInstallDialog.tsx`, `SettingsToolsMoveEditor.tsx`, `SettingsToolsMoveReview.tsx`, `SettingsToolsMoveReviewDialog.tsx` | Те же пути |
| Настройки: выход с черновиком и подтверждение замены сохранённого значения | `features/settings/SettingsLeaveDialog.tsx`, `SettingsConflictDialog.tsx` | Те же пути |
| Настройки и первый запуск: форма токена GitHub | `features/tools/GitHubTokenFormView.tsx`, `toolMessages.ts` | Те же пути |
| Первый запуск: порядок шагов, каркас мастера, поля папок, карточки инструментов, формат публикации, MusicBrainz, загрузка каталога жанров и итоговая сводка | `features/setup/SetupWizard.tsx`, `SetupDirectoryFields.tsx`, `SetupToolCardsView.tsx`, `SetupToolReleasePicker.tsx`, `SetupPublicationOptions.tsx`, `SetupMusicBrainzFields.tsx`, `SetupGenreCatalogView.tsx`, `SetupSummaryView.tsx` | Те же пути |
| Медиатека: карточки артистов, списки альбомов и треков, позиции издания, выбор издания, вкладки, поиск, фильтр, пагинация и панель выбора | `features/library/LibraryArtistCards.tsx`, `libraryArtistCardStyles.ts`, `LibraryAlbumListView.tsx`, `LibraryTrackListView.tsx`, `LibraryAlbumPositionsView.tsx`, `LibraryMissingEditionView.tsx`, `LibraryCatalogControls.tsx`, `LibraryCatalogFilterView.tsx`, `LibraryPaginationView.tsx`, `LibrarySelectionDockView.tsx`, `LibrarySelectionReviewView.tsx` | Те же пути |
| Медиатека: редактор карточки артиста, выбор жанров и действия редакторов метаданных | `features/library/ArtistMetadataCard.tsx`, `GenrePicker.tsx`, `genreMessages.ts`, `MetadataCardActions.tsx` | Те же пути |
| Медиатека: строка принятых жанров MusicBrainz в редакторе издания | `features/library/ProviderGenreBaselineRow.tsx` | Тот же путь |
| Медиатека: значения части произведения — название, всего частей, номер | `features/library/MovementFieldsView.tsx` | Тот же путь |
| Медиатека: заголовок артиста, сведения о позиции и её источниках, простые поля метаданных и снимок тегов | `features/library/LibraryArtistHeadingView.tsx`, `LibraryPositionIdentityView.tsx`, `LibraryPositionSourcesView.tsx`, `MetadataFieldPresentation.tsx`, `TagSnapshotView.tsx` | Те же пути |
| Публикация: сводка по выбору, детали плана релиза, трека и входящих, сведения и измерения громкости CURRENT | `features/library/LibrarySelectionPreviewView.tsx`, `AggregatePublicationPreviewView.tsx`, `PublicationPreviewDetailsView.tsx`, `firstPublicationPreviewMessages.ts`, `PublicationCurrentSummaryView.tsx`, `PublicationAudioMeasurementsView.tsx` | Те же пути |

### Где ещё есть расхождения

- **Результат сопоставления во «Входящих».** `MatchingLinkResultView.tsx`,
  `MatchingReceiptPublicationFrame.tsx` и `matchingLinksCopy.ts` в макете и
  приложении совпадают по байтам. Провайдер макета планирует и исполняет
  публикацию только на демонстрационных данных; приложение получает точное
  решение и подтверждает позиции через сервер. Ответы сервера о проверках
   и ходе операций не заменяются демонстрационными результатами.
- **Первый запуск.** Порядок шагов и показ загрузки обязательного каталога
  жанров общие. Макет загружает демонстрационный каталог; приложение ждёт
  подтверждённый сервером результат для проверенной конфигурации MusicBrainz.
  Часть композиции остальных провайдеров и вывод ошибок проверки ещё различаются.
- **Источники.** Инспектор открывается из инвентаря в обеих сборках.
  Провайдер макета использует состояние демонстрационного анализа и не
  подставляет отсутствующие измерения, отпечатки и исходный вывод ffprobe.
  Диалог удаления корня использует одинаковое представление; приложение
  подтверждает путь и число записей через сервер, макет проверяет свои
  демонстрационные данные. Очистка временных копий представлена общим списком:
  провайдер приложения получает допустимые записи и результаты от сервера;
  макет меняет только свои данные. Сводка корня общая, в том числе показ
  прежнего пути инвентаря при наличии серверных данных. Форму регистрации
  каталогов используют обе сборки; проверка и открытие нового каталога
  выполняются провайдерами. Порядок секций и панель редактирования карточки
  общие. Инвентарь использует одинаковую разметку и тексты состояний; страницы
  и диагностику чтения приложение получает от сервера. Подробные шаги обработки
  и их повтор находятся в инспекторе, без отдельной панели в карточке каталога.
- **Медиатека, входящие и публикация.** Общие строки и части рабочих областей
  уже перенесены. В композиции маршрутов, редакторах структурированных метаданных,
  диалогах и каталогах переводов остаются разные реализации. Перед копированием
  нужно отделить представление и форматирование от загрузки, черновиков и
  записи, чтобы не заменить серверный workflow демонстрационным.
- **Стили.** Рукописные стили отдельных экранов в `frontend/src/features/`
  ещё надо заменить соответствующими стилями макета. Одинаковый компонент
  может выглядеть по-разному при разных глобальных стилях или версиях библиотек.

Реальный HTTP, SSE, файловая система, in-memory fixtures и результаты
preflight не должны копироваться между провайдерами. В тестах обеих копий
можно использовать разные способы подготовки данных.

## Проверка

Для макета и документации используются `task design:check` и живой браузер.
Проверяются общие сценарии, draft, ошибки, focus, клавиатура, обе темы и локали;
основной визуальный приоритет — удобный плотный desktop от 1400 px. Проверки
не являются полным WCAG-аудитом или production-приёмкой. Screenshot-артефакты
не сохраняются.

Исторические результаты прогонов находятся в [отчётах](../reports/), а статус
реализации приложения — в [планах](../plans/). Они не являются параллельным
контрактом и не используются как текущий перечень возможностей макета.
