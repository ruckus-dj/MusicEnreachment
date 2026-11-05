/** Tailwind utilities for the catalogue screens; kept literal for Tailwind scanning. */
export const catalogueStyles = {
	shell:
		"max-w-none px-[clamp(16px,2.2vw,34px)] py-[clamp(16px,2.2vw,34px)] text-[var(--ink)] max-[600px]:px-[10px] max-[600px]:py-[14px]",
	pageHeader:
		"mb-[18px] grid grid-cols-[minmax(0,1fr)_auto] items-end gap-x-6 gap-y-3 border-b border-[var(--line)] pb-4 max-[600px]:grid-cols-1 max-[600px]:items-start",
	pageTitle:
		"m-0 text-[20px] leading-[1.3] tracking-[-.02em] [overflow-wrap:anywhere]",
	pageSubtitle: "mt-[5px] text-[var(--muted)]",
	pageHeaderAction: "justify-self-end max-[600px]:justify-self-start",
	controls: "mb-3",
	tabsPanel: "min-w-0",
	panelControls:
		"mb-3 grid grid-cols-[minmax(220px,1fr)_minmax(340px,1fr)] items-end gap-3 max-[1100px]:grid-cols-1 max-[600px]:gap-2",
	tabs: "min-w-0 [&_.react-aria-TabList]:gap-[3px] [&_.react-aria-TabList]:border-0 [&_.react-aria-Tab]:min-h-9 [&_.react-aria-Tab]:rounded-[4px] [&_.react-aria-Tab]:border [&_.react-aria-Tab]:border-transparent [&_.react-aria-Tab]:px-[11px] [&_.react-aria-Tab]:py-[7px] [&_.react-aria-Tab]:font-semibold [&_.react-aria-Tab[data-selected]]:border-[var(--line)] [&_.react-aria-Tab[data-selected]]:bg-[var(--surface)] [&_.react-aria-Tab[data-selected]]:text-[var(--accent)] [&_.react-aria-Tab[data-selected]]:shadow-[0_1px_2px_#0001] max-[600px]:overflow-x-auto max-[600px]:[&_.react-aria-TabList]:flex-nowrap max-[600px]:[&_.react-aria-Tab]:whitespace-nowrap",
	search:
		"min-h-[34px] rounded-[4px] border border-[var(--control)] bg-[var(--surface)] px-2 focus-within:outline focus-within:outline-2 focus-within:outline-[var(--focus)] focus-within:outline-offset-1 [&_.react-aria-Input]:min-h-8 [&_.react-aria-Input]:border-0 [&_.react-aria-Input]:outline-none [&_.react-aria-Input]:shadow-none",
	filter:
		"min-w-0 [&_.react-aria-Label]:mb-1 [&_.react-aria-Label]:block [&_.react-aria-Label]:text-[11px] [&_.react-aria-Label]:font-semibold [&_.react-aria-Label]:text-[var(--muted)]",
	segments:
		"flex min-h-[34px] flex-wrap overflow-visible rounded-md border border-[var(--control)] bg-[var(--surface)]",
	segment:
		"flex min-w-[82px] flex-[1_1_82px] cursor-pointer items-center justify-center px-[7px] py-[5px] text-center text-[11px] leading-[1.2] text-[var(--muted)] transition-colors duration-[120ms] [overflow-wrap:anywhere] [white-space:normal] [&+*]:border-l [&+*]:border-[var(--line)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--ink)] data-[selected]:font-bold data-[selected]:shadow-[inset_0_-2px_var(--accent)] data-[hovered]:not-data-[selected]:bg-[var(--raised)] data-[hovered]:not-data-[selected]:text-[var(--ink)] data-[focus-visible]:relative data-[focus-visible]:z-[1] data-[focus-visible]:outline data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:-outline-offset-[3px]",
	select:
		"min-w-0 [&>.react-aria-Button]:flex [&>.react-aria-Button]:min-h-[34px] [&>.react-aria-Button]:w-full [&>.react-aria-Button]:items-center [&>.react-aria-Button]:justify-between [&>.react-aria-Button]:gap-2.5 [&>.react-aria-Button]:rounded-[4px] [&>.react-aria-Button]:border [&>.react-aria-Button]:border-[var(--control)] [&>.react-aria-Button]:bg-[var(--surface)] [&>.react-aria-Button]:px-[9px] [&>.react-aria-Button]:py-[5px] [&>.react-aria-Button]:text-left [&>.react-aria-Button]:text-[var(--ink)]",
	popover:
		"z-50 max-h-[300px] min-w-[var(--trigger-width)] overflow-auto rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_28px_color-mix(in_srgb,var(--ink)_6%,transparent)] [&_.react-aria-ListBox]:grid [&_.react-aria-ListBox]:gap-0.5 [&_.react-aria-ListBox]:outline-none [&_.react-aria-ListBoxItem]:cursor-pointer [&_.react-aria-ListBoxItem]:rounded-[3px] [&_.react-aria-ListBoxItem]:px-[9px] [&_.react-aria-ListBoxItem]:py-[7px] [&_.react-aria-ListBoxItem[data-focused]]:bg-[var(--raised)] [&_.react-aria-ListBoxItem[data-selected]]:bg-[var(--accent-soft)] [&_.react-aria-ListBoxItem[data-selected]]:text-[var(--accent)]",
	dockScreen:
		"pb-[calc(136px+env(safe-area-inset-bottom))] max-[600px]:pb-[calc(190px+env(safe-area-inset-bottom))]",
	dock: "fixed bottom-[calc(16px+env(safe-area-inset-bottom))] right-6 z-[60] grid w-[min(680px,calc(100vw-48px))] gap-2 rounded-lg border border-[color-mix(in_srgb,var(--accent)_38%,var(--line))] bg-[var(--accent-soft)] px-[14px] py-3 text-[var(--ink)] shadow-[0_12px_36px_color-mix(in_srgb,var(--ink)_18%,transparent)] max-[600px]:inset-x-2 max-[600px]:bottom-[env(safe-area-inset-bottom)] max-[600px]:w-auto max-[600px]:gap-[7px] max-[600px]:p-[10px]",
	dockActions:
		"flex flex-wrap gap-2 max-[600px]:grid max-[600px]:grid-cols-2 [&_.react-aria-Button]:min-h-9 [&_.react-aria-Button]:rounded-[5px] [&_.react-aria-Button]:border [&_.react-aria-Button]:border-[var(--line)] [&_.react-aria-Button]:bg-[var(--surface)] [&_.react-aria-Button]:px-[10px] [&_.react-aria-Button]:py-[6px] [&_.react-aria-Button]:text-[var(--ink)] max-[600px]:[&_.react-aria-Button]:min-w-0 max-[600px]:[&_.react-aria-Button]:px-[5px] max-[600px]:[&_.react-aria-Button]:text-xs max-[600px]:[&_.react-aria-Button:first-child]:col-span-full [&_.react-aria-Button.ui-button--primary]:border-[var(--accent)] [&_.react-aria-Button.ui-button--primary]:bg-[var(--accent)] [&_.react-aria-Button.ui-button--primary]:text-[var(--button-text)]",
	list: "overflow-hidden rounded-md border border-[var(--line)] bg-[var(--surface)] shadow-[0_8px_28px_color-mix(in_srgb,var(--ink)_6%,transparent)]",
	tableScroll: "w-full overflow-auto [overscroll-behavior-inline:contain]",
	mobileCatalog: "hidden max-[599px]:grid gap-2",
	mobileCatalogRow:
		"grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs [font-variant-numeric:tabular-nums]",
	mobileCatalogField:
		"grid min-w-0 grid-cols-[minmax(76px,.7fr)_minmax(0,1.5fr)] gap-2 border-b border-[var(--line)] pb-1.5 last:border-b-0 last:pb-0 [&>dt]:text-[11px] [&>dt]:text-[var(--muted)] [&>dd]:m-0 [&>dd]:[overflow-wrap:anywhere] [&_a]:font-semibold [&_a]:text-[var(--accent)] [&_a]:underline-offset-2 [&_a:hover]:underline",
	mobileCatalogGroup:
		"grid gap-2 border-t border-[var(--line)] pt-2 first:border-0 first:pt-0",
	mobileCatalogTracks: "grid gap-2 border-t border-[var(--line)] pt-2",
	table:
		"w-full border-separate border-spacing-0 text-xs [font-variant-numeric:tabular-nums] [&_.react-aria-Column]:bg-[var(--raised)] [&_.react-aria-Column]:px-[9px] [&_.react-aria-Column]:py-2 [&_.react-aria-Column]:text-left [&_.react-aria-Column]:text-[12px] [&_.react-aria-Column]:font-bold [&_.react-aria-Column]:text-[var(--muted)] [&_.react-aria-Column]:whitespace-nowrap [&_.react-aria-Column]:border-b [&_.react-aria-Column]:border-[var(--line)] [&_.react-aria-Row]:bg-[var(--surface)] [&_.react-aria-Row]:transition-colors [&_.react-aria-Row:hover]:bg-[var(--raised)] [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-t [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-[var(--line)] [&_.react-aria-Cell]:min-w-0 [&_.react-aria-Cell]:px-2 [&_.react-aria-Cell]:py-[6px] [&_.react-aria-Cell]:align-top [&_.react-aria-Cell]:[overflow-wrap:anywhere] [&_.react-aria-Cell:first-child]:box-border [&_.react-aria-Cell:first-child]:w-[34px] [&_.react-aria-Cell:first-child]:min-w-[34px] [&_.react-aria-Cell:first-child]:max-w-[34px] [&_.react-aria-Cell:first-child]:px-0 [&_.react-aria-Cell:first-child]:text-center [&_.react-aria-Column:first-child]:box-border [&_.react-aria-Column:first-child]:w-[34px] [&_.react-aria-Column:first-child]:min-w-[34px] [&_.react-aria-Column:first-child]:max-w-[34px] [&_.react-aria-Column:first-child]:px-0 [&_.react-aria-Column:first-child]:text-center [&_.react-aria-Column]:align-middle [&_.react-aria-Cell:nth-child(2)]:align-middle [&_.react-aria-Cell_a]:font-semibold [&_.react-aria-Cell_a]:text-[var(--accent)] [&_.react-aria-Cell_a]:underline-offset-2 [&_.react-aria-Cell_a:hover]:underline [&_.filter-match]:bg-[linear-gradient(to_right,color-mix(in_srgb,var(--accent)_4%,transparent),transparent)] [&_.filter-match]:bg-left-top [&_.filter-match]:bg-no-repeat [&_.filter-match]:[background-size:9%_100%] [&_.filter-muted>td]:opacity-[.55] [&_.filter-muted:focus-within>td]:opacity-100",
	artistGrid:
		"grid grid-cols-[repeat(auto-fill,minmax(min(100%,224px),1fr))] gap-[14px] p-[clamp(12px,1.5vw,20px)] max-[600px]:grid-cols-1 max-[600px]:gap-[11px] max-[600px]:p-[10px]",
	artistCard:
		"min-w-0 transition-transform duration-[180ms] motion-reduce:transition-none [@media(hover:hover)_and_(pointer:fine)]:hover:-translate-y-[3px]",
	artistArt:
		"relative isolate aspect-[1.18/1] min-h-0 w-full min-w-0 overflow-hidden rounded-[11px] border border-[color-mix(in_srgb,var(--artist-color)_45%,var(--line))] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--artist-color)_77%,#111),color-mix(in_srgb,var(--artist-color)_46%,#101820))] text-white shadow-[0_7px_20px_color-mix(in_srgb,var(--artist-color)_20%,transparent)] [--artist-color:#ac7059] transition-shadow duration-[180ms] data-[palette=forest]:[--artist-color:#486d59] data-[palette=dusk]:[--artist-color:#676e95] data-[palette=sand]:[--artist-color:#aa905f] data-[palette=slate]:[--artist-color:#4c717e] before:pointer-events-none before:absolute before:inset-[10%_8%_13%_31%] before:z-[-1] before:rounded-[48%_52%_42%_58%/54%_38%_62%_46%] before:border before:border-[#ffffff47] before:bg-[linear-gradient(135deg,#ffffff16,transparent_68%)] before:transition-transform before:duration-300 before:[transform:rotate(-18deg)] after:pointer-events-none after:absolute after:right-[14%] after:top-[12%] after:z-[-1] after:aspect-square after:w-[38%] after:rounded-full after:border after:border-[#ffffff30] after:bg-[radial-gradient(circle_at_35%_32%,#ffffff28,#ffffff05_68%)] after:transition-transform after:duration-300 [@media(hover:hover)_and_(pointer:fine)]:hover:shadow-[0_14px_32px_color-mix(in_srgb,var(--artist-color)_30%,transparent)] [@media(hover:hover)_and_(pointer:fine)]:hover:before:[transform:rotate(-18deg)_scale(1.04)] [@media(hover:hover)_and_(pointer:fine)]:hover:after:scale-105 motion-reduce:before:transition-none motion-reduce:after:transition-none motion-reduce:transition-none motion-reduce:hover:translate-y-0",
	artistCardLink:
		"absolute inset-0 flex flex-col justify-between overflow-hidden p-[17px] text-white no-underline hover:text-white hover:no-underline focus-visible:rounded-[inherit] focus-visible:outline focus-visible:outline-[3px] focus-visible:outline-[var(--focus)] focus-visible:-outline-offset-1",
	artistMonogram:
		"block size-[62px] text-center text-[42px] font-semibold leading-[62px] text-white [text-shadow:0_2px_15px_#0005]",
	artistOverlay:
		"-mx-[17px] -mb-[17px] -mt-[17px] grid gap-[11px] bg-[linear-gradient(transparent,#101415e8_22%,#101415f2)] px-[17px] pb-[15px] pt-[34px]",
	artistCopy:
		"grid min-w-0 gap-[3px] [&_strong]:text-[clamp(16px,1.3vw,20px)] [&_strong]:leading-[1.15] [&_strong]:tracking-[-.02em] [&_strong]:[overflow-wrap:anywhere] [&>span]:text-[11px] [&>span]:text-[#ffffffbf]",
	artistStats:
		"grid grid-cols-2 gap-x-[10px] gap-y-[6px] text-[10px] leading-[1.25] text-[#ffffffbd] [&>span]:min-w-0 [&_strong]:text-[11px] [&_strong]:text-white [&_strong]:[font-variant-numeric:tabular-nums]",
	cardSelection:
		"absolute left-[9px] top-[9px] z-[2] grid size-6 place-items-center",
	object:
		"min-w-0 p-[clamp(14px,2vw,26px)] max-[600px]:px-[10px] max-[600px]:py-[13px]",
	hero: "mb-0 flex flex-nowrap items-start gap-x-[clamp(16px,2.5vw,30px)] gap-y-[18px] py-1 pb-[22px] max-[600px]:grid max-[600px]:grid-cols-[76px_minmax(0,1fr)] max-[600px]:items-start max-[600px]:gap-x-[13px] max-[600px]:gap-y-[10px]",
	heroArt:
		"relative isolate grid aspect-square min-w-0 place-items-center overflow-hidden rounded-[18px] border border-[color-mix(in_srgb,var(--artist-color)_48%,var(--line))] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--artist-color)_80%,#111),color-mix(in_srgb,var(--artist-color)_42%,#101820))] text-white shadow-[0_16px_34px_color-mix(in_srgb,var(--artist-color)_24%,transparent)] [--artist-color:#ac7059] data-[palette=forest]:[--artist-color:#486d59] data-[palette=dusk]:[--artist-color:#676e95] data-[palette=sand]:[--artist-color:#aa905f] data-[palette=slate]:[--artist-color:#4c717e] before:absolute before:inset-[12%] before:rounded-full before:border before:border-[#ffffff38] before:content-[''] before:[transform:rotate(-24deg)_scaleX(.72)] after:absolute after:inset-[22%] after:rounded-full after:border after:border-[#ffffff22] after:content-[''] after:[transform:rotate(28deg)_scaleX(.72)] [&>span]:z-[1] [&>span]:font-[Georgia,serif] [&>span]:text-[clamp(42px,6vw,76px)] [&>span]:font-medium [&>span]:[text-shadow:0_4px_22px_#0007] [flex:0_0_clamp(96px,14vw,168px)] max-[600px]:col-start-1 max-[600px]:row-start-1 max-[600px]:rounded-xl max-[600px]:[flex:none] max-[600px]:[&>span]:text-[38px]",
	heroBody:
		"flex min-w-0 flex-[1_1_auto] flex-col justify-between gap-[14px] self-stretch max-[600px]:contents",
	heroHeading: "min-w-0 max-[600px]:col-start-2 max-[600px]:row-start-1",
	heroFoot:
		"flex min-w-0 flex-wrap items-center justify-between gap-x-[18px] gap-y-[10px] max-[600px]:col-span-full max-[600px]:row-start-2",
	heroStats: "m-0",
	heroBack: "mb-[10px] [&_a]:text-xs [&_a]:text-[var(--muted)]",
	heroKicker: "mb-[7px] text-xs font-bold text-[var(--accent)]",
	heroGenre: "mt-1 font-semibold text-[var(--ink)]",
	previewCount:
		"ml-1.5 inline-grid h-[20px] min-w-[20px] place-items-center rounded-full bg-[var(--raised)] px-[5px] text-xs [font-variant-numeric:tabular-nums]",
	artistDanger:
		"[&_.ui-button--secondary.ui-button--danger]:border-[color-mix(in_srgb,var(--error)_45%,var(--control))] [&_.ui-button--secondary.ui-button--danger]:bg-transparent [&_.ui-button--secondary.ui-button--danger]:text-[var(--error)]",
	objectHeader:
		"mb-[10px] flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] pb-[14px] [&>div]:min-w-0 [&>div]:[overflow-wrap:anywhere] [&_h1]:text-[20px] [&_h1]:leading-[1.3] [&_h1]:tracking-[-.02em] [&_h1]:[overflow-wrap:anywhere] [&_p]:max-w-[850px] [&_p]:[overflow-wrap:anywhere] [&>a]:justify-self-end",
	objectColumns: "grid grid-cols-2 gap-3 max-[1100px]:grid-cols-1",
	panel:
		"mb-[10px] min-w-0 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&_h2]:mb-[9px] [&_h2]:text-sm [&_h3]:text-sm [&_h3]:font-semibold [&_h3]:mb-2 [&_p]:my-2",
	facts:
		"my-[10px] grid grid-cols-[minmax(130px,.75fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] max-[600px]:grid-cols-1 max-[600px]:gap-y-0 [&_dt]:text-[11px] [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:[overflow-wrap:anywhere] max-[600px]:[&_dd]:mb-[7px]",
	actions:
		"my-2 flex flex-wrap items-center gap-[7px] [&>button]:min-h-8 [&>button]:rounded-[4px] [&>button]:border [&>button]:border-[var(--control)] [&>button]:bg-[var(--surface)] [&>button]:px-[9px] [&>button]:py-[5px] [&>button]:text-[var(--ink)]",
	secondary:
		"mt-[3px] block text-[11px] font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
	sub: "mt-[3px] block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
	path: "block font-[var(--mono)] text-[11px] [overflow-wrap:anywhere]",
	expander:
		"min-h-0 border-0 bg-transparent p-0 text-left font-[inherit] font-semibold text-[var(--accent)] hover:underline",
	previewTable:
		"overflow-auto rounded-[4px] border border-[var(--line)] [&_table]:w-full [&_table]:min-w-[700px] [&_table]:border-collapse [&_table]:text-[11px] [&_th]:px-2 [&_th]:py-[7px] [&_td]:px-2 [&_td]:py-[7px] [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:border-b [&_td]:border-[var(--line)] [&_th]:text-left [&_td]:text-left [&_th]:align-middle [&_td]:align-top [&_td:nth-child(2)]:align-middle [&_th]:[overflow-wrap:anywhere] [&_td]:[overflow-wrap:anywhere] [&_th]:bg-[var(--raised)] [&_th]:text-[var(--muted)] [&_th]:font-semibold [&_tr:last-child>*]:border-b-0",
	editions: "border-t border-[var(--line)]",
	albumRow: "my-3",
	albumCard:
		"relative min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-[0_5px_18px_color-mix(in_srgb,var(--ink)_5%,transparent)] transition-[border-color,box-shadow] duration-[160ms] data-[expanded]:border-[color-mix(in_srgb,var(--accent)_45%,var(--line))] data-[expanded]:shadow-[0_10px_28px_color-mix(in_srgb,var(--ink)_9%,transparent)] [&_.catalogue-edition-trigger]:before:hidden [&_.catalogue-edition-trigger]:after:content-['⌄'] [&_.catalogue-edition-trigger]:after:text-[18px] [&_.catalogue-edition-trigger]:after:text-[var(--accent)] [&_.catalogue-edition-trigger]:after:transition-transform [&_.catalogue-edition-trigger]:after:duration-[160ms] [&[data-expanded]_.catalogue-edition-trigger]:after:rotate-180 [&_.artist-album-delete]:absolute [&_.artist-album-delete]:right-[27px] [&_.artist-album-delete]:top-1/2 [&_.artist-album-delete]:z-[2] [&_.artist-album-delete]:size-[38px] [&_.artist-album-delete]:min-h-[38px] [&_.artist-album-delete]:min-w-[38px] [&_.artist-album-delete]:-translate-y-1/2 [&_.artist-album-delete]:rounded-[7px] [&_.artist-album-delete]:border [&_.artist-album-delete]:border-[color-mix(in_srgb,var(--error)_48%,var(--control))] [&_.artist-album-delete]:bg-[var(--surface)] [&_.artist-album-delete]:p-0 [&_.artist-album-delete]:text-[var(--error)] [&_.artist-album-delete:hover]:border-[var(--error)] [&_.artist-album-delete:hover]:bg-[var(--error-soft)] [&_.artist-album-delete:focus-visible]:outline [&_.artist-album-delete:focus-visible]:outline-2 [&_.artist-album-delete:focus-visible]:outline-[var(--focus)] [&_.artist-album-delete:focus-visible]:outline-offset-2",
	albumHeader: "relative",
	albumTrigger:
		"grid min-h-[94px] w-full grid-cols-[68px_minmax(0,1fr)_18px] items-center gap-[13px] border-0 bg-transparent py-3 pl-[15px] pr-[66px] text-left before:hidden after:content-['⌄'] after:text-[18px] after:text-[var(--accent)] after:transition-transform after:duration-[160ms] data-[expanded]:after:rotate-180 max-[600px]:min-h-20 max-[600px]:grid-cols-[54px_minmax(0,1fr)_14px] max-[600px]:items-start max-[600px]:gap-[9px] max-[600px]:py-[9px] max-[600px]:pl-[9px]",
	albumDelete:
		"absolute right-[27px] top-1/2 z-[2] grid size-[38px] min-h-[38px] min-w-[38px] -translate-y-1/2 place-items-center rounded-[7px] border border-[color-mix(in_srgb,var(--error)_48%,var(--control))] bg-[var(--surface)] p-0 text-[var(--error)] hover:border-[var(--error)] hover:bg-[var(--error-soft)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
	albumCover:
		"relative grid aspect-square w-[68px] place-items-center overflow-hidden rounded-[7px] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--album-color)_78%,#121212),color-mix(in_srgb,var(--album-color)_44%,#15201d))] text-white shadow-[0_4px_10px_#0002] [--album-color:#ac7059] data-[palette=forest]:[--album-color:#486d59] data-[palette=dusk]:[--album-color:#676e95] data-[palette=sand]:[--album-color:#aa905f] data-[palette=slate]:[--album-color:#4c717e] before:absolute before:inset-[10%] before:rounded-[50%_42%_52%_45%] before:border before:border-[#ffffff60] before:content-[''] before:[transform:rotate(-22deg)] [&>span]:z-[1] [&>span]:font-[Georgia,serif] [&>span]:text-[34px] [&>span]:[text-shadow:0_2px_8px_#0008] max-[600px]:w-[54px] max-[600px]:[&>span]:text-[28px]",
	albumCopy:
		"grid min-w-0 gap-[5px] text-left [&>strong]:text-[clamp(14px,1.35vw,18px)] [&>strong]:leading-[1.2] [&>strong]:tracking-[-.015em] [&>strong]:[overflow-wrap:anywhere]",
	albumMeta:
		"block text-[12px] font-normal leading-[1.4] text-[var(--muted)] [overflow-wrap:anywhere]",
	albumPanel:
		"border-t border-[var(--line)] [&_.demo-preview-table]:mx-3 [&_.demo-preview-table]:mb-3",
	albumActions: "px-[13px] py-[10px]",
	artistPanel:
		"my-[10px] rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-3 shadow-[0_2px_8px_#00000008] [&_h2]:mb-[9px] [&_h2]:text-sm [&_p]:my-1 [&_p]:mb-3 [&_p]:text-xs [&_p]:text-[var(--muted)] [&_select]:min-h-[34px] [&_select]:rounded-[4px] [&_select]:border [&_select]:border-[var(--control)] [&_select]:bg-[var(--surface)] [&_select]:px-2 [&_select]:py-[5px]",
	inlineTracks:
		"mt-2 grid min-w-[240px] gap-1 overflow-auto border-l-2 border-[var(--accent)] bg-[var(--raised)] p-[7px] [&>div]:grid [&>div]:grid-cols-[30px_minmax(100px,1fr)_auto] [&>div]:items-start [&>div]:gap-2 [&>div>span]:text-xs [&>div>span]:text-[var(--muted)]",
	inlineTable:
		"w-full min-w-[700px] border-collapse text-xs [&_th]:bg-[var(--surface)] [&_th]:text-[var(--muted)] [&_th]:font-semibold [&_th]:text-left [&_th]:align-middle [&_th]:px-[6px] [&_th]:py-[5px] [&_td]:px-[6px] [&_td]:py-[5px] [&_td]:align-top [&_td:nth-child(2)]:align-middle [&_td]:[overflow-wrap:anywhere] [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:border-b [&_td]:border-[var(--line)] [&_tr:last-child>*]:border-b-0",
	empty: "p-7 px-4 text-center text-[var(--muted)]",
	absPanel: "min-w-0",
	detailSelect:
		"mt-3 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-[14px] [&_h2]:mb-1 [&_h2]:text-[15px] [&_p]:my-1 [&_p]:mb-3 [&_p]:text-xs [&_p]:text-[var(--muted)] [&_label]:grid [&_label]:max-w-[440px] [&_label]:gap-[5px] [&_label]:text-[11px] [&_label]:text-[var(--muted)] [&_select]:min-h-[34px] [&_select]:rounded-[4px] [&_select]:border [&_select]:border-[var(--control)] [&_select]:bg-[var(--surface)] [&_select]:px-2 [&_select]:py-[5px]",
	filterRows:
		"[&.filter-match]:bg-[linear-gradient(to_right,color-mix(in_srgb,var(--accent)_4%,transparent),transparent)] [&.filter-match]:bg-left-top [&.filter-match]:bg-no-repeat [&.filter-match]:[background-size:9%_100%] [&.filter-muted>td]:opacity-[.55] [&.filter-muted:focus-within>td]:opacity-100",
	tableCheckbox:
		"group mx-auto grid h-8 w-[34px] flex-[0_0_34px] cursor-pointer place-items-center",
	cardCheckbox: "group grid size-6 cursor-pointer place-items-center",
	checkboxBox:
		"grid size-[18px] place-items-center rounded-[3px] border border-[var(--control)] text-sm leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-[3px]",
	cardCheckboxBox:
		"grid size-4 place-items-center rounded-[4px] border-[1.5px] border-[#ffffffd9] bg-[#1014154d] text-[11px] leading-none text-transparent transition-colors group-data-[hovered]:border-white group-data-[hovered]:bg-[#10141580] group-data-[selected]:border-white group-data-[selected]:bg-white group-data-[selected]:text-[#17201d] group-data-[indeterminate]:border-white group-data-[indeterminate]:bg-white group-data-[indeterminate]:text-[#17201d] group-data-[focus-visible]:outline group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-white group-data-[focus-visible]:outline-offset-2 group-data-[focus-visible]:shadow-[0_0_0_4px_#10141580] group-data-[disabled]:opacity-[.45]",
	selectionColumn:
		"box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!",
	trackDetails:
		"[&_summary]:cursor-pointer [&_summary]:text-[var(--accent)] [&_summary]:underline-offset-2 [&_summary:hover]:underline",
	dockDialog:
		"[&_[slot=title]]:mb-2 [&_[slot=title]]:text-base [&_p]:my-2 [&_p]:mb-[14px] [&_ul]:grid [&_ul]:gap-2 [&_code]:break-all [&_button]:mr-2 [&_button]:rounded [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-[6px] [&_button]:text-[var(--ink)] [&_button.ui-button--danger]:border-[var(--error)] [&_button.ui-button--danger]:bg-[var(--error)] [&_button.ui-button--danger]:text-white",
	dockDialogFooter: "mt-3 flex flex-wrap justify-end gap-2",
	source:
		"group mb-1 flex min-w-0 cursor-pointer items-center gap-2 [overflow-wrap:anywhere] rounded-[5px] border border-[var(--line)] p-2 data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 [&>span:last-child]:min-w-0 [&_strong]:[overflow-wrap:anywhere]",
	secondaryInline: "inline text-[11px] text-[var(--muted)]",
	radioDot:
		"size-4 flex-[0_0_16px] rounded-full border border-[var(--control)] group-data-[selected]:border-[5px] group-data-[selected]:border-[var(--accent)]",
	disclosure:
		"my-2 min-w-0 overflow-hidden rounded-[5px] border border-[var(--line)] bg-[var(--surface)] [&_summary]:cursor-pointer [&_summary]:text-[var(--accent)] [&_summary]:underline-offset-2 [&_summary:hover]:underline",
	trackPublication:
		"[&_[slot=trigger]]:border-0 [&_[slot=trigger]]:bg-transparent [&_[slot=trigger]]:px-0 [&_[slot=trigger]]:py-1 [&_[slot=trigger]]:text-[var(--accent)]",
} as const;

export const catalogueTableWidths: Record<
	"artists" | "albums" | "tracks",
	string
> = {
	artists: "min-w-[680px]",
	albums: "min-w-[940px]",
	tracks: "min-w-[1050px]",
};
