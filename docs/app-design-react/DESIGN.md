---
name: MeloTrove prototype
description: "Нейтральный каталожный интерфейс на React Aria: бумажные поверхности, графит и один рабочий синий."
colors:
  canvas: "#fafafa"
  canvas-dark: "#0a0a0a"
  surface: "#ffffff"
  surface-dark: "#171717"
  raised: "#f5f5f5"
  raised-dark: "#262626"
  ink: "#171717"
  ink-dark: "#f5f5f5"
  muted: "#525252"
  muted-dark: "#a3a3a3"
  line: "#d4d4d4"
  line-dark: "#404040"
  control: "#8a8a8a"
  control-dark: "#737373"
  accent: "#2563eb"
  accent-dark: "#60a5fa"
  accent-strong: "#1d4ed8"
  accent-strong-dark: "#93c5fd"
  accent-soft: "#dbeafe"
  accent-soft-dark: "#1e3a8a4d"
  button: "#2563eb"
  button-dark: "#2563eb"
  button-hover: "#1d4ed8"
  button-hover-dark: "#1d4ed8"
  button-text: "#ffffff"
  button-text-dark: "#ffffff"
  focus: "#2563eb"
  focus-dark: "#60a5fa"
  warning: "#92400e"
  warning-dark: "#fbbf24"
  warning-soft: "#fef3c7"
  warning-soft-dark: "#451a03"
  error: "#b91c1c"
  error-dark: "#f87171"
  error-soft: "#fee2e2"
  error-soft-dark: "#450a0a"
  info: "#1d4ed8"
  info-dark: "#93c5fd"
  info-soft: "#dbeafe"
  info-soft-dark: "#172554"
  success: "#047857"
  success-dark: "#34d399"
  success-soft: "#d1fae5"
  success-soft-dark: "#022c22"
typography:
  title:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "26px"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  title-compact:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "22px"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  heading-compact:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: 1.3
  headline:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.56
  section:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.5
    letterSpacing: "-0.01em"
  brand:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "15px"
    fontWeight: 600
  body:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  control:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "14px"
    fontWeight: 500
    lineHeight: 1.5
  secondary:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.54
  label:
    fontFamily: "system-ui,-apple-system,'Segoe UI',sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.33
  mono:
    fontFamily: "ui-monospace,SFMono-Regular,Consolas,monospace"
    fontSize: "11px"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  sm: "4px"
  control: "8px"
  panel: "12px"
  dialog: "16px"
  pill: "9999px"
spacing:
  base: "4px"
  xs: "8px"
  sm: "12px"
  md: "16px"
  lg: "20px"
  xl: "24px"
  xxl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.button}"
    textColor: "{colors.button-text}"
    rounded: "{rounded.control}"
    height: "36px"
    padding: "0 12px"
    typography: "{typography.control}"
  button-primary-hover:
    backgroundColor: "{colors.button-hover}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    height: "36px"
    padding: "0 12px"
    typography: "{typography.control}"
  button-secondary-hover:
    backgroundColor: "{colors.raised}"
  button-danger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.error}"
    rounded: "{rounded.control}"
    height: "36px"
    padding: "0 12px"
    typography: "{typography.control}"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    height: "36px"
    padding: "0 12px"
    typography: "{typography.body}"
  nav-item:
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    height: "40px"
    padding: "8px 12px"
    typography: "{typography.control}"
  nav-item-active:
    backgroundColor: "{colors.accent-soft}"
    textColor: "{colors.accent-strong}"
  status-badge:
    rounded: "{rounded.pill}"
    padding: "2px 8px"
    typography: "{typography.label}"
  table-header:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.muted}"
    padding: "10px 16px"
    typography: "{typography.label}"
  table-cell:
    padding: "12px 16px"
    typography: "{typography.body}"
  table-row-selected:
    backgroundColor: "{colors.accent-soft}"
  gallery-card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
    padding: "16px"
  gallery-card-selected:
    backgroundColor: "{colors.accent-soft}"
  breadcrumb:
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    typography: "{typography.body}"
  breadcrumb-current:
    textColor: "{colors.ink}"
    typography: "{typography.control}"
  panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
    padding: "20px"
  dialog:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.dialog}"
    padding: "24px"
    width: "560px"
---

# Визуальная система MeloTrove prototype

Продуктовые требования и границы `src/ui/` задаёт
[канонический контракт](../design/decisions.md#интерфейс). Здесь зафиксирована
только визуальная система самостоятельного React-макета.

## Overview

**Creative North Star: «Ясный каталожный стол»**

Макет устроен как рабочий стол каталогизатора, а не как дашборд. Белая бумага,
графитовый текст и тонкие линии держат содержание; записи, таблицы и карточки
остаются главными, а действия стоят рядом с теми записями, которые они меняют.
Плотность и поведение таблиц наследуют выбранному примеру React Aria CRUD;
медиатека использует отдельный GridList-рисунок с плиткой-обложкой, названием,
описанием и множественным выбором.

Синий — рабочий сигнал, а не украшение. Он появляется в выборе, ссылках,
активной навигации и фокусе, но не заливает фоны и не спорит с содержимым.
Декоративные KPI-карточки, крупные hero-блоки, стекло, градиенты и тяжёлые тени
исключены: глубина строится лестницей поверхностей и линиями в 1 px.

Компоновка desktop-first: устойчивый shell, компактные контролы высотой 36 px,
таблицы с локальной прокруткой и галерея с автоматической сеткой. Светлая и
тёмная темы сохраняют одну иерархию; русская и английская локали равноправны.

**Key Characteristics:**
- Бумажные нейтрали `canvas` / `surface` / `raised` и графитовый текст.
- Один синий акцент: выбор, ссылки, активный пункт, фокус.
- Плотные рабочие поверхности: таблицы, карточки, контролы 36 px.
- Семантические статусы-пилюли, а не цвета обложек.
- Плоскость по умолчанию; тени только у слоёв поверх страницы и редких
  приподнятых панелей.
- Паритет светлой и тёмной тем, RU/EN.

## Colors

Палитра почти монохромна: бумага и графит несут интерфейс, синий обозначает
действие и выбор. В скобках — значения светлой и тёмной тем; полный набор
токенов собран в frontmatter.

### Primary
- **Каталожный синий** (#2563eb / #60a5fa): ссылки, активный пункт навигации,
  выбранные строки и карточки. Главный сигнал системы.
- **Глубокий синий** (#1d4ed8 / #93c5fd): текст ссылок и активных пунктов на
  мягкой синей подложке, hover primary-кнопки.
- **Синяя подложка** (#dbeafe / #1e3a8a4d): выбранная строка таблицы, выбранная
  карточка галереи, активный пункт навигации.
- **Кнопочный синий** (#2563eb / #2563eb): фон primary-кнопки, текст — #ffffff
  в обеих темах; hover — глубокий синий в обеих темах.
- **Фокус** (#2563eb / #60a5fa): внешнее кольцо 2 px с отступом 2 px.

### Neutral
- **Холст** (#fafafa / #0a0a0a): фон рабочей области и страницы.
- **Лист** (#ffffff / #171717): панели, таблицы, карточки, поля ввода.
- **Приподнятая поверхность** (#f5f5f5 / #262626): заголовки таблиц, hover строк
  и вторичных кнопок, вторичные плашки.
- **Графит** (#171717 / #f5f5f5): основной текст и заголовки.
- **Серый текст** (#525252 / #a3a3a3): подписи, метаданные, placeholder,
  неактивные пункты навигации.
- **Линия** (#d4d4d4 / #404040): границы панелей, таблиц и разделители.
- **Контур** (#8a8a8a / #737373): граница полей в покое и неактивные иконки.

### Status
- **Тёплое предупреждение** (#92400e / #fbbf24) на подложке
  (#fef3c7 / #451a03): предупреждения, неполные данные и ограниченная область
  провайдера.
- **Красная ошибка** (#b91c1c / #f87171) на подложке (#fee2e2 / #450a0a):
  ошибки, конфликты и опасные действия.
- **Информационный синий** (#1d4ed8 / #93c5fd) на подложке
  (#dbeafe / #172554): нейтральные пояснения; значение совпадает с
  `accent-strong`, но роль и семантика отдельные.
- **Зелёный успех** (#047857 / #34d399) на подложке (#d1fae5 / #022c22):
  завершённые операции и опубликованные записи.

### Named Rules
**The One Blue Rule.** Синий занимает не более десятой части экрана. Он отмечает
выбор, ссылку, активный пункт и фокус; если синего много, интерфейс теряет
иерархию.

**The Surface Ladder Rule.** Глубина — это `canvas` → `surface` → `raised`, а не
тень. Новая поверхность сначала получает место в этой лестнице и линию в 1 px.

**The Semantic Status Rule.** Статус всегда несёт текстовую подпись и цвет
одновременно. Цвет обложки или палитра артиста не становятся статусом.

## Typography

**Display Font:** нет отдельного display-шрифта; иерархию держат размер и вес.
**Body Font:** system-ui,-apple-system,'Segoe UI',sans-serif.
**Label/Mono Font:** ui-monospace,SFMono-Regular,Consolas,monospace — только для
путей к файлам.

**Character:** нейтральный системный sans-serif с плотной строкой 14/1.5.
Заголовки крупнее, но не декоративнее; числа в таблицах выровнены табличными
цифрами, поэтому каталог читается как рабочий инструмент.

### Hierarchy
- **Page title** (600, 26 px / 1.2, tracking −0.02 em): заголовок страницы.
- **Compact page title** (600, 22 px / 1.2, tracking −0.02 em): заголовок
  страницы в `uiStyles` и каталоге на ширине до 700 px.
- **Compact heading** (600, 20 px / 1.3): h1 Incoming; крупные диалоги
  используют 20 px с line-height 1.4.
- **Headline** (600, 18 px / 1.56): заголовок диалога и крупной секции.
- **Title** (600, 16 px / 1.5, tracking −0.01 em): заголовок панели и группы.
- **Brand / edition title** (600, 15 px): бренд shell и заголовок издания;
  line-height 1.3 в карточке, 1.33–1.5 в shell.
- **Body** (400, 14 px / 1.5): основной текст, таблицы, поля; строка до 75 ch.
- **Control** (500, 14 px / 1.5): кнопки, навигация и подписи контролов.
- **Secondary** (400, 13 px / 1.54): вторичные строки Incoming, пути и статусы.
- **Label** (500, 12 px / 1.33): заголовки колонок, статусы, метаданные,
  вспомогательный текст.
- **Mono** (400, 11 px / 1.5): пути к файлам и технические значения.

**Scale:** 11 / 12 / 13 / 14 / 15 / 16 / 18 / 20 / 22 / 24 / 26 / 30 px.
11–18 px — рабочие роли; 13 и 15 px — точечные secondary и brand; 20–22 px —
компактные заголовки; 24–30 px — крупные числа, отдельные заголовки и монограмма
обложки.

### Named Rules
**The Tabular Rule.** Числа, счётчики и таблицы используют
`font-variant-numeric: tabular-nums`; моноширинный шрифт не применяется к прозе.

## Layout

Shell — сетка `240px minmax(0,1fr)` по колонкам и `56px minmax(0,1fr)` по
строкам: боковая навигация и верхняя панель неподвижны, прокручивается только
`main`. Страница ограничена 1500 px, поля 24 px; на ширине до 700 px поля
уменьшаются до 12–16 px. До 599 px shell складывается в колонку: навигация
становится горизонтальной полосой с видимым скроллбаром и стрелками направления,
верхняя панель — 52 px; desktop-rail не меняется.

Базовый ритм — 4 px; рабочие шаги 8/12/16/20/24/32. Заголовок страницы отделён
линией, отступ снизу 20 px и до содержимого 24 px. Таблицы занимают всю ширину,
прокручиваются внутри своего контейнера; в каталожной таблице колонка выбора —
48 px, в компактном списке треков — 34 px, ячейки — 12/16 px, заголовки —
10/16 px. Галерея Artists не имеет внешней рамки: обёртка `min-w-0 py-1`
с `overflow-visible`; у табличных видов рамка `catalogueStyles.list` остаётся
с `overflow-hidden`. Сетка галереи — `repeat(auto-fill, minmax(min(100%,260px),1fr))`
с зазором 16 px. Формы идут одной колонкой: подпись, контрол 36 px, помощь или
ошибка 12 px. Канонический диалог ограничен 560 px, отдельные — до 620 px,
DirectoryPicker — 640 px; overlay затемняет фон на 45 %, у DirectoryPicker — на
50 %.

## Elevation & Depth

Система плоская по умолчанию. Панели, строки и карточки не отбрасывают тень:
их отделяют линия в 1 px и смена поверхности. Основная тень появляется у слоёв,
которые физически перекрывают страницу, — поповеров, диалогов и подсказок;
тонкая тень 4 % остаётся редким исключением для отдельных приподнятых панелей.

### Shadow Vocabulary
- **Тонкая тень панели** (`0 1px 2px rgb(0 0 0 / 0.04)`): редкие приподнятые
  блоки и итоговые панели.
- **Поповер** (`0 12px 32px rgb(0 0 0 / 0.14)`): меню выбора и списки.
- **Диалог** (`0 16px 48px rgb(0 0 0 / 0.2)`): стандартные диалоги и
  DirectoryPicker.
- **Крупный диалог** (`0 24px 64px rgb(0 0 0 / 0.24)`): ActionModal и крупные
  модальные формы.
- **Подсказка** (`0 8px 24px rgb(0 0 0 / 0.18)`): tooltip.

### Named Rules
**The Flat-By-Default Rule.** Покой — почти всегда плоско. Тень означает слой
поверх страницы; тонкая тень 4 % допустима только у отдельной приподнятой
панели, а не как знак важности содержимого.

## Shapes

Форма сдержанная: прямоугольники с мягкими углами и границей в 1 px. Радиусы
растут вместе с площадью: 4 px у чекбоксов и хлебных крошек, 8 px у контролов,
полей, кнопок, навигации и рамок таблиц, 12 px у панелей, поповеров и карточек
галереи, 16 px у диалогов. Пилюли — только статусы и счётчики, не кнопки.
Локальные 6 px у компактных вкладок и кнопки очистки — исключения экранов, не
новые токены.

Фокус — внешнее кольцо 2 px с отступом 2 px; у полей ввода отступ 0, чтобы
кольцо не расходилось с границей (inline-поле пути в DirectoryPicker — 2 px).
Обложка-плитка — 48 px с радиусом 8 px;
крупная обложка артиста — 72 px, 96 px на широком экране, радиус 12 px.
Никаких скошенных углов, стеклянных слоёв и декоративной обводки.

## Components

### Buttons
- **Shape:** высота 36 px, радиус 8 px, граница 1 px, горизонтальные поля 12 px,
  зазор 8 px, текст 14/500.
- **Primary:** фон `button`, текст `button-text`; hover и pressed — `button-hover`.
- **Secondary:** фон `surface`, текст `ink`, граница `line`; hover — `raised` и
  граница `control`.
- **Ghost / Icon:** прозрачный фон, квадрат 36 px; hover — `raised`.
- **Danger:** фон `surface`, текст `error`, граница — смесь `error` 35 % с `line`;
  hover — `error-soft` и граница `error`.
- **Focus / Disabled:** кольцо 2 px `accent` с отступом 2 px; disabled —
  `raised` / `muted`, курсор `not-allowed`. Компактный размер — 32 px,
  pointer-coarse — 40 px.

### Inputs / Fields
- **Style:** высота 36 px (pointer-coarse — 40 px), радиус 8 px, граница 1 px
  `control`, фон `surface`, текст `ink`, placeholder `muted`, поля 12 px.
- **Hover / Focus:** hover — граница `ink`; focus — граница `accent` и
  кольцо 2 px `accent` с отступом 0.
- **Error / Disabled:** `aria-invalid` — граница `error`; disabled — фон `raised`,
  текст `muted`.
- **Select:** триггер выбора — те же 36 px и радиус 8 px, граница `control`;
  hover — граница `ink` и фон `raised`.
- **Search:** ведущая иконка 16 px слева на 12 px, кнопка очистки 28 px справа.
- **TextField mapping:** подпись 14/500, помощь и ошибка 12 px; тот же размер и
  радиус используют `AppSearchField` и поле пути в `DirectoryPickerView`.

### Selection Controls
- **Checkbox:** поле 16–20 px, радиус 4 px (в плотной таблице — 3 px), граница
  `control`, фон `surface`; выбранный — фон `accent`, знак цветом `button-text`;
  подпись 14/500, помощь 12 px `muted`; фокус — кольцо 2 px `accent` с отступом
  2 px; disabled — прозрачность 60 %.
- **Switch:** трек 36 × 20 px, радиус 9999 px, граница `control`, фон `raised`;
  бегунок 16 px на `surface` центрируется по вертикали, отступ от края 2 px.
  В выбранном состоянии трек `accent`, бегунок сдвинут на 16 px; disabled —
  прозрачность 60 %.
- **Toolbar:** flex-строка с переносом и зазором 8 px; поиск и фильтры — группа
  с подписью, ряд только с действиями — React Aria `Toolbar`.
- **Активные фильтры:** изменённые условия показаны съёмными метками рядом
  с результатами. Сброс возвращает значения по умолчанию для текущего списка.

### Navigation
- **Rail:** 240 px, фон `surface`, правая граница `line`, поля 20 px по вертикали
  и 16 px по горизонтали. Бренд — плитка 36 px с фоном `button` и музыкальной
  иконкой 20 px.
- **Item:** минимум 40 px, радиус 8 px, поля 8/12 px, текст 14/500 `muted`,
  иконка 18 px, зазор 12 px. Hover — `raised` и `ink`; текущий —
  `accent-soft` и `accent-strong`.
- **Count:** пилюля 20 px, фон `surface`, текст 12 px `muted`, кольцо `line`.
- **Top bar:** 56 px; хлебные крошки 14 px `muted`, разделитель 14 px `control`,
  текущий элемент 14/500 `ink`. До 599 px бренд скрыт; навигация — горизонтальная
  полоса с видимым скроллбаром и стрелками направления, desktop-rail не меняется.
- **Breadcrumbs mapping:** React Aria `Breadcrumbs` / `Breadcrumb` / `Link`;
  ссылка подчёркивается на hover, текущая страница — без ссылки.

### Tables and Lists
- **Frame:** радиус 8 px, граница 1 px `line`, фон `surface`, `overflow-hidden`;
  прокрутка — во внутреннем контейнере.
- **Header:** фон `raised`, нижняя граница `line`, поля 10/16 px, текст 12/500
  `muted`, tabular numerals.
- **Cell:** поля 12/16 px, текст 14/1.5, вертикальное выравнивание по центру,
  перенос длинных значений. Между строками — линия `line`.
- **States:** hover строки — `raised`; выбранная — `accent-soft`; ссылки —
  `accent-strong` с подчёркиванием на hover.
- **Mapping:** React Aria `Table` / `TableHeader` / `Column` / `Row` / `Cell`.
  На узком экране `LibraryTrackListView` показывает те же данные карточками.
- **Действия:** основная команда остаётся на виду, второстепенные команды
  собраны в меню `⋯`. Переходы вроде «Сопоставить» оформляются как кнопки,
  сохраняя семантику ссылки.
- **Раскрытие:** строки групп и изданий используют шеврон слева и показывают
  подробности под строкой. «Открыть издание» — отдельный переход.
- **Форматы:** кодек или контейнер выделен синей меткой (например, `MKA · FLAC`),
  а измеряемые свойства — нейтральной. Эти метки не обозначают статус.

### Gallery Cards
- **Wrapper:** у Artists внешняя обёртка без рамки — `min-w-0 py-1`,
  `overflow-visible`; рамка принадлежит карточкам. Табличные виды сохраняют
  `catalogueStyles.list` с границей и `overflow-hidden`.
- **Grid:** `repeat(auto-fill, minmax(min(100%,260px),1fr))`, зазор 16 px.
- **Card:** радиус 12 px, граница 1 px `line`, фон `surface`, поля 16 px,
  внутренний зазор 12 px. Hover — граница `control` и фон `raised`; выбранная —
  граница `color-mix(accent 45%, line)` и фон `accent-soft`; фокус — кольцо 2 px
  `accent` с отступом 2 px.
- **Artwork:** плитка 48 px, радиус 8 px, фон `raised` с оттенком палитры
  (forest / dusk / sand / slate, 16–18 %), буква-монограмма 16/600. Отдельная
  обложка издания использует тот же размер и показывает изображение при
  наличии, сохраняя рамку.
- **Content:** название 14/600, описание 12 px `muted`, две метрики —
  наличие аудио и публикация — с верхней линией и табличными числами.
  Длинная RU-подпись и значение
  переносятся (`flex-wrap`), а `dd` не рвётся (`whitespace-nowrap`), поэтому
  счётчики вида 14 / 29 остаются целыми.
- **Selection:** чекбокс 20 px в правом верхнем углу, radius 4 px, виден
  постоянно (без resting opacity). Выбранный и смешанный — фон `accent`, знак
  цветом `surface` (контраст в обеих темах); mixed задаёт фон явно через
  `data-indeterminate`. Подпись поясняет, что выбираются позиции артиста,
  а не сама запись артиста.
- **Mapping:** React Aria `GridList` / `GridListItem` с `selectionMode="multiple"`
  и `Checkbox slot="selection"`; стрелки и Space работают без мыши.

### Status
- **Pill:** `inline-flex`, радиус 9999 px, поля 2/8 px, граница 1 px, текст
  12/500, зазор 4 px.
- **Neutral:** граница `line`, фон `raised`, текст `muted`.
- **Semantic:** фон — `*-soft`, текст — тон, граница — `color-mix` тона 25–30 %
  с прозрачным. Всегда с текстовой подписью.
- **Notice:** радиус 8 px, граница 1 px (`line` или тон 25 %), фон `raised` или
  `*-soft`, поля 10/14 px, текст 14 px.

### Dialogs and Overlays
- **Modal:** React Aria `ModalOverlay` / `Modal` / `Dialog`; overlay —
  `rgb(0 0 0 / 0.45)`, диалог — радиус 16 px, граница 1 px `line`, фон `surface`,
  поля 24 px, канонический максимум 560 px. Тень — `0 16px 48px rgb(0 0 0 / 0.2)`,
  у ActionModal — `0 24px 64px rgb(0 0 0 / 0.24)`.
- **Content:** заголовок 18/600 (крупный диалог — 20/600), тело 14/1.5 `muted`,
  действия справа с зазором 8 px.
- **DirectoryPicker:** модальный композит из `Breadcrumbs`, `TextField` и списка
  папок; overlay 50 %, ширина до 640 px, поля 18 px, навигационная полоса на
  `raised` с радиусом 8 px. Текущая крошка открывает inline-редактирование пути;
  Escape отменяет правку, Enter подтверждает, футер справа — «Отмена» и
  «Выбрать».
- **Popover:** радиус 12 px, граница `line`, фон `surface`, поля 4 px, тень
  `0 12px 32px rgb(0 0 0 / 0.14)`; опция — радиус 8 px, поля 8/10 px, focused —
  `raised`, selected — `accent-soft` и `accent-strong`.

### React Aria Mapping
- `AppButton` → `Button`; primary / secondary / ghost / danger / icon — варианты
  одного контрола.
- `AppSearchField` → `SearchField` с `Input` и кнопкой `slot="clear"`.
- Поля форм → `TextField` / `Label` / `Input`; ошибки — `aria-invalid` и
  `aria-describedby`.
- `AppCheckbox` → `Checkbox`; `AppSwitch` → `Switch`.
- `AppToolbar` → подписанная группа `fieldset` для поиска и фильтров, чтобы
  `Toolbar` не перехватывал стрелки у вложенного поля; `AppActionToolbar` →
  React Aria `Toolbar` для ряда только с действиями.
- `AppBreadcrumbs` → `Breadcrumbs` / `Breadcrumb` / `Link`.
- `ActionModal` → `ModalOverlay` / `Modal` / `Dialog`; `DirectoryPickerView`
  добавляет к той же схеме `Breadcrumbs` и `TextField`.
- `DirectoryPathFieldView` → read-only mono-поле 36 px и кнопка «Выбрать»,
  открывающая `DirectoryPickerView`.
- `LibraryArtistCards` → `GridList` / `GridListItem` с множественным выбором.
- `LibraryTrackListView` → `Table` / `Column` / `Row` / `Cell` на desktop и
  карточный список на узком экране.
- `ActiveFilterTags` → `TagGroup` / `TagList` / `Tag` и кнопка `slot="remove"`.
- `RowActionMenu` → `MenuTrigger` / `Popover` / `Menu` / `MenuItem`.
- `SettingsHelpDetails` → `Disclosure` / `DisclosurePanel` для технических
  подробностей. Существенные условия, последствия и ошибки видны без раскрытия.
  Панель провайдеров занимает всю ширину вкладки, а строки подсказок сохраняют
  ограниченную длину.
- `GenrePicker` → выбранные жанры в `TagGroup` прямо в поле; кнопка «+»
  открывает компактный немодальный `Popover` с поиском и списком каталога.

## Do's and Don'ts

### Do:
- **Do** держать синий редким: выбор, ссылка, активный пункт, фокус.
- **Do** использовать контролы 36 px и радиус 8 px как базовый размер.
- **Do** строить глубину лестницей `canvas` → `surface` → `raised` и линией 1 px.
- **Do** сопровождать статус текстом и цветом одновременно.
- **Do** сохранять одинаковую иерархию в светлой и тёмной темах.
- **Do** использовать React Aria primitives и выбранные примеры для поведения.
- **Do** оставлять таблице локальную прокрутку, не ломая shell.

### Don't:
- **Don't** добавлять декоративные KPI-карточки, крупные hero и градиентные фоны.
- **Don't** использовать палитру обложки или артиста как статус.
- **Don't** вешать тени на спокойные панели, строки и карточки.
- **Don't** заливать синим большие площади и фоны секций.
- **Don't** передавать статус только цветом.
- **Don't** уменьшать контролы ниже 36 px и убирать подписи полей.
- **Don't** добавлять второй акцент или произвольные радиусы.
