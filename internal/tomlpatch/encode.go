package tomlpatch

import (
	"bytes"
	"time"

	gotoml "github.com/pelletier/go-toml/v2"
)

// EncodeMap encodes a decoded TOML document (as produced by
// BurntSushi/toml into map[string]any) back to TOML bytes without
// BurntSushi's local-datetime UTC-shift bug.
//
// BurntSushi/toml decodes offset-less TOML dates/times into time.Time
// values carrying synthetic locations ("date-local", "time-local",
// "datetime-local"), but its Encoder ignores those locations and
// converts them to UTC timestamps. Converting them to go-toml's native
// LocalDate / LocalTime / LocalDateTime types preserves wall-clock
// values byte-for-byte on round-trip.
func EncodeMap(m map[string]any) ([]byte, error) {
	normalized := normalizeLocalTimes(m)
	var buf bytes.Buffer
	enc := gotoml.NewEncoder(&buf)
	enc.SetIndentTables(true)
	if err := enc.Encode(normalized); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeLocalTimes(v any) any {
	switch x := v.(type) {
	case time.Time:
		switch x.Location().String() {
		case "date-local":
			return gotoml.LocalDate{
				Year:  x.Year(),
				Month: int(x.Month()),
				Day:   x.Day(),
			}
		case "time-local":
			return gotoml.LocalTime{
				Hour:       x.Hour(),
				Minute:     x.Minute(),
				Second:     x.Second(),
				Nanosecond: x.Nanosecond(),
				Precision:  timePrecision(x.Nanosecond()),
			}
		case "datetime-local":
			return gotoml.LocalDateTime{
				LocalDate: gotoml.LocalDate{
					Year:  x.Year(),
					Month: int(x.Month()),
					Day:   x.Day(),
				},
				LocalTime: gotoml.LocalTime{
					Hour:       x.Hour(),
					Minute:     x.Minute(),
					Second:     x.Second(),
					Nanosecond: x.Nanosecond(),
					Precision:  timePrecision(x.Nanosecond()),
				},
			}
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, elem := range x {
			out[k] = normalizeLocalTimes(elem)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, elem := range x {
			out[i] = normalizeLocalTimes(elem).(map[string]any)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			out[i] = normalizeLocalTimes(elem)
		}
		return out
	default:
		return v
	}
}

func timePrecision(ns int) int {
	if ns == 0 {
		return 0
	}
	// Strip trailing decimal zeros so .500000000 encodes as .5, matching
	// BurntSushi/toml's fractional rendering.
	p := 9
	for p > 0 && ns%10 == 0 {
		ns /= 10
		p--
	}
	return p
}
