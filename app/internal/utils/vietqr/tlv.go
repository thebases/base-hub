package vietqr

// File overview:
// - Responsibility: collects shared helper logic for tlv, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `NewTLV`, `string`, `TLV`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import "fmt"

// TLV struct
type TLV struct {
	TagID     int
	TagName   string
	TagLength int
	TagValue  interface{}
	Presence  string
}

// TLV Constructor
func NewTLV(id int, name string, length int, isFixed bool, presence string, value interface{}) TLV {
	if !isFixed {
		length = len(fmt.Sprintf("%v", value))
	}
	return TLV{
		TagID:     id,
		TagName:   name,
		TagLength: length,
		TagValue:  value,
		Presence:  presence,
	}
}

// TLV toString function
func (t TLV) ToString() string {
	value := ""
	switch v := t.TagValue.(type) {
	case []TLV:
		for _, item := range v {
			value += item.ToString()
		}
	case string:
		value = v
	}

	if value == "" {
		return ""
	}

	t.TagLength = len(value)
	return fmt.Sprintf("%02d%02d%s", t.TagID, t.TagLength, value)
}
