package models

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

// websiteThemeHeadingScale mirrors the legacy editor's themeService.ts
// HEADING_SCALE: Bootstrap 5.3's own default h1-h6 font-size ratios, used to
// derive h2-h6 from the single "Headings" font-size field the theme panel
// exposes. Keys are iterated in a fixed order so generated CSS is stable.
var websiteThemeHeadingScale = []struct {
	tag   string
	ratio string
}{
	{"h1", "1"}, {"h2", "0.83"}, {"h3", "0.7"}, {"h4", "0.58"}, {"h5", "0.5"}, {"h6", "0.42"},
}

// websiteThemeDefaults mirrors themeService.ts's DEFAULT_THEME_PRESET; every
// field lookup in themeCSSForWebsiteSite falls back to the matching entry
// here so a partially-populated (or entirely empty) tbl.website_site.theme jsonb
// column still renders complete, valid CSS.
var websiteThemeDefaults = map[string]string{
	"colors.main.0":                 "#0d1117",
	"colors.main.1":                 "#f0ad4e",
	"colors.main.2":                 "#ffffff",
	"colors.light":                  "#ffffff",
	"colors.dark":                   "#0d1117",
	"website.mode":                  "light",
	"website.pageLayout":            "full",
	"paragraph.fontSize":            "16px",
	"paragraph.fontFamily":          "Inter, system-ui, sans-serif",
	"paragraph.lineHeight":          "1.5",
	"paragraph.marginTop":           "0px",
	"paragraph.marginBottom":        "16px",
	"headings.fontSize":             "48px",
	"headings.fontFamily":           "'Inter Tight', system-ui, sans-serif",
	"headings.lineHeight":           "1.2",
	"headings.marginTop":            "0px",
	"headings.marginBottom":         "8px",
	"button.primaryStyle":           "fill",
	"button.secondaryStyle":         "fill",
	"button.fontFamily":             "Inter, system-ui, sans-serif",
	"button.paddingY":               "6px",
	"button.paddingX":               "16px",
	"button.fontSize":               "16px",
	"button.roundCorners":           "6.4px",
	"button.onClickEffect":          "none",
	"link.style":                    "underline-hover",
	"input.paddingY":                "6px",
	"input.paddingX":                "12px",
	"input.fontSize":                "16px",
	"input.borderWidth":             "1px",
	"input.borderRadius":            "6.4px",
	"input.background":              "#ffffff",
	"advanced.statusColors.success": "#28a745",
	"advanced.statusColors.info":    "#17a2b8",
	"advanced.statusColors.warning": "#ffc107",
	"advanced.statusColors.danger":  "#dc3545",
	"advanced.grays.0":              "#f8f9fa",
	"advanced.grays.1":              "#dee2e6",
	"advanced.grays.2":              "#adb5bd",
	"advanced.grays.3":              "#495057",
	"advanced.grays.4":              "#212529",
}

var websiteThemeAdvancedShowHeaderDefault = true

// websiteThemeValue walks a dotted path (e.g. "colors.main.0") through the
// theme jsonb map, falling back to websiteThemeDefaults[path] when any
// segment is missing, the wrong type, or an empty string.
func websiteThemeValue(theme map[string]any, path string) string {
	segments := strings.Split(path, ".")
	var cursor any = theme
	for _, segment := range segments {
		switch node := cursor.(type) {
		case map[string]any:
			cursor = node[segment]
		case []any:
			var idx int
			if _, err := fmt.Sscanf(segment, "%d", &idx); err != nil || idx < 0 || idx >= len(node) {
				cursor = nil
			} else {
				cursor = node[idx]
			}
		default:
			cursor = nil
		}
		if cursor == nil {
			break
		}
	}
	if str, ok := cursor.(string); ok && str != "" {
		return str
	}
	return websiteThemeDefaults[path]
}

// websiteThemeShowHeader reads advanced.showHeader (a bool, not covered by
// websiteThemeValue's string defaulting), defaulting to true.
func websiteThemeShowHeader(theme map[string]any) bool {
	advanced, _ := theme["advanced"].(map[string]any)
	if advanced == nil {
		return websiteThemeAdvancedShowHeaderDefault
	}
	if value, ok := boolValue(advanced["showHeader"]); ok {
		return value
	}
	return websiteThemeAdvancedShowHeaderDefault
}

func websiteThemeButtonVars(style, colorVar string) string {
	switch style {
	case "outline":
		return fmt.Sprintf("--bs-btn-bg: transparent; --bs-btn-color: %s; --bs-btn-border-color: %s; --bs-btn-hover-bg: %s; --bs-btn-hover-color: #fff;", colorVar, colorVar, colorVar)
	case "flat":
		return fmt.Sprintf("--bs-btn-bg: transparent; --bs-btn-border-color: transparent; --bs-btn-color: %s;", colorVar)
	default:
		return fmt.Sprintf("--bs-btn-bg: %s; --bs-btn-color: #fff; --bs-btn-border-color: %s;", colorVar, colorVar)
	}
}

func websiteThemeLinkDecoration(style string) string {
	if style == "underline-always" {
		return "underline"
	}
	return "none"
}

// themeCSSForWebsiteSite is the Go port of the legacy editor's
// themeService.ts themePresetToCss: renders tbl.website_site.theme as CSS
// overriding Bootstrap 5's own :root custom properties, so the published
// site's root styles.css matches what the editor canvas/export already
// produce from the same jsonb shape.
func themeCSSForWebsiteSite(theme map[string]any) string {
	primary := websiteThemeValue(theme, "colors.main.0")
	secondary := websiteThemeValue(theme, "colors.main.1")
	tertiary := websiteThemeValue(theme, "colors.main.2")
	isDark := websiteThemeValue(theme, "website.mode") == "dark"
	bodyBg := websiteThemeValue(theme, "colors.light")
	bodyColor := websiteThemeValue(theme, "colors.dark")
	if isDark {
		bodyBg, bodyColor = websiteThemeValue(theme, "colors.dark"), websiteThemeValue(theme, "colors.light")
	}

	var headingRules strings.Builder
	for i, entry := range websiteThemeHeadingScale {
		if i > 0 {
			headingRules.WriteByte('\n')
		}
		headingRules.WriteString(fmt.Sprintf("%s { font-size: calc(%s * %s); }", entry.tag, websiteThemeValue(theme, "headings.fontSize"), entry.ratio))
	}

	linkStyle := websiteThemeValue(theme, "link.style")
	linkHoverDecoration := "underline"
	if linkStyle == "none" {
		linkHoverDecoration = "none"
	}

	onClickEffectRule := ""
	if websiteThemeValue(theme, "button.onClickEffect") == "zoom" {
		onClickEffectRule = ".btn:active { transform: scale(0.96); }"
	}
	headerRule := ""
	if !websiteThemeShowHeader(theme) {
		headerRule = "header, .site-header { display: none; }"
	}
	boxedRule := ""
	if websiteThemeValue(theme, "website.pageLayout") == "boxed" {
		boxedRule = "body { max-width: 1140px; margin-left: auto; margin-right: auto; }"
	}

	return fmt.Sprintf(`:root {
  --bs-primary: %s;
  --bs-secondary: %s;
  --bs-tertiary-color: %s;
  --bs-body-bg: %s;
  --bs-body-color: %s;
  --bs-link-color: %s;
  --bs-success: %s;
  --bs-info: %s;
  --bs-warning: %s;
  --bs-danger: %s;
  --bs-gray-100: %s;
  --bs-gray-300: %s;
  --bs-gray-500: %s;
  --bs-gray-700: %s;
  --bs-gray-900: %s;
  --bs-body-font-family: %s;
  --bs-body-font-size: %s;
  --bs-body-line-height: %s;
  --bs-border-radius: %s;
}
body { margin-top: %s; }
p { margin-top: %s; margin-bottom: %s; }
h1, h2, h3, h4, h5, h6 {
  font-family: %s;
  line-height: %s;
  margin-top: %s;
  margin-bottom: %s;
}
%s
a { text-decoration: %s; }
a:hover { text-decoration: %s; }
.btn {
  --bs-btn-font-family: %s;
  --bs-btn-padding-y: %s;
  --bs-btn-padding-x: %s;
  --bs-btn-font-size: %s;
  --bs-btn-border-radius: %s;
}
.btn-primary { %s }
.btn-secondary { %s }
%s
.form-control, .form-select {
  padding: %s %s;
  font-size: %s;
  border-width: %s;
  border-radius: %s;
  background-color: %s;
}
%s
%s
`,
		primary, secondary, tertiary, bodyBg, bodyColor, primary,
		websiteThemeValue(theme, "advanced.statusColors.success"), websiteThemeValue(theme, "advanced.statusColors.info"),
		websiteThemeValue(theme, "advanced.statusColors.warning"), websiteThemeValue(theme, "advanced.statusColors.danger"),
		websiteThemeValue(theme, "advanced.grays.0"), websiteThemeValue(theme, "advanced.grays.1"),
		websiteThemeValue(theme, "advanced.grays.2"), websiteThemeValue(theme, "advanced.grays.3"), websiteThemeValue(theme, "advanced.grays.4"),
		websiteThemeValue(theme, "paragraph.fontFamily"), websiteThemeValue(theme, "paragraph.fontSize"), websiteThemeValue(theme, "paragraph.lineHeight"),
		websiteThemeValue(theme, "button.roundCorners"),
		websiteThemeValue(theme, "paragraph.marginTop"),
		websiteThemeValue(theme, "paragraph.marginTop"), websiteThemeValue(theme, "paragraph.marginBottom"),
		websiteThemeValue(theme, "headings.fontFamily"), websiteThemeValue(theme, "headings.lineHeight"),
		websiteThemeValue(theme, "headings.marginTop"), websiteThemeValue(theme, "headings.marginBottom"),
		headingRules.String(),
		websiteThemeLinkDecoration(linkStyle), linkHoverDecoration,
		websiteThemeValue(theme, "button.fontFamily"), websiteThemeValue(theme, "button.paddingY"), websiteThemeValue(theme, "button.paddingX"),
		websiteThemeValue(theme, "button.fontSize"), websiteThemeValue(theme, "button.roundCorners"),
		websiteThemeButtonVars(websiteThemeValue(theme, "button.primaryStyle"), "var(--bs-primary)"),
		websiteThemeButtonVars(websiteThemeValue(theme, "button.secondaryStyle"), "var(--bs-secondary)"),
		onClickEffectRule,
		websiteThemeValue(theme, "input.paddingY"), websiteThemeValue(theme, "input.paddingX"), websiteThemeValue(theme, "input.fontSize"),
		websiteThemeValue(theme, "input.borderWidth"), websiteThemeValue(theme, "input.borderRadius"), websiteThemeValue(theme, "input.background"),
		headerRule, boxedRule,
	)
}

// websiteBaseStylesSourcePath is the static base stylesheet shipped with the
// module (disk in dev, embedded in prod; see kernelmodules.ReadModuleResource).
const websiteBaseStylesSourcePath = "website/static/css/styles.css"

// buildWebsiteStylesCSS duplicates the static base stylesheet and appends the
// site's theme-specific CSS to the copy, so shared rules live in a plain CSS
// file and only per-site values are generated in code.
func buildWebsiteStylesCSS(theme map[string]any) (string, error) {
	base, err := kernelmodules.ReadModuleResource(websiteBaseStylesSourcePath)
	if err != nil {
		return "", fmt.Errorf("read website base stylesheet %q: %w", websiteBaseStylesSourcePath, err)
	}
	return strings.TrimRight(string(base), "\n") + "\n\n" + themeCSSForWebsiteSite(theme), nil
}

// websiteThemeValueRe is the whole vocabulary a theme value may use: colors, lengths, numbers and
// font stacks. Theme values are interpolated into the generated stylesheet, so anything that could
// end a declaration or open another rule (; { } < > \ @) is rejected.
var websiteThemeValueRe = regexp.MustCompile(`^[#%.,\-\w\s()'"]*$`)

// validateWebsiteTheme rejects theme values that could inject CSS. Non-string leaves are fine.
func validateWebsiteTheme(node any, path string) error {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if err := validateWebsiteTheme(child, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range v {
			if err := validateWebsiteTheme(child, fmt.Sprintf("%s.%d", path, i)); err != nil {
				return err
			}
		}
	case string:
		lowered := strings.ToLower(v)
		if !websiteThemeValueRe.MatchString(v) || strings.Contains(lowered, "url(") || strings.Contains(lowered, "expression(") {
			return fmt.Errorf("theme value at %s contains characters that are not allowed", strings.TrimPrefix(path, "."))
		}
	}
	return nil
}

// previewWebsiteThemeCSS is the model-level `preview_theme_css` RuntimeMethod: renders the CSS a
// draft theme (kwargs theme) would publish, so the builder canvas can show it before saving.
func previewWebsiteThemeCSS(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	theme, _ := kwargs["theme"].(map[string]any)
	if err := validateWebsiteTheme(theme, ""); err != nil {
		return nil, err
	}
	return map[string]any{"css": themeCSSForWebsiteSite(theme)}, nil
}
