import { useSyncExternalStore } from "react";

export interface HashAttempt {
  id: number;
  from: string;
  to: string;
}

interface HashNavigationGuard {
  shouldBlock: () => boolean;
  onBlocked: (attempt: HashAttempt) => void;
}

type PendingAttempt = {
  attempt: HashAttempt;
  source: "anchor" | "browser";
};

const listeners = new Set<() => void>();
let acceptedHash: string | undefined;
let storeWindow: Window | undefined;
let navigationGuard: HashNavigationGuard | undefined;
let pendingAttempt: PendingAttempt | undefined;
let nextAttemptID = 1;
let browserListenersAttached = false;

function initializeStore() {
  if (typeof window === "undefined") return;
  if (storeWindow !== window) {
    detachBrowserListeners();
    storeWindow = window;
    acceptedHash = window.location.hash;
    pendingAttempt = undefined;
  } else if (listeners.size === 0 && !navigationGuard) {
    // Keep direct hash changes made before a component subscribes observable.
    acceptedHash = window.location.hash;
    pendingAttempt = undefined;
  }
}

function notifyListeners() {
  for (const listener of listeners) listener();
}

function publishHash(hash: string) {
  if (acceptedHash === hash) return;
  acceptedHash = hash;
  notifyListeners();
}

function makeAttempt(to: string, source: PendingAttempt["source"]) {
  const attempt: HashAttempt = {
    id: nextAttemptID++,
    from: acceptedHash ?? "",
    to,
  };
  pendingAttempt = { attempt, source };
  navigationGuard?.onBlocked(attempt);
}

function handleHashChange() {
  if (!storeWindow) return;
  // A hashchange event's newURL can be stale if several navigations were queued.
  const hash = storeWindow.location.hash;
  if (hash === acceptedHash) {
    pendingAttempt = undefined;
    return;
  }
  if (
    pendingAttempt?.source === "browser" &&
    pendingAttempt.attempt.to === hash
  )
    return;
  if (navigationGuard?.shouldBlock()) {
    makeAttempt(hash, "browser");
    return;
  }
  pendingAttempt = undefined;
  publishHash(hash);
}

function handleAnchorClick(event: MouseEvent) {
  if (
    !storeWindow ||
    event.defaultPrevented ||
    event.button !== 0 ||
    event.metaKey ||
    event.ctrlKey ||
    event.shiftKey ||
    event.altKey
  )
    return;

  const targetWindow = storeWindow.document.defaultView;
  if (!targetWindow) return;
  const target = event.target;
  if (!(target instanceof targetWindow.Element)) return;
  const anchor = target.closest("a[href]");
  if (!(anchor instanceof targetWindow.HTMLAnchorElement)) return;
  if (anchor.hasAttribute("download")) return;
  if (anchor.target && anchor.target.toLowerCase() !== "_self") return;

  const targetURL = new URL(anchor.href, storeWindow.location.href);
  const currentURL = new URL(storeWindow.location.href);
  if (
    targetURL.origin !== currentURL.origin ||
    targetURL.pathname !== currentURL.pathname ||
    targetURL.search !== currentURL.search
  )
    return;

  const to = targetURL.hash;
  if (to === acceptedHash || !navigationGuard?.shouldBlock()) return;

  event.preventDefault();
  makeAttempt(to, "anchor");
}

function attachBrowserListeners() {
  if (!storeWindow || browserListenersAttached) return;
  storeWindow.addEventListener("hashchange", handleHashChange);
  if (navigationGuard)
    storeWindow.addEventListener("click", handleAnchorClick, true);
  browserListenersAttached = true;
}

function detachBrowserListeners() {
  if (!storeWindow || !browserListenersAttached) return;
  storeWindow.removeEventListener("hashchange", handleHashChange);
  storeWindow.removeEventListener("click", handleAnchorClick, true);
  browserListenersAttached = false;
}

function stopWhenUnused() {
  if (listeners.size !== 0 || navigationGuard) return;
  detachBrowserListeners();
  pendingAttempt = undefined;
}

function restoreAcceptedHashURL() {
  if (
    !storeWindow ||
    acceptedHash === undefined ||
    storeWindow.location.hash === acceptedHash
  )
    return;
  const acceptedURL = `${storeWindow.location.pathname}${storeWindow.location.search}${acceptedHash}`;
  storeWindow.history.pushState(null, "", acceptedURL);
}

function subscribe(onChange: () => void) {
  initializeStore();
  listeners.add(onChange);
  attachBrowserListeners();
  return () => {
    listeners.delete(onChange);
    stopWhenUnused();
  };
}

function getSnapshot() {
  initializeStore();
  return acceptedHash ?? "";
}

function getServerSnapshot() {
  return "";
}

export function useHashLocation() {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export function registerHashNavigationGuard(
  guard: HashNavigationGuard,
): () => void {
  initializeStore();
  if (navigationGuard && pendingAttempt) restoreAcceptedHashURL();
  const registeredGuard = guard;
  navigationGuard = registeredGuard;
  pendingAttempt = undefined;
  if (storeWindow && browserListenersAttached)
    storeWindow.addEventListener("click", handleAnchorClick, true);
  attachBrowserListeners();

  return () => {
    if (navigationGuard !== registeredGuard) return;
    if (pendingAttempt) restoreAcceptedHashURL();
    navigationGuard = undefined;
    pendingAttempt = undefined;
    if (storeWindow && browserListenersAttached)
      storeWindow.removeEventListener("click", handleAnchorClick, true);
    stopWhenUnused();
  };
}

export function resolveHashNavigation(
  id: number,
  resolution: "stay" | "leave",
): boolean {
  initializeStore();
  const pending = pendingAttempt;
  if (!storeWindow || !pending || pending.attempt.id !== id) return false;

  if (
    pending.source === "browser" &&
    storeWindow.location.hash !== pending.attempt.to
  ) {
    handleHashChange();
    return false;
  }

  pendingAttempt = undefined;
  const { to } = pending.attempt;
  if (resolution === "stay") {
    restoreAcceptedHashURL();
    return true;
  }

  if (pending.source === "anchor") {
    const destinationURL = `${storeWindow.location.pathname}${storeWindow.location.search}${to}`;
    storeWindow.history.pushState(null, "", destinationURL);
  } else if (storeWindow.location.hash !== to) {
    storeWindow.location.hash = to;
  }
  publishHash(to);
  return true;
}
