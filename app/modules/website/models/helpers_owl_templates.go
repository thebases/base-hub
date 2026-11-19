package models

// File overview:
// - Responsibility: parses the Owl template files modules ship for builder blocks
//   (`static/snippets/*.xml`) and page templates (`static/templates/*.xml`).
// - File shape: `<templates name=".." group=".." ...><t t-name="module.kind.file">markup</t></templates>`.
//   Metadata lives as attributes on the `<templates>` root (Owl ignores them); the markup is the
//   body of the `<t t-name>` element, written as well-formed XML (void tags self-closed, other
//   elements always closed explicitly so the HTML sanitizer reads them the same way).
// - Key symbols: `parseOwlTemplateFile`, `owlTemplateFile`.

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type owlTemplateFile struct {
	XMLName   xml.Name        `xml:"templates"`
	Attrs     []xml.Attr      `xml:",any,attr"`
	Templates []owlTemplateEl `xml:"t"`
}

type owlTemplateEl struct {
	Name  string `xml:"t-name,attr"`
	Inner string `xml:",innerxml"`
}

// owlTemplate is a parsed file: root metadata plus the markup of its first `t-name` template.
type owlTemplate struct {
	Meta map[string]string
	HTML string
}

func parseOwlTemplateFile(raw []byte) (*owlTemplate, error) {
	var f owlTemplateFile
	if err := xml.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	if len(f.Templates) == 0 || strings.TrimSpace(f.Templates[0].Name) == "" {
		return nil, fmt.Errorf("no <t t-name> template found")
	}
	meta := make(map[string]string, len(f.Attrs))
	for _, a := range f.Attrs {
		meta[a.Name.Local] = a.Value
	}
	return &owlTemplate{Meta: meta, HTML: strings.TrimSpace(f.Templates[0].Inner)}, nil
}

func (t *owlTemplate) sequence() int {
	n, _ := strconv.Atoi(strings.TrimSpace(t.Meta["sequence"]))
	return n
}

func (t *owlTemplate) keywords() []string {
	var out []string
	for _, k := range strings.Split(t.Meta["keywords"], ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}
