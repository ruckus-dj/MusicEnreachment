import { parseLibraryPositionAddress } from "../../../ui/features/library/libraryDetailRoute";

export type MetadataPreviewToken = {
	inventoryVersion: number;
	scopeKey: string;
	selectedIds: string[];
};

export function canonicalPreviewIds(ids: Iterable<string>) {
	return [...new Set(ids)].sort();
}

export function previewScopeKey(route: string, ids: Iterable<string>) {
	const position = parseLibraryPositionAddress(route);
	if (position) return `position:${position.releaseId}:${position.positionId}`;
	return `selection:${canonicalPreviewIds(ids).join("|")}`;
}

export function captureMetadataPreview(
	inventoryVersion: number,
	scopeKey: string,
	selectedIds: Iterable<string>,
): MetadataPreviewToken {
	return {
		inventoryVersion,
		scopeKey,
		selectedIds: canonicalPreviewIds(selectedIds),
	};
}

export function isMetadataPreviewFresh(
	token: MetadataPreviewToken,
	inventoryVersion: number,
	scopeKey: string,
) {
	return (
		token.inventoryVersion === inventoryVersion && token.scopeKey === scopeKey
	);
}
