// Port of frontend-admin's modules/base-bus/multi-tab.ts — byte-faithful
// leader-election logic (localStorage-based heartbeat/election), types
// dropped. Only one browser tab should hold the live bus connection at a
// time; peers observe ownership via the same localStorage keys.

import { createBusEventTarget } from "./event-bus.js";

const LAST_PRESENCE_KEY = "basehub.bus.lastPresenceByTab";
const MAIN_KEY = "basehub.bus.main";
const HEARTBEAT_KEY = "basehub.bus.heartbeat";
const TAB_HEARTBEAT_PERIOD = 10_000;
const MAIN_TAB_HEARTBEAT_PERIOD = 1_500;
const HEARTBEAT_OUT_OF_DATE_PERIOD = 5_000;
const HEARTBEAT_KILL_OLD_PERIOD = 15_000;

// readPresenceMap parses the localStorage election map and falls back to an
// empty object if another tab wrote malformed data.
function readPresenceMap() {
  try {
    return JSON.parse(localStorage.getItem(LAST_PRESENCE_KEY) ?? "{}");
  } catch {
    return {};
  }
}

// writePresenceMap stores the tab election map for peers in other browser tabs.
function writePresenceMap(value) {
  localStorage.setItem(LAST_PRESENCE_KEY, JSON.stringify(value));
}

// readHeartbeat returns the last main-tab heartbeat timestamp as a number.
function readHeartbeat() {
  return Number(localStorage.getItem(HEARTBEAT_KEY) ?? "0");
}

// createMultiTabService ports legacy fallback leader election so only one tab
// performs authenticated HTTP polling (or opens the live WebSocket) while
// peers can still observe ownership.
export function createMultiTabService(name = "basehub_bus") {
  const bus = createBusEventTarget();
  const now = Date.now();
  const tabId = `${name}:${now}:${Math.random().toString(36).slice(2)}`;
  let heartbeatTimeout;
  let isMainTab = false;
  let lastHeartbeat = 0;

  // startElection promotes this tab when it is the oldest live peer.
  function startElection() {
    if (isMainTab) return;
    const electionNow = Date.now();
    const lastPresenceByTab = readPresenceMap();
    const killBefore = electionNow - HEARTBEAT_KILL_OLD_PERIOD;
    const nextTab = Object.entries(lastPresenceByTab).find(
      ([, lastPresence]) => lastPresence >= killBefore
    )?.[0];

    if (nextTab === tabId) {
      lastHeartbeat = electionNow;
      localStorage.setItem(HEARTBEAT_KEY, String(lastHeartbeat));
      localStorage.setItem(MAIN_KEY, "true");
      isMainTab = true;
      delete lastPresenceByTab[tabId];
      writePresenceMap(lastPresenceByTab);
      bus.trigger("become_main_tab");
    }
  }

  // heartbeat maintains this tab's liveness and demotes duplicate main tabs.
  function heartbeat() {
    const heartbeatNow = Date.now();
    const lastPresenceByTab = readPresenceMap();
    let heartbeatValue = readHeartbeat();

    if (heartbeatValue + HEARTBEAT_OUT_OF_DATE_PERIOD < heartbeatNow) {
      startElection();
      heartbeatValue = readHeartbeat();
    }

    if (isMainTab) {
      const cleanedTabs = Object.fromEntries(
        Object.entries(lastPresenceByTab).filter(
          ([, lastPresence]) => lastPresence + HEARTBEAT_KILL_OLD_PERIOD > heartbeatNow
        )
      );

      if (heartbeatValue !== lastHeartbeat) {
        isMainTab = false;
        lastHeartbeat = 0;
        cleanedTabs[tabId] = heartbeatNow;
        writePresenceMap(cleanedTabs);
        bus.trigger("no_longer_main_tab");
      } else {
        lastHeartbeat = heartbeatNow;
        localStorage.setItem(HEARTBEAT_KEY, String(heartbeatNow));
        writePresenceMap(cleanedTabs);
      }
    } else {
      lastPresenceByTab[tabId] = heartbeatNow;
      writePresenceMap(lastPresenceByTab);
    }

    heartbeatTimeout = window.setTimeout(
      heartbeat,
      isMainTab ? MAIN_TAB_HEARTBEAT_PERIOD : TAB_HEARTBEAT_PERIOD
    );
  }

  // unregister removes this tab from the election and releases main ownership.
  function unregisterTab() {
    window.clearTimeout(heartbeatTimeout);
    const lastPresenceByTab = readPresenceMap();
    delete lastPresenceByTab[tabId];
    writePresenceMap(lastPresenceByTab);

    if (isMainTab) {
      isMainTab = false;
      bus.trigger("no_longer_main_tab");
      localStorage.removeItem(MAIN_KEY);
    }
  }

  // onStorage reacts when a main tab disappears and starts a replacement vote.
  function onStorage({ key, newValue }) {
    if (key === MAIN_KEY && !newValue) {
      startElection();
    }
  }

  const lastPresenceByTab = readPresenceMap();
  lastPresenceByTab[tabId] = now;
  writePresenceMap(lastPresenceByTab);
  if (!localStorage.getItem(MAIN_KEY)) {
    startElection();
  }
  heartbeat();
  window.addEventListener("pagehide", unregisterTab);
  window.addEventListener("storage", onStorage);

  return {
    bus,
    async isOnMainTab() {
      return isMainTab;
    },
    unregister() {
      window.removeEventListener("pagehide", unregisterTab);
      window.removeEventListener("storage", onStorage);
      unregisterTab();
    },
  };
}
