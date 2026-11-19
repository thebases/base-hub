// Shared kanban color-index palette: the index stored in a `color` int field
// (see the kanban_color_picker widget) maps to one of these hex values.
export const KANBAN_COLOR_PALETTE = ["#ffffff", "#f06050", "#f4a460", "#f7cd1f", "#6cc1ed", "#814968", "#eb7e7f", "#2c8397", "#475577", "#d6145f"];

// paletteColorForIndex returns the hex for a stored color index, or "" when the
// value is not a usable index or is 0 ("no color").
export function paletteColorForIndex(value) {
  const index = Number(value);
  if (value === null || value === undefined || value === "" || !Number.isInteger(index) || index <= 0) return "";
  return KANBAN_COLOR_PALETTE[index] ?? "";
}
