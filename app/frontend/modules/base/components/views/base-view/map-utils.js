// map-utils.js — arch parsing for <map latitude_field="..." longitude_field="..."
// panel_title="..." hide_name="0|1" hide_address="0|1"><field name="..."/></map>
// plus marker-list building from a flat search_read result. No RPC calls here
// (fetch stays in jsonv2.js/ModelView.js) and no geocoding: v1 only ever
// plots records that already have numeric lat/lng stored, per the plan.

// parseMapArchConfig reads the <map> root's attributes and its child <field>
// list (the popup's field list), mirroring gantt-utils.js's parseGanttArchConfig.
function parseMapArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "map") return {};

  const popupFields = Array.from(root.querySelectorAll("field"))
    .map((field) => field.getAttribute("name")?.trim() || "")
    .filter((name) => name.length > 0);

  return {
    latitudeField: root.getAttribute("latitude_field")?.trim() || undefined,
    longitudeField: root.getAttribute("longitude_field")?.trim() || undefined,
    panelTitle: root.getAttribute("panel_title")?.trim() || undefined,
    hideName: root.getAttribute("hide_name") === "1",
    hideAddress: root.getAttribute("hide_address") === "1",
    popupFields,
  };
}

// resolveMapViewData validates the arch's required lat/lng fields against
// fields_get metadata and returns the config the view needs to render.
// Returns null when the model has no usable lat/lng field pair.
export function resolveMapViewData(viewData) {
  const archConfig = parseMapArchConfig(viewData?.arch);
  const fields = viewData?.fields ?? {};
  if (!archConfig.latitudeField || !archConfig.longitudeField) return null;
  if (!fields[archConfig.latitudeField] || !fields[archConfig.longitudeField]) return null;
  return archConfig;
}

// toCoordinate reads a stored lat/lng value as a finite number, or null when
// the field is unset/non-numeric — records failing either coordinate are
// dropped from the marker list (v1 has no geocoding fallback).
function toCoordinate(value) {
  const number = Number(value);
  return value !== null && value !== undefined && value !== false && Number.isFinite(number) ? number : null;
}

// buildMapMarkers filters the fetched records to those with both coordinates
// present and pairs each with its {lat, lng} for the map/side-list to share.
export function buildMapMarkers(records, latitudeField, longitudeField) {
  return records.flatMap((record) => {
    const lat = toCoordinate(record[latitudeField]);
    const lng = toCoordinate(record[longitudeField]);
    return lat !== null && lng !== null ? [{ record, lat, lng }] : [];
  });
}
