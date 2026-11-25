import { useCallback, useEffect, useRef, useState } from "react";
import { genreCatalog } from "../genres/catalogue";

/** Demo scheduling: the in-memory load must remain visibly busy before settling. */
export const demoGenreCatalogLoadDelayMs = 600;

export type SetupGenreCatalogDemoState =
	| "idle"
	| "loading"
	| "ready"
	| "failed";

type GenreCatalogAttempt = {
	endpoint: string;
	state: SetupGenreCatalogDemoState;
};

export type SetupGenreCatalogDemoProvider = {
	/** State for the endpoint currently selected in Setup. */
	state: SetupGenreCatalogDemoState;
	/** Complete fixture size after a successful load. */
	count?: number;
	/** Loads the fixture for the current endpoint; ignored while a load is pending. */
	load: () => void;
};

/**
 * The catalog proof is scoped to one MusicBrainz mode and effective endpoint.
 * A load for a different endpoint cannot certify the current selection, and a
 * Settings demo state that merely marks the catalog loaded certifies nothing.
 */
export function setupGenreCatalogEndpoint(
	mode: "" | "public" | "self-hosted",
	url: string,
): string {
	if (mode === "public") return "public";
	if (mode !== "self-hosted") return "";
	const base = url.trim().replace(/\/+$/, "");
	return base ? `self-hosted|${base}` : "";
}

export function useSetupGenreCatalogDemoProvider({
	endpoint,
	delayMs = demoGenreCatalogLoadDelayMs,
}: {
	endpoint: string;
	/** Test and live-check override; the default keeps the pending state visible. */
	delayMs?: number;
}): SetupGenreCatalogDemoProvider {
	const [attempt, setAttempt] = useState<GenreCatalogAttempt>({
		endpoint: "",
		state: "idle",
	});
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

	useEffect(() => {
		timer.current = undefined;
		setAttempt((current) =>
			current.endpoint === endpoint ? current : { endpoint, state: "idle" },
		);
		return () => {
			if (timer.current !== undefined) {
				clearTimeout(timer.current);
				timer.current = undefined;
			}
		};
	}, [endpoint]);

	const state =
		endpoint !== "" && attempt.endpoint === endpoint ? attempt.state : "idle";

	const load = useCallback(() => {
		if (endpoint === "" || timer.current !== undefined) return;
		setAttempt({ endpoint, state: "loading" });
		timer.current = setTimeout(() => {
			timer.current = undefined;
			setAttempt({ endpoint, state: "ready" });
		}, delayMs);
	}, [delayMs, endpoint]);

	return {
		state,
		count: state === "ready" ? genreCatalog.length : undefined,
		load,
	};
}
