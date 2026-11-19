package models

// File overview:
// - Responsibility: parses, patches (xpath-style operations) and renders view arch XML.
// - Key symbols: `uiXMLNode`, `uiXPathSelector`, `parseArchXML`, `mergeUIViewArch`.

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

type uiXMLNode struct {
	Name     string
	Attr     []xml.Attr
	Children []*uiXMLNode
	Text     string
	Parent   *uiXMLNode
}

type uiXPathSelector struct {
	Tag     string
	AttrKey string
	AttrVal string
}

// parseArchXML parses the arch xml handled by this package.
func parseArchXML(raw string) (*uiXMLNode, error) {
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var root *uiXMLNode
	stack := make([]*uiXMLNode, 0)
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			node := &uiXMLNode{Name: typed.Name.Local, Attr: append([]xml.Attr(nil), typed.Attr...)}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				node.Parent = parent
				parent.Children = append(parent.Children, node)
			} else {
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			text := string([]byte(typed))
			if strings.TrimSpace(text) == "" {
				continue
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, &uiXMLNode{Text: text, Parent: parent})
		}
	}
	if root == nil {
		log.Errorf("ui.view arch is empty")
		return nil, fmt.Errorf("ui.view arch is empty")
	}
	return root, nil
}

// mergeUIViewArch merges the ui view arch handled by this package.
func mergeUIViewArch(baseArch, extensionArch string) (string, error) {
	baseRoot, err := parseArchXML(baseArch)
	if err != nil {
		return "", err
	}
	opsRoot, err := parseArchXML(extensionArch)
	if err != nil {
		return "", err
	}
	// Walk each viewoperations(opsroot) entry in order so validation and side effects stay scoped to the current element.
	for _, op := range viewOperations(opsRoot) {
		if err := applyViewOperation(baseRoot, op); err != nil {
			return "", err
		}
	}
	return renderArchXML(baseRoot), nil
}

// renderArchXML renders the arch xml handled by this package.
func renderArchXML(node *uiXMLNode) string {
	if node == nil {
		return ""
	}
	if node.Name == "" {
		return xmlEscape(node.Text)
	}
	var builder strings.Builder
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
	builder.WriteString(">")
	for _, child := range node.Children {
		builder.WriteString(renderArchXML(child))
	}
	builder.WriteString("</")
	builder.WriteString(node.Name)
	builder.WriteString(">")
	return builder.String()
}

func viewOperations(root *uiXMLNode) []*uiXMLNode {
	if root == nil {
		return nil
	}
	if root.Name == "xpath" || operationPosition(root) != "" {
		return []*uiXMLNode{root}
	}
	ops := make([]*uiXMLNode, 0)
	for _, child := range root.Children {
		if child.Name == "" {
			continue
		}
		ops = append(ops, child)
	}
	if len(ops) == 0 {
		return []*uiXMLNode{root}
	}
	return ops
}

// applyViewOperation applies the view operation handled by this package.
func applyViewOperation(root, op *uiXMLNode) error {
	selector, err := operationSelector(op)
	if err != nil {
		return err
	}
	targets := findTargets(root, selector)
	if len(targets) == 0 {
		log.Errorf("ui.view xpath %q did not match any node", operationExpr(op))
		return fmt.Errorf("ui.view xpath %q did not match any node", operationExpr(op))
	}

	switch position := operationPosition(op); position {
	case "", "inside":
		for _, target := range targets {
			for _, child := range payloadNodes(op) {
				cloned := cloneNode(child)
				cloned.Parent = target
				target.Children = append(target.Children, cloned)
			}
		}
		return nil
	case "attributes":
		for _, target := range targets {
			for _, child := range op.Children {
				if child.Name != "attribute" {
					continue
				}
				name := attrValue(child, "name")
				if name == "" {
					continue
				}
				setNodeAttr(target, name, innerText(child))
			}
		}
		return nil
	case "replace", "before", "after":
		for _, target := range targets {
			if target.Parent == nil {
				log.Errorf("ui.view cannot apply %s on root node", position)
				return fmt.Errorf("ui.view cannot apply %s on root node", position)
			}
			replacements := cloneNodes(payloadNodes(op))
			for _, replacement := range replacements {
				replacement.Parent = target.Parent
			}
			target.Parent.Children = spliceChildren(target.Parent.Children, target, replacements, position)
		}
		return nil
	default:
		log.Errorf("ui.view unsupported xpath position %q", position)
		return fmt.Errorf("ui.view unsupported xpath position %q", position)
	}
}

func operationSelector(op *uiXMLNode) (uiXPathSelector, error) {
	expr := operationExpr(op)
	if expr == "" {
		log.Errorf("ui.view extension node requires expr or a matching element selector")
		return uiXPathSelector{}, fmt.Errorf("ui.view extension node requires expr or a matching element selector")
	}
	return parseSelector(expr)
}

func operationExpr(op *uiXMLNode) string {
	if op == nil {
		return ""
	}
	if expr := attrValue(op, "expr"); expr != "" {
		return expr
	}
	if name := attrValue(op, "name"); name != "" {
		return fmt.Sprintf("//%s[@name='%s']", op.Name, name)
	}
	if id := attrValue(op, "id"); id != "" {
		return fmt.Sprintf("//%s[@id='%s']", op.Name, id)
	}
	if op.Name == "" {
		return ""
	}
	return "//" + op.Name
}

// parseSelector parses the selector handled by this package.
func parseSelector(expr string) (uiXPathSelector, error) {
	expr = strings.TrimSpace(strings.TrimPrefix(expr, "."))
	expr = strings.TrimPrefix(expr, "/")
	expr = strings.TrimPrefix(expr, "/")
	if expr == "" {
		log.Errorf("empty xpath expr")
		return uiXPathSelector{}, fmt.Errorf("empty xpath expr")
	}
	selector := uiXPathSelector{Tag: expr}
	if bracket := strings.Index(expr, "["); bracket >= 0 {
		selector.Tag = strings.TrimSpace(expr[:bracket])
		predicate := strings.TrimSuffix(strings.TrimSpace(expr[bracket+1:]), "]")
		if strings.HasPrefix(predicate, "@") {
			parts := strings.SplitN(strings.TrimPrefix(predicate, "@"), "=", 2)
			if len(parts) == 2 {
				selector.AttrKey = strings.TrimSpace(parts[0])
				selector.AttrVal = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			}
		}
	}
	if selector.Tag == "" {
		selector.Tag = "*"
	}
	return selector, nil
}

// findTargets finds the targets handled by this package.
func findTargets(root *uiXMLNode, selector uiXPathSelector) []*uiXMLNode {
	matches := make([]*uiXMLNode, 0)
	var walk func(node *uiXMLNode)
	walk = func(node *uiXMLNode) {
		if node == nil {
			return
		}
		if node.Name != "" && nodeMatches(node, selector) {
			matches = append(matches, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return matches
}

func nodeMatches(node *uiXMLNode, selector uiXPathSelector) bool {
	if selector.Tag != "*" && node.Name != selector.Tag {
		return false
	}
	if selector.AttrKey == "" {
		return true
	}
	return attrValue(node, selector.AttrKey) == selector.AttrVal
}

func payloadNodes(op *uiXMLNode) []*uiXMLNode {
	nodes := make([]*uiXMLNode, 0)
	for _, child := range op.Children {
		if child.Name == "" {
			continue
		}
		if operationPosition(op) == "attributes" && child.Name == "attribute" {
			continue
		}
		nodes = append(nodes, child)
	}
	return nodes
}

// cloneNodes clones the nodes handled by this package.
func cloneNodes(nodes []*uiXMLNode) []*uiXMLNode {
	cloned := make([]*uiXMLNode, 0, len(nodes))
	for _, node := range nodes {
		cloned = append(cloned, cloneNode(node))
	}
	return cloned
}

// cloneNode clones the node handled by this package.
func cloneNode(node *uiXMLNode) *uiXMLNode {
	if node == nil {
		return nil
	}
	cloned := &uiXMLNode{
		Name: node.Name,
		Attr: append([]xml.Attr(nil), node.Attr...),
		Text: node.Text,
	}
	for _, child := range node.Children {
		next := cloneNode(child)
		next.Parent = cloned
		cloned.Children = append(cloned.Children, next)
	}
	return cloned
}

func spliceChildren(children []*uiXMLNode, target *uiXMLNode, replacements []*uiXMLNode, position string) []*uiXMLNode {
	index := -1
	for i, child := range children {
		if child == target {
			index = i
			break
		}
	}
	if index < 0 {
		return children
	}
	switch position {
	case "replace":
		return append(append(append([]*uiXMLNode{}, children[:index]...), replacements...), children[index+1:]...)
	case "before":
		return append(append(append([]*uiXMLNode{}, children[:index]...), replacements...), children[index:]...)
	case "after":
		return append(append(append([]*uiXMLNode{}, children[:index+1]...), replacements...), children[index+1:]...)
	default:
		return children
	}
}

func attrValue(node *uiXMLNode, name string) string {
	for _, attr := range node.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

// setNodeAttr stores the node attr handled by this package.
func setNodeAttr(node *uiXMLNode, name, value string) {
	for i, attr := range node.Attr {
		if attr.Name.Local != name {
			continue
		}
		if strings.TrimSpace(value) == "" {
			node.Attr = append(node.Attr[:i], node.Attr[i+1:]...)
			return
		}
		node.Attr[i].Value = value
		return
	}
	if strings.TrimSpace(value) != "" {
		node.Attr = append(node.Attr, xml.Attr{Name: xml.Name{Local: name}, Value: value})
	}
}

func innerText(node *uiXMLNode) string {
	if node == nil {
		return ""
	}
	if node.Name == "" {
		return node.Text
	}
	var builder strings.Builder
	for _, child := range node.Children {
		builder.WriteString(innerText(child))
	}
	return strings.TrimSpace(builder.String())
}

func operationPosition(node *uiXMLNode) string {
	return strings.ToLower(strings.TrimSpace(attrValue(node, "position")))
}

// inferArchType infers the arch type handled by this package.
func inferArchType(arch string) (string, bool) {
	arch = strings.TrimSpace(arch)
	if arch == "" {
		return "", false
	}
	decoder := xml.NewDecoder(strings.NewReader(arch))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		viewType := normalizeViewType(start.Name.Local)
		switch viewType {
		case "form", "list", "tree", "graph", "pivot", "calendar", "kanban", "search", "qweb", "dashboard":
			return viewType, true
		default:
			return viewType, false
		}
	}
}

func normalizeViewType(viewType string) string {
	viewType = strings.TrimSpace(strings.ToLower(viewType))
	if viewType == "tree" {
		return "list"
	}
	return viewType
}
