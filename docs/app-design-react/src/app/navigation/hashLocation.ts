import { useSyncExternalStore } from "react";

const subscribe = (listener: () => void) => {
	window.addEventListener("hashchange", listener);
	window.addEventListener("popstate", listener);
	return () => {
		window.removeEventListener("hashchange", listener);
		window.removeEventListener("popstate", listener);
	};
};

const getSnapshot = () => window.location.hash;
const getServerSnapshot = () => "";

/** Reads the current hash, including Back/Forward navigation. */
export function useHashLocation() {
	return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
