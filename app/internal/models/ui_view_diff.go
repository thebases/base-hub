package models

// File overview:
// - Responsibility: renders arch comparison output (pretty XML and line diff HTML) for ui.view compare.
// - Key symbols: `formatUIViewArchForCompare`, `renderPrettyArchXML`, `buildUIViewArchDiffHTML`, `buildUIViewLineDiff`.

import (
	"fmt"
	"html"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

func formatUIViewArchForCompare(arch string) string {
	arch = strings.TrimSpace(arch)
	if arch == "" {
		return ""
	}
	root, err := parseArchXML(arch)
	if err != nil {
		return arch
	}
	return renderPrettyArchXML(root, 0)
}

// renderPrettyArchXML renders the pretty arch xml handled by this package.
func renderPrettyArchXML(node *uiXMLNode, depth int) string {
	if node == nil {
		return ""
	}
	if node.Name == "" {
		text := strings.TrimSpace(node.Text)
		if text == "" {
			return ""
		}
		return strings.Repeat("  ", depth) + text
	}

	indent := strings.Repeat("  ", depth)
	var builder strings.Builder
	builder.WriteString(indent)
	builder.WriteString("<")
	builder.WriteString(node.Name)
	for _, attr := range node.Attr {
		builder.WriteString(" ")
		builder.WriteString(attr.Name.Local)
		builder.WriteString(`="`)
		builder.WriteString(xmlEscape(attr.Value))
		builder.WriteString(`"`)
	}
	if len(node.Children) == 0 {
		builder.WriteString("/>")
		return builder.String()
	}

	inlineTextOnly := true
	for _, child := range node.Children {
		if child.Name != "" {
			inlineTextOnly = false
			break
		}
		if strings.TrimSpace(child.Text) == "" {
			continue
		}
	}
	if inlineTextOnly {
		builder.WriteString(">")
		for _, child := range node.Children {
			builder.WriteString(xmlEscape(strings.TrimSpace(child.Text)))
		}
		builder.WriteString("</")
		builder.WriteString(node.Name)
		builder.WriteString(">")
		return builder.String()
	}

	builder.WriteString(">\n")
	lines := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		rendered := renderPrettyArchXML(child, depth+1)
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		lines = append(lines, rendered)
	}
	builder.WriteString(strings.Join(lines, "\n"))
	builder.WriteString("\n")
	builder.WriteString(indent)
	builder.WriteString("</")
	builder.WriteString(node.Name)
	builder.WriteString(">")
	return builder.String()
}

// buildUIViewArchDiffHTML builds the ui view arch diff html handled by this package.
func buildUIViewArchDiffHTML(currentArch, compareArch string) string {
	if currentArch == compareArch {
		return ""
	}
	diff := buildUIViewLineDiff(currentArch, compareArch)
	if diff == "" {
		return ""
	}
	return "<pre>" + html.EscapeString(diff) + "</pre>"
}

// buildUIViewLineDiff builds the ui view line diff handled by this package.
func buildUIViewLineDiff(currentArch, compareArch string) string {
	currentLines := strings.Split(strings.ReplaceAll(currentArch, "\r\n", "\n"), "\n")
	compareLines := strings.Split(strings.ReplaceAll(compareArch, "\r\n", "\n"), "\n")
	lcs := buildUIViewLineLCS(currentLines, compareLines)

	var builder strings.Builder
	i, j := 0, 0
	for i < len(currentLines) && j < len(compareLines) {
		if currentLines[i] == compareLines[j] {
			builder.WriteString("  ")
			builder.WriteString(currentLines[i])
			builder.WriteByte('\n')
			i++
			j++
			continue
		}
		if lcs[i+1][j] >= lcs[i][j+1] {
			builder.WriteString("- ")
			builder.WriteString(currentLines[i])
			builder.WriteByte('\n')
			i++
			continue
		}
		builder.WriteString("+ ")
		builder.WriteString(compareLines[j])
		builder.WriteByte('\n')
		j++
	}
	for ; i < len(currentLines); i++ {
		builder.WriteString("- ")
		builder.WriteString(currentLines[i])
		builder.WriteByte('\n')
	}
	for ; j < len(compareLines); j++ {
		builder.WriteString("+ ")
		builder.WriteString(compareLines[j])
		builder.WriteByte('\n')
	}
	return strings.TrimRight(builder.String(), "\n")
}

// buildUIViewLineLCS builds the ui view line lcs handled by this package.
func buildUIViewLineLCS(left, right []string) [][]int {
	dp := make([][]int, len(left)+1)
	for i := range dp {
		dp[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp
}

func normalizeArchText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if strings.Contains(strings.ToLower(text), "<?xml") && !viewXMLDeclarationPattern.MatchString(text) {
		log.Errorf("ui.view XML declaration must be at the start of the arch")
		return "", fmt.Errorf("ui.view XML declaration must be at the start of the arch")
	}
	return strings.TrimSpace(viewXMLDeclarationPattern.ReplaceAllString(text, "")), nil
}
