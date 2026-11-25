package agentusage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseWhen_MicroAndNanoEpochs(t *testing.T) {
	want := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	for name, raw := range map[string]string{
		"seconds":      "1779192000",
		"milliseconds": "1779192000000",
		"microseconds": "1779192000000000",
		"nanoseconds":  "1779192000000000000",
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseWhen(json.RawMessage(raw))
			if !ok {
				t.Fatalf("parseWhen(%s) returned !ok", raw)
			}
			if !got.UTC().Equal(want) {
				t.Errorf("parseWhen(%s) = %v, want %v", raw, got.UTC(), want)
			}
		})
	}
}
