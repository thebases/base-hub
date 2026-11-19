// Server time helpers: the API returns every date/datetime as UTC Unix seconds (integer).
// Strings are still accepted so legacy values and user-typed input keep working.

// serverTimeToDate converts a server value into a Date, or null when empty/unparsable.
// Numbers are Unix seconds; magnitudes >= 1e11 are treated as epoch milliseconds.
export function serverTimeToDate(value) {
  if (value === null || value === undefined || value === "" || value === false) return null;
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value;
  if (typeof value === "number") {
    const date = new Date(Math.abs(value) >= 1e11 ? value : value * 1000);
    return Number.isNaN(date.getTime()) ? null : date;
  }
  const text = String(value).trim();
  // A timestamp without an explicit zone (Z / ±hh:mm) is server UTC.
  const isNaive = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/.test(text);
  const date = new Date(isNaive ? `${text.replace(" ", "T")}Z` : text);
  return Number.isNaN(date.getTime()) ? null : date;
}

// serverTimeToInputValue renders a server value for <input type=date|datetime-local> (UTC, as before).
export function serverTimeToInputValue(value, kind) {
  const date = serverTimeToDate(value);
  if (!date) return typeof value === "string" ? value : "";
  const iso = date.toISOString();
  return kind === "datetime" ? iso.slice(0, 16) : iso.slice(0, 10);
}

// displayTimeZone is the IANA zone of the logged-in user's address (state, else country, resolved by
// the backend; "" -> browser zone). It is read
// from the persisted auth state so this module stays free of an import cycle with the auth store.
// Stored times are always UTC; only display and datetime input conversion use this zone.
function displayTimeZone() {
  try {
    const raw = sessionStorage.getItem("basehub-auth");
    if (raw === zoneRaw) return zoneValue;
    const zone = String(JSON.parse(raw || "null")?.state?.timezone || "").trim();
    if (zone) zoneFormatter(zone); // throws RangeError on a zone this browser does not know
    zoneRaw = raw;
    zoneValue = zone;
    return zone;
  } catch {
    return "";
  }
}

let zoneRaw;
let zoneValue = "";
const zoneFormatters = new Map();

// zoneFormatter returns the (cached) wall-clock formatter for a zone; building one per date cell was a hot spot.
function zoneFormatter(zone) {
  let formatter = zoneFormatters.get(zone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-GB", {
      timeZone: zone, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit",
    });
    zoneFormatters.set(zone, formatter);
  }
  return formatter;
}

// zonedParts splits a Date into wall-clock numbers in zone (browser zone when blank).
function zonedParts(date, zone) {
  if (!zone) {
    return { year: date.getFullYear(), month: date.getMonth() + 1, day: date.getDate(), hour: date.getHours(), minute: date.getMinutes(), second: date.getSeconds() };
  }
  const parts = {};
  for (const part of zoneFormatter(zone).formatToParts(date)) {
    if (part.type !== "literal") parts[part.type] = Number(part.value);
  }
  return parts;
}

// pad2 zero-pads one date/time part.
function pad2(part) {
  return String(part).padStart(2, "0");
}

// formatLocalDateTime renders a server UTC timestamp in the viewer's local
// display zone (the logged-in user's country timezone) as "dd-MM-yyyy HH:mm:ss" ("dd-MM HH:mm:ss" when `short`). Empty -> "";
// an unparsable value falls back to its raw text instead of "Invalid Date".
export function formatLocalDateTime(value, short = false) {
  const date = serverTimeToDate(value);
  if (!date) return value === null || value === undefined || value === false ? "" : String(value).trim();
  const p = zonedParts(date, displayTimeZone());
  const day = `${pad2(p.day)}-${pad2(p.month)}`;
  const time = `${pad2(p.hour)}:${pad2(p.minute)}:${pad2(p.second)}`;
  return short ? `${day} ${time}` : `${day}-${p.year} ${time}`;
}

// serverTimeToLocalInputValue renders a server value for <input type=datetime-local>
// in the viewer's local zone ("YYYY-MM-DDTHH:mm").
export function serverTimeToLocalInputValue(value) {
  const date = serverTimeToDate(value);
  if (!date) return typeof value === "string" ? value : "";
  const p = zonedParts(date, displayTimeZone());
  return `${p.year}-${pad2(p.month)}-${pad2(p.day)}T${pad2(p.hour)}:${pad2(p.minute)}`;
}

// localInputToServerTime converts a datetime-local string (viewer's local zone)
// into UTC Unix seconds, the form the backend accepts on write; "" stays "" and
// an unparsable string is returned unchanged.
export function localInputToServerTime(text) {
  if (typeof text !== "string" || text.trim() === "") return "";
  const zone = displayTimeZone();
  let ms = new Date(text).getTime();
  if (Number.isNaN(ms)) return text;
  if (zone) {
    // Read the typed wall-clock as UTC, then subtract the zone's offset at that instant (twice,
    // so a DST edge settles on the right side).
    const m = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?/.exec(text.trim());
    if (!m) return Math.floor(ms / 1000);
    const wall = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +(m[6] || 0));
    let utc = wall;
    for (let i = 0; i < 2; i++) {
      const p = zonedParts(new Date(utc), zone);
      utc = wall - (Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - utc);
    }
    ms = utc;
  }
  return Math.floor(ms / 1000);
}
