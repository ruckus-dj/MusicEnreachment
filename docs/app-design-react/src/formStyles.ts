// Static Tailwind utilities for the prototype's forms and operations screens.
export const formStyles = {
	page: "grid gap-[14px]",
	pageHeader:
		"grid gap-[5px] mb-[18px] pb-[14px] border-b border-[var(--line)] max-[600px]:flex max-[600px]:flex-wrap max-[600px]:items-center max-[600px]:justify-between",
	pageTitle:
		"m-0 text-[clamp(21px,2.5vw,28px)] leading-[1.18] [text-wrap:balance]",
	pageDescription: "m-0 max-w-[75ch] text-[var(--muted)]",
	panel:
		"min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold [&>p]:my-2",
	panelHeading:
		"flex items-center justify-between gap-3 border-b border-[var(--line)] px-[14px] py-3 [&>h2]:m-0 [&>h2]:text-[15px]",
	formNotice:
		"mt-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px] text-[var(--ink)]",
	gridTwo: "grid grid-cols-2 gap-[14px] max-[900px]:grid-cols-1",
	formField:
		"grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)] [&_.react-aria-TextField]:min-w-0 [&_.react-aria-Input]:min-h-[38px] [&_.react-aria-Input]:w-full [&_.react-aria-Input]:min-w-0 [&_.react-aria-Input]:rounded-[5px] [&_.react-aria-Input]:border [&_.react-aria-Input]:border-[var(--control)] [&_.react-aria-Input]:bg-[var(--surface)] [&_.react-aria-Input]:px-[10px] [&_.react-aria-Input]:py-2 [&_.react-aria-Input]:text-[var(--ink)]",
	searchField:
		"mb-4 grid max-w-[420px] min-w-0 gap-[5px] text-xs text-[var(--muted)] [&_.react-aria-TextField]:min-w-0 [&_.react-aria-Input]:min-h-[38px] [&_.react-aria-Input]:w-full [&_.react-aria-Input]:min-w-0 [&_.react-aria-Input]:rounded-[5px] [&_.react-aria-Input]:border [&_.react-aria-Input]:border-[var(--control)] [&_.react-aria-Input]:bg-[var(--surface)] [&_.react-aria-Input]:px-3 [&_.react-aria-Input]:py-[10px] [&_.react-aria-Input]:text-[var(--ink)]",
	objectActions: "my-2 flex flex-wrap items-center gap-2",
	setupSteps:
		"m-0 grid list-none grid-cols-4 overflow-hidden rounded-[7px] border border-[var(--line)] bg-[var(--raised)] p-0 max-[600px]:grid-cols-2 [&>li]:flex [&>li]:items-center [&>li]:gap-2 [&>li]:p-[10px] [&>li]:text-xs [&>li]:text-[var(--muted)] [&>li+li]:border-l [&>li+li]:border-[var(--line)] [&>li[aria-current=step]]:bg-[var(--accent-soft)] [&>li[aria-current=step]]:font-bold [&>li[aria-current=step]]:text-[var(--accent)] [&>li:nth-child(3)]:max-[600px]:border-l-0 [&>li:nth-child(3)]:max-[600px]:border-t [&>li:nth-child(4)]:max-[600px]:border-t [&>li:nth-child(4)]:max-[600px]:border-[var(--line)]",
	setupStepIndex:
		"grid size-6 flex-none place-items-center rounded-full border border-[var(--line)] bg-[var(--surface)]",
	setupFooter: "flex justify-between gap-2 border-t border-[var(--line)] pt-3",
	settingsGrid: "grid grid-cols-2 gap-[14px] max-[900px]:grid-cols-1",
	sourceFormGrid: "grid grid-cols-2 gap-[14px] max-[900px]:grid-cols-1",
	tableWrap:
		"overflow-auto rounded-[7px] border border-[var(--line)] bg-[var(--surface)]",
	table:
		"w-full max-[900px]:min-w-[760px] border-collapse text-left text-xs [&_th]:whitespace-nowrap [&_th]:bg-[var(--raised)] [&_th]:font-semibold [&_th]:text-[var(--muted)] [&_td]:align-top [&_th]:align-top [&_td]:border-b [&_th]:border-b [&_td]:border-[var(--line)] [&_th]:border-[var(--line)] [&_td]:p-[10px] [&_th]:p-[10px] [&_tr:last-child_td]:border-b-0 [&_code]:[overflow-wrap:anywhere]",
	previewTable:
		"overflow-auto rounded-[7px] border border-[var(--line)] bg-[var(--surface)] [&_table]:w-full [&_table]:border-collapse [&_table]:text-xs [&_th]:bg-[var(--raised)] [&_th]:p-2 [&_th]:text-left [&_th]:text-[var(--muted)] [&_th]:font-semibold [&_td]:p-2 [&_td]:text-left [&_td]:align-top [&_td]:border-b [&_td]:border-[var(--line)] [&_th]:border-b [&_th]:border-[var(--line)]",
	modeChoice:
		"rounded-md border border-[var(--line)] px-3 py-[10px] [&>p]:ml-[25px] [&>p]:my-[3px] [&>p]:text-xs [&>p]:text-[var(--muted)]",
	directoryDialog:
		"grid max-h-[min(82dvh,720px)] grid-rows-[auto_auto_minmax(100px,1fr)_auto] gap-[14px] max-[600px]:max-h-[calc(100dvh-24px)] max-[600px]:p-[14px] [&>[slot=title]]:m-0",
	directoryBreadcrumbs:
		"flex flex-wrap items-center gap-[5px] rounded-md border border-[var(--line)] bg-[var(--raised)] p-[9px]",
	directoryList:
		"min-h-0 max-h-[44dvh] overflow-auto overscroll-contain rounded-md border border-[var(--line)] max-[600px]:max-h-[34dvh] [&_.demo-directory-list]:my-0",
	directoryFooter:
		"flex justify-end border-t border-[var(--line)] pt-[10px] max-[600px]:[&>*]:flex-1",
	operationSummary: "flex flex-wrap gap-[10px]",
	operationError:
		"min-w-[180px] max-w-[300px] text-[var(--error)] [overflow-wrap:anywhere]",
	operationActions: "flex flex-wrap gap-[6px]",
	operationCards: "grid gap-[10px]",
	operationCard:
		"rounded-[7px] border border-[var(--line)] border-l-[3px] border-l-[var(--error)] bg-[var(--surface)] p-[14px] [&>h3]:m-0 [&>h3]:text-sm [&>p]:my-[7px] [&>dl]:grid [&>dl]:grid-cols-[minmax(100px,.35fr)_minmax(0,1fr)] [&>dl]:gap-x-3 [&>dl]:gap-y-[6px] [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:[overflow-wrap:anywhere]",
	genreLibrary:
		"flex items-start justify-between gap-4 rounded-md border border-[var(--line)] bg-[var(--raised)] p-3 max-[600px]:flex-col [&_p]:my-1 [&_small]:text-[var(--muted)]",
	genreLibraryStatus: "min-w-0 [&_p]:my-1 [&_small]:text-[var(--muted)]",
	genreLibraryControls:
		"flex flex-wrap items-center gap-2 my-2 max-[600px]:flex-col max-[600px]:items-start",
	genreLibraryButton:
		"!border-[var(--button)] !bg-[var(--button)] !text-[var(--button-text)] font-semibold hover:brightness-[.92]",
	localWorkspace:
		"grid grid-cols-[minmax(240px,.82fr)_minmax(0,1.8fr)] items-start gap-4 max-[767px]:grid-cols-1 [&>section]:min-w-0 max-[600px]:[&>section]:w-full",
	localTabs: "mb-[14px] border-b border-[var(--line)]",
	localTabList: "my-2 flex flex-wrap gap-2",
	localTab:
		"min-h-[42px] border-0 border-b-2 border-transparent bg-transparent px-[14px] text-[var(--muted)] data-[selected]:border-[var(--accent)] data-[selected]:font-bold data-[selected]:text-[var(--accent)]",
	localSearch:
		"m-3 grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)] [&_.react-aria-TextField]:min-w-0 [&_.react-aria-Input]:min-h-[38px] [&_.react-aria-Input]:w-full [&_.react-aria-Input]:min-w-0 [&_.react-aria-Input]:rounded-[5px] [&_.react-aria-Input]:border [&_.react-aria-Input]:border-[var(--control)] [&_.react-aria-Input]:bg-[var(--surface)] [&_.react-aria-Input]:px-[10px] [&_.react-aria-Input]:py-2 [&_.react-aria-Input]:text-[var(--ink)]",
	localListActions: "mx-3 mb-[10px]",
	localTracklist: "grid gap-3",
	localTracklistHeading:
		"flex items-center justify-between gap-3 border-b border-[var(--line)] pb-2",
	localTrackActions: "my-2 flex flex-wrap gap-2",
	localFields:
		"grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-3",
	localTrackDefault: "my-2 text-[var(--muted)]",
	localDetailActions: "flex flex-wrap gap-2",
	localRow:
		"flex min-h-16 w-full justify-between gap-2 border-0 border-t border-[var(--line)] bg-transparent px-3 py-[10px] text-left hover:bg-[var(--raised)] [&_small]:block [&_small]:text-[var(--muted)]",
	localRowSelected:
		"bg-[var(--accent-soft)] [box-shadow:inset_3px_0_var(--accent)]",
	localForm: "grid gap-[14px] p-[14px]",
	localFormHeading:
		"-mx-[14px] -mt-[14px] border-b border-[var(--line)] px-[14px] py-3 [&_h2]:m-0 [&_h2]:text-[15px] [&_p]:mt-1 [&_p]:mb-0",
	localField:
		"grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)] [&>span]:font-normal [&>span]:text-[var(--muted)] [&_.react-aria-TextField]:min-w-0 [&_.react-aria-Input]:min-h-[38px] [&_.react-aria-Input]:w-full [&_.react-aria-Input]:min-w-0 [&_.react-aria-Input]:rounded-[5px] [&_.react-aria-Input]:border [&_.react-aria-Input]:border-[var(--control)] [&_.react-aria-Input]:bg-[var(--surface)] [&_.react-aria-Input]:px-[10px] [&_.react-aria-Input]:py-2 [&_.react-aria-Input]:text-[var(--ink)]",
	fieldset:
		"my-3 min-w-0 rounded-md border border-[var(--line)] bg-[var(--raised)] p-3 [&_legend]:px-[5px] [&_legend]:font-semibold",
	genrePicker:
		"rounded-md border border-[var(--line)] p-3 [&_legend]:px-[5px] [&_legend]:font-semibold",
	genreOptions:
		"my-[9px] flex flex-wrap gap-[7px] [&_button]:min-h-[34px] [&_button]:rounded-full",
	genreSelected: "my-[9px] flex flex-wrap gap-[7px]",
	genreChip:
		"inline-flex items-center gap-[5px] rounded-full bg-[var(--accent-soft)] px-2 py-[3px] text-[var(--accent)]",
	localOption: "items-start rounded p-[7px] hover:bg-[var(--raised)]",
	muted: "text-[var(--muted)]",
	required: "font-semibold text-[var(--error)]",
	localError: "my-2 text-[var(--error)]",
	localNote:
		"my-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px]",
	localEmpty: "p-[18px] text-[var(--muted)]",
	localEyebrow: "text-[11px] tracking-[.06em] text-[var(--muted)]",
	localDetailHeading:
		"flex items-start justify-between gap-3 p-[14px] max-[600px]:flex-col [&_h2]:mt-1 [&_h2]:mb-0 [overflow-wrap:anywhere]",
	localFacts:
		"grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-3 px-[14px] pb-[14px] [&>div]:border-b [&>div]:border-[var(--line)] [&>div]:py-[9px] [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:[overflow-wrap:anywhere]",
	localNotice:
		"mt-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px] text-[var(--ink)]",
} as const;
