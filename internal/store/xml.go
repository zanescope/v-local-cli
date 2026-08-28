package store

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"strings"
)

const maxXMLBytes = 4 * 1024 * 1024

// maxXMLDepth 限制嵌套层数。encoding/xml 自带 maxUnmarshalDepth 守卫，但
// DecodeElement 每次都以深度 0 重新进入 unmarshal，自定义 UnmarshalXML 里的递归
// 因此完全绕过它；不自己计数的话，4 MB 以内即可构造出耗尽 goroutine 栈的深度，
// 触发 recover 无法捕获的致命错误。真实微信 XML 只有十几层。
const maxXMLDepth = 256

var errXMLTooDeep = errors.New("XML 嵌套层数超过上限")

type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     strings.Builder
	Children []*xmlNode
	depth    int
}

func (node *xmlNode) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	if node.depth >= maxXMLDepth {
		return errXMLTooDeep
	}
	node.Name = start.Name
	node.Attrs = append(node.Attrs, start.Attr...)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			child := &xmlNode{depth: node.depth + 1}
			if err := decoder.DecodeElement(child, &value); err != nil {
				return err
			}
			node.Children = append(node.Children, child)
		case xml.CharData:
			node.Text.Write([]byte(value))
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

func parseXML(value string, unescape bool) (*xmlNode, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, "empty"
	}
	if unescape && strings.Contains(value, "&lt;") {
		value = html.UnescapeString(value)
	}
	data := []byte(value)
	if len(data) > maxXMLBytes {
		return nil, "too_large"
	}
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return nil, "unsafe_doctype"
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var root xmlNode
	if err := decoder.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, "empty"
		}
		if errors.Is(err, errXMLTooDeep) {
			return nil, "too_deep"
		}
		return nil, "invalid_xml"
	}
	return &root, ""
}

func localXMLName(name xml.Name) string {
	return strings.ToLower(name.Local)
}

func (node *xmlNode) direct(names ...string) *xmlNode {
	if node == nil {
		return nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	for _, child := range node.Children {
		if wanted[localXMLName(child.Name)] {
			return child
		}
	}
	return nil
}

func (node *xmlNode) descendants(names ...string) []*xmlNode {
	if node == nil {
		return nil
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	var result []*xmlNode
	var walk func(*xmlNode)
	walk = func(current *xmlNode) {
		for _, child := range current.Children {
			if wanted[localXMLName(child.Name)] {
				result = append(result, child)
			}
			walk(child)
		}
	}
	walk(node)
	return result
}

func (node *xmlNode) descendant(names ...string) *xmlNode {
	values := node.descendants(names...)
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func (node *xmlNode) text() string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	value := strings.TrimSpace(node.Text.String())
	if value != "" {
		builder.WriteString(value)
	}
	for _, child := range node.Children {
		childText := child.text()
		if childText == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(childText)
	}
	return strings.TrimSpace(builder.String())
}

func (node *xmlNode) directText(names ...string) string {
	return node.direct(names...).text()
}

func (node *xmlNode) descendantText(names ...string) string {
	return node.descendant(names...).text()
}

func (node *xmlNode) attribute(name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attrs {
		if strings.EqualFold(attribute.Name.Local, name) {
			return strings.TrimSpace(attribute.Value)
		}
	}
	return ""
}

func (node *xmlNode) path(parts ...string) *xmlNode {
	current := node
	for _, part := range parts {
		current = current.direct(part)
		if current == nil {
			return nil
		}
	}
	return current
}

func firstXMLText(node *xmlNode, paths ...[]string) string {
	for _, path := range paths {
		if value := node.path(path...).text(); value != "" {
			return value
		}
	}
	return ""
}
