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

После интеграции `task design:parity` сравнивает байты файлов из
[реестра пар](../../tools/check-ui-parity.mjs) и выводит путь каждого
расхождения. Это не ограничивает разработку следующей версии макета до её
переноса. В таблице — границы уже перенесённого кода. Внутри этих границ
представление не читает демонстрационные данные или HTTP API: провайдеры
передают ему данные и действия через типизированный контракт.

| Область | Файлы макета относительно `src/` | Копии в `frontend/src/` |
| --- | --- | --- |
| Оболочка, язык и тема | `routes/ShellView.tsx`, `components/LanguageControl.tsx`, `components/ThemeControl.tsx` | Те же пути |
| Источники и операции | `features/sources/SourceRootList.tsx`, `features/sources/SourceScanPanel.tsx`, `features/operations/OperationsList.tsx` | Те же пути |
| Перенос файла | `components/AppButton.tsx`, `features/incoming/IncomingFileActions.tsx` | Те же пути |
| Выбор папки | `components/DirectoryPathFieldView.tsx`, `components/DirectoryPickerView.tsx`, `components/DirectoryPickerMessages.ts` | Те же пути |
| Сопоставление | `features/matching/`: `MatchingBoard.tsx`, `MatchingDnD.tsx`, `MatchingWorkspaceLayout.tsx`, `MatchingEvidenceInspector.tsx`, `MatchingPreview.tsx`, `ManualSearchView.tsx`, `ManualReleasePreparationView.tsx`, `evidenceOverrideMessages.ts`, `index.ts`, `manualReleasePreparationContract.ts`, `manualSearchContract.ts`, `matchingBoardMessages.ts`, `matchingBoardStyles.ts`, `matchingSourcePath.ts`, `scoring.ts`, `sourceFileOrder.ts`, `types.ts` | `features/matchingWorkspace/` |
| Редактор Local-релиза | `features/local-release/`: `LocalReleaseEditor.tsx`, `LocalReleaseForm.tsx`, `LocalReleaseCancelDialog.tsx`, `LocalRecordingArtistField.tsx`, `LocalReleaseRecordingPicker.tsx`, `useLocalReleaseForm.ts`, `localReleaseFormModel.ts`, `localReleaseMessages.ts`, `localReleaseUi.tsx` | Те же пути |
| Собственный каталог | `features/local-catalog/`: `OwnCatalogList.tsx`, `OwnCatalogPagination.tsx`, `OwnCatalogEditorShell.tsx` | `features/own-catalog/` |
| Настройки: вкладки, общие параметры, папки, подтверждение сброса, провайдеры, каталог жанров | `features/settings/SettingsTabs.tsx`, `SettingsGeneralSection.tsx`, `SettingsFoldersSection.tsx`, `SettingsOutputResetDialog.tsx`, `SettingsProvidersSection.tsx`, `GenreCatalogView.tsx` | Те же пути |
| Настройки: карточки пакетов, подтверждение загрузки и сводка переноса | `features/settings/SettingsManagedPackageCards.tsx`, `SettingsToolInstallDialog.tsx`, `SettingsToolsMoveReview.tsx` | Те же пути |
| Настройки и первый запуск: форма токена GitHub | `features/tools/GitHubTokenFormView.tsx`, `toolMessages.ts` | Те же пути |
| Первый запуск: каркас мастера, поля выбора папок и выбор версии инструмента | `features/setup/SetupWizard.tsx`, `SetupDirectoryFields.tsx`, `SetupToolReleasePicker.tsx` | Те же пути |
| Медиатека: карточки артистов и проверка выбранных позиций | `features/library/LibraryArtistCards.tsx`, `libraryArtistCardStyles.ts`, `LibrarySelectionReviewView.tsx` | Те же пути |

Это не утверждение, что экраны целиком используют одинаковый код. В частности,
выборки каталога, управление операциями и получение жанров остаются в отдельных
адаптерах. Во вкладке инструментов ещё различаются действия над каталогом,
редактирование нового пути и оболочка диалога переноса. Приложение открывает
подтверждение загрузки только для конфликтного preflight и показывает серверные
пути; макет показывает имена
проверяемых программ, не имитируя серверные пути. Форма токена общая: макет
имитирует сохранение и проверку без запросов в GitHub, а приложение выполняет
их на сервере. Не переносились остальные диалоги настроек.
В первом запуске совпадают каркас и отдельные поля, но не порядок
шагов и не полные тела шагов. Редакторы медиатеки и предпросмотр публикации
пока не перенесены дословно.

## Проверка

Для макета и документации используются `task design:check` и живой браузер.
Проверяются общие сценарии, draft, ошибки, focus, клавиатура, обе темы и локали;
основной визуальный приоритет — удобный плотный desktop от 1400 px. Проверки
не являются полным WCAG-аудитом или production-приёмкой. Screenshot-артефакты
не сохраняются.

Исторические результаты прогонов находятся в [отчётах](../reports/), а статус
реализации приложения — в [планах](../plans/). Они не являются параллельным
контрактом и не используются как текущий перечень возможностей макета.
