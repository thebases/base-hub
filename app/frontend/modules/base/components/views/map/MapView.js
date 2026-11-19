// MapView — Leaflet pin map + a synced side list of the same records. v1
// scope only (see the plan's Map MVP cut list): pins from stored lat/lng
// fields, no geocoding, no routing/route-lines, no drag-to-resequence, no
// marker clustering. The OSM tile layer's attribution control is required by
// OSM's tile usage policy, not optional polish.
import { Component, loadTemplate, onMounted, onPatched, onWillUnmount, useRef, useState, xml } from "nodexui/owl";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { loadLeaflet } from "./leaflet-loader.js";


const template = await loadTemplate(new URL("./MapView.xml", import.meta.url));

const OSM_TILE_URL = "https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png";
const OSM_ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener">OpenStreetMap</a> contributors';

// displayValue mirrors KanbanRecord.js's helper: many2one tuples show their
// label, everything else is stringified as-is.
function displayValue(value) {
  if (value === null || value === undefined || value === false) return "";
  if (Array.isArray(value) && value.length === 2) return String(value[1]);
  return String(value);
}

// escapeHtml guards the hand-built popup markup below from field values that
// contain HTML-significant characters (Leaflet popup content is raw HTML).
function escapeHtml(value) {
  return value.replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
}

export class MapView extends Component {
  static template = xml({ raw: [template] });
  static components = { NoContent };

  setup() {
    this.mapRef = useRef("mapContainer");
    this.state = useState({ selectedId: null, error: "" });
    this.map = null;
    this.markersById = new Map();
    this.renderedMarkers = null;

    onMounted(() => void this.initMap());
    // The map is only meaningfully re-rendered when the marker list itself
    // changes (e.g. a filter/refresh) — renderMarkers() no-ops otherwise.
    onPatched(() => this.renderMarkers());
    onWillUnmount(() => this.map?.remove());
  }

  async initMap() {
    try {
      const L = await loadLeaflet();
      this.L = L;
      const container = this.mapRef.el;
      this.map = L.map(container);
      L.tileLayer(OSM_TILE_URL, { maxZoom: 19, attribution: OSM_ATTRIBUTION }).addTo(this.map);
      // Leaflet mounts popup DOM into the map's own container, so a single
      // delegated listener catches every marker popup's "Open record" link.
      container.addEventListener("click", (event) => {
        const link = event.target.closest("[data-open-record-id]");
        if (!link) return;
        event.preventDefault();
        const marker = this.props.markers.find((item) => String(item.record.id) === link.dataset.openRecordId);
        if (marker) this.props.onOpenRecord(marker.record);
      });
      this.renderMarkers();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to load the map");
    }
  }

  // renderMarkers rebuilds the marker layer only when the props.markers
  // reference actually changed, so onPatched (fired on any parent re-render)
  // doesn't churn the map on every unrelated state update.
  renderMarkers() {
    if (!this.map || this.props.markers === this.renderedMarkers) return;
    this.renderedMarkers = this.props.markers;
    for (const marker of this.markersById.values()) marker.remove();
    this.markersById.clear();
    for (const item of this.props.markers) {
      const marker = this.L.marker([item.lat, item.lng]).addTo(this.map);
      marker.bindPopup(this.popupHtml(item.record));
      marker.on("click", () => (this.state.selectedId = item.record.id));
      this.markersById.set(item.record.id, marker);
    }
    this.fitBounds();
  }

  fitBounds() {
    const points = this.props.markers.map((item) => [item.lat, item.lng]);
    if (points.length === 0) this.map.setView([0, 0], 2);
    else this.map.fitBounds(points, { padding: [24, 24], maxZoom: 14 });
  }

  // popupHtml builds the marker's popup: the record's name (unless
  // hide_name), a generic "address" field if present (unless hide_address),
  // then the arch's configured popup fields, then a link to the record's form.
  popupHtml(record) {
    const lines = [];
    if (!this.props.hideName) lines.push(`<div class="basehub-map-popup-title">${escapeHtml(displayValue(record.display_name ?? record.name))}</div>`);
    if (!this.props.hideAddress && record.address) lines.push(`<div class="basehub-map-popup-address">${escapeHtml(displayValue(record.address))}</div>`);
    for (const name of this.props.popupFields) {
      const value = displayValue(record[name]);
      if (value) lines.push(`<div class="basehub-map-popup-field">${escapeHtml(value)}</div>`);
    }
    lines.push(`<a href="#" data-open-record-id="${record.id}">${escapeHtml(t("Open record"))}</a>`);
    return lines.join("");
  }

  selectRow(record) {
    this.state.selectedId = record.id;
    const marker = this.markersById.get(record.id);
    if (!marker) return;
    this.map.panTo(marker.getLatLng());
    marker.openPopup();
  }

  displayName(record) {
    return displayValue(record.display_name ?? record.name) || `#${record.id}`;
  }
}
