package sleeplock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// readBattery returns current battery status by shelling out to the
// platform-appropriate tool. Pure logic for parsing is split into
// parsePmsetBatt and parseLinuxBattery so the tests can pin the
// behavior without spawning subprocesses.
func readBattery(ctx context.Context) (BatteryStatus, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output()
		if err != nil {
			return BatteryStatus{}, err
		}
		return parsePmsetBatt(string(out)), nil
	case "linux":
		return readLinuxBattery()
	}
	return BatteryStatus{}, nil
}

// parsePmsetBatt extracts {Percent, OnAC, HasBattery} from `pmset -g batt`.
// Typical output (macOS 15):
//
//	Now drawing from 'Battery Power'
//	 -InternalBattery-0 (id=…)\t87%; discharging; 4:12 remaining present: true
//
// On a desktop / Mac mini there is no battery line:
//
//	Now drawing from 'AC Power'
//
// The "Now drawing from" line is the authoritative AC indicator.
func parsePmsetBatt(out string) BatteryStatus {
	bs := BatteryStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Now drawing from"):
			bs.OnAC = strings.Contains(line, "'AC Power'")
		case strings.Contains(line, "InternalBattery"):
			bs.HasBattery = true
			// e.g. "-InternalBattery-0 (id=…)\t87%; discharging; …"
			if idx := strings.Index(line, "%"); idx > 0 {
				// Walk back to find the start of the digit run.
				start := idx
				for start > 0 && (line[start-1] >= '0' && line[start-1] <= '9') {
					start--
				}
				if start < idx {
					if n, err := strconv.Atoi(line[start:idx]); err == nil {
						bs.Percent = n
					}
				}
			}
		}
	}
	return bs
}

// readLinuxBattery reads /sys/class/power_supply/BAT*/{capacity,status}
// to assemble a BatteryStatus. Returns HasBattery=false when there's no
// BAT* directory (desktop / server / container without an exposed battery).
func readLinuxBattery() (BatteryStatus, error) {
	matches, err := filepath.Glob("/sys/class/power_supply/BAT*")
	if err != nil {
		return BatteryStatus{}, err
	}
	if len(matches) == 0 {
		return BatteryStatus{}, nil
	}
	// Use the first battery we find.
	dir := matches[0]
	bs := BatteryStatus{HasBattery: true}
	if data, err := os.ReadFile(filepath.Join(dir, "capacity")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			bs.Percent = n
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		// "Charging" or "Full" -> on AC. "Discharging" -> not on AC.
		status := strings.TrimSpace(string(data))
		bs.OnAC = status == "Charging" || status == "Full" || status == "Not charging"
	}
	return bs, nil
}
