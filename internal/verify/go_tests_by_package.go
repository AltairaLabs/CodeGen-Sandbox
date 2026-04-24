package verify

import (
	"sort"
	"strings"
)

// ExtractGoTestsByPackage scans a `go test -json` stdout stream and
// returns a map keyed by Go import path whose value is the sorted,
// deduplicated list of named tests that ran in that package (pass OR
// fail — both indicate the test body executed).
//
// Subtests are collapsed to their parent test name so the map lines up
// with the units an agent would rerun via `-run ^TestX$`.
//
// This powers the coverage index in tools.CoverageIndex.Ingest: each
// test listed under a package is attributed coverage of every file
// that package's profile records touched during the run.
//
// Never returns an error; malformed lines are skipped silently.
func ExtractGoTestsByPackage(stdout string) map[string][]string {
	sets := map[string]map[string]struct{}{}
	for _, line := range strings.Split(stdout, "\n") {
		ev, ok := decodeEvent(line)
		if !ok || ev.Test == "" || ev.Package == "" {
			continue
		}
		if ev.Action != "pass" && ev.Action != "fail" {
			continue
		}
		parent := strings.SplitN(ev.Test, "/", 2)[0]
		if parent == "" {
			continue
		}
		set, exists := sets[ev.Package]
		if !exists {
			set = map[string]struct{}{}
			sets[ev.Package] = set
		}
		set[parent] = struct{}{}
	}
	out := make(map[string][]string, len(sets))
	for pkg, set := range sets {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		out[pkg] = names
	}
	return out
}
