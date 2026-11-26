import { useEffect, useRef, useState } from "react";
import { useI18n } from "../demoPreferences";
import type { ReleaseCoverEditorModel } from "../../../ui/features/library/ReleaseCoverEditorOutlet";
import { releaseCoverEditorCopy } from "../../../ui/features/library/releaseCoverEditorCopy";
import {
	libraryReleaseText,
	renderLibraryReleaseText,
} from "../../../ui/features/library/libraryReleaseMessages";
import type { Locale } from "../../../ui/i18n/types";

/**
 * Saved cover state for one demo release. Files stay browser objects: this
 * demo does not create managed generations, digests, embedded artwork or
 * publication results.
 */
export type DemoReleaseCoverState = {
	selected?: File;
	pending?: File;
};

export type DemoReleaseCoverEditorModel = {
	source: ReleaseCoverEditorModel;
	selectedImageSrc?: string;
	selectedImageAlt: string;
};

const emptyState: DemoReleaseCoverState = {};

const demoNoticeCopy = {
	en: {
		applied:
			"Cover promoted in this demo preview. No publication, managed file or embedded artwork was changed.",
		retried:
			"Selected cover remains in this demo preview. No publication was retried.",
	},
	ru: {
		applied:
			"Обложка применена в предпросмотре демонстрации. Публикации, управляемые файлы и встроенная обложка не изменены.",
		retried:
			"Выбранная обложка остаётся в предпросмотре демонстрации. Повторное применение публикации не выполнялось.",
	},
} as const;

type DemoCoverNotice = "saved" | "applied" | "retried";

function noticeText(locale: Locale, notice: DemoCoverNotice): string {
	switch (notice) {
		case "saved":
			return renderLibraryReleaseText(locale, libraryReleaseText("coverSaved"));
		case "applied":
			return demoNoticeCopy[locale].applied;
		case "retried":
			return demoNoticeCopy[locale].retried;
	}
}

/** Browser preview of actual File bytes; absent when the runtime has no URLs. */
function useFilePreview(file: File | undefined): string | undefined {
	const [preview, setPreview] = useState<{ file: File; url: string }>();
	useEffect(() => {
		if (!file || typeof URL.createObjectURL !== "function") {
			setPreview(undefined);
			return;
		}
		const url = URL.createObjectURL(file);
		setPreview({ file, url });
		return () => {
			URL.revokeObjectURL(url);
		};
	}, [file]);
	return preview?.file === file ? preview?.url : undefined;
}

/**
 * Demo host controller for the portable release cover editor. The state starts
 * empty for every release. Choosing a browser File only arms Save pending;
 * Save pending records the File as pending; Apply promotes that pending File to
 * selected and clears pending; Retry only acknowledges the selected File and
 * never touches pending. Nothing here advertises native proof, managed
 * versions, filesystem embedding or a real API.
 */
export function useReleaseCoverEditorDemoModel({
	releaseId,
	state,
	onChange,
}: {
	releaseId: string;
	state?: DemoReleaseCoverState;
	onChange?: (state: DemoReleaseCoverState) => void;
}): DemoReleaseCoverEditorModel {
	const { locale } = useI18n();
	const [localState, setLocalState] =
		useState<DemoReleaseCoverState>(emptyState);
	const [file, setFile] = useState<File>();
	const [notice, setNotice] = useState<DemoCoverNotice>();
	const fileInput = useRef<HTMLInputElement>(null);
	const current = onChange ? (state ?? emptyState) : localState;
	const selectedPreview = useFilePreview(current.selected);
	const pendingPreview = useFilePreview(current.pending);

	const commit = (next: DemoReleaseCoverState) => {
		if (onChange) onChange(next);
		else setLocalState(next);
	};

	const chooseFile = (next?: File) => {
		setFile(next);
		setNotice(undefined);
	};

	const savePending = () => {
		if (!file) return;
		if (file.type !== "image/png" && file.type !== "image/jpeg") return;
		commit({ ...current, pending: file });
		setFile(undefined);
		if (fileInput.current) fileInput.current.value = "";
		setNotice("saved");
	};

	const apply = () => {
		if (!current.pending) return;
		commit({ selected: current.pending, pending: undefined });
		setNotice("applied");
	};

	const retry = () => {
		if (!current.selected) return;
		setNotice("retried");
	};

	const model: ReleaseCoverEditorModel = {
		releaseId,
		selected: current.selected ? { imageSrc: selectedPreview } : undefined,
		pending: current.pending ? { imageSrc: pendingPreview } : undefined,
		fileInput,
		busy: false,
		savePendingDisabled: !file,
		applyDisabled: !current.pending,
		retryDisabled: !current.selected,
		onFileChange: chooseFile,
		onSavePending: savePending,
		onApply: apply,
		onRetry: retry,
		notice: notice ? noticeText(locale, notice) : undefined,
	};

	return {
		source: model,
		selectedImageSrc: selectedPreview,
		selectedImageAlt: releaseCoverEditorCopy[locale].selectedCoverAlt,
	};
}
