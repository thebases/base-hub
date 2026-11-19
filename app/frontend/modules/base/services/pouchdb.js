// Port of frontend-admin's modules/base/services/pouchdb.ts. Wraps the global
// window.PouchDB (loaded on demand from "./vendor/pouchdb-9.0.0.min.js" by
// services/script-loader.js) with a singleton IndexedDB handle for retained backend bus
// notifications. This app has no TypeScript, so getNotificationDb() throws at
// call time instead of relying on compile-time optionality of window.PouchDB.

import { loadPouchDB } from "@/services/script-loader.js";

const DB_NAME = "basehub_notifications";
const RETENTION_DAYS = 7;
const RETENTION_MS = RETENTION_DAYS * 24 * 60 * 60 * 1000;

let db = null;

// getNotificationDb loads the vendored PouchDB script on first use (index.html no longer ships it,
// so /login and /web cold loads skip its ~136KB parse) and keeps a singleton handle for all
// notification reads and writes.
async function getNotificationDb() {
  if (db) return db;
  await loadPouchDB();
  if (!window.PouchDB) {
    throw new Error("PouchDB script has not loaded");
  }
  db ??= new window.PouchDB(DB_NAME);
  return db;
}

// notificationDocId creates a stable document id so repeated bus deliveries
// update the same notification instead of duplicating rows.
function notificationDocId(notification) {
  return `bus:${notification.id}:${notification.type}`;
}

// toStoredNotification converts a backend bus notification into the retained
// IndexedDB document shape used by the notifications screen.
function toStoredNotification(notification) {
  const receivedAt = new Date();
  const expiresAt = new Date(receivedAt.getTime() + RETENTION_MS);
  return {
    _id: notificationDocId(notification),
    sourceId: notification.id,
    type: notification.type,
    payload: notification.payload,
    receivedAt: receivedAt.toISOString(),
    expiresAt: expiresAt.toISOString(),
  };
}

// saveNotification writes one backend notification to IndexedDB through
// PouchDB and tolerates duplicate delivery from polling or multiple tabs.
export async function saveNotification(notification) {
  const database = await getNotificationDb();
  try {
    await database.put(toStoredNotification(notification));
  } catch (error) {
    if (error && error.status === 409) {
      await pruneExpiredNotifications();
      return;
    }
    throw error;
  }
  await pruneExpiredNotifications();
}

// listNotifications returns non-expired notification documents newest first and
// performs lazy retention cleanup before rendering the screen.
export async function listNotifications() {
  await pruneExpiredNotifications();
  const result = await (await getNotificationDb()).allDocs({ include_docs: true });
  return result.rows
    .map((row) => row.doc)
    .filter((doc) => Boolean(doc))
    .sort((a, b) => b.receivedAt.localeCompare(a.receivedAt));
}

// pruneExpiredNotifications removes documents older than the seven-day
// retention window so IndexedDB does not grow without bound.
async function pruneExpiredNotifications() {
  const now = new Date().toISOString();
  const result = await (await getNotificationDb()).allDocs({ include_docs: true });
  const expired = result.rows.flatMap((row) => {
    const doc = row.doc;
    if (!doc || doc.expiresAt > now) return [];
    return [{ _id: doc._id, _rev: doc._rev, _deleted: true }];
  });
  if (expired.length > 0) {
    await (await getNotificationDb()).bulkDocs(expired);
  }
}
