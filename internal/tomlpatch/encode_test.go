package tomlpatch

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestEncodeMap_PreservesLocalDateTimes(t *testing.T) {
	input := `
title = "test"
local_date = 2026-05-01
local_time = 07:32:00
local_dt = 2026-05-01T07:32:00.5
`
	var doc map[string]any
	if _, err := toml.Decode(input, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded, err := EncodeMap(doc)
	if err != nil {
		t.Fatalf("EncodeMap: %v", err)
	}
	text := string(encoded)
	for _, want := range []string{"2026-05-01", "07:32:00", "2026-05-01T07:32:00.5"} {
		if !strings.Contains(text, want) {
			t.Errorf("EncodeMap output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Z") {
		t.Errorf("EncodeMap shifted local datetime to UTC:\n%s", text)
	}
	var roundtrip map[string]any
	if _, err := toml.Decode(text, &roundtrip); err != nil {
		t.Fatalf("re-decode: %v\n%s", err, text)
	}
	if !Equal(doc, roundtrip) {
		t.Errorf("round-trip mismatch:\norig=%#v\ngot =%#v", doc, roundtrip)
	}
}
