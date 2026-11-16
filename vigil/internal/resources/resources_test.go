package resources

import (
	"slices"
	"testing"
	"vigil/internal/config"
)

func TestRunawayAndUnknownOwnership(t *testing.T) {
	c, _ := config.Default()
	snapshot := analyze("11 1 0 120 /usr/bin/codex\n12 11 190 3000000 /usr/bin/go\n13 1 220 4000000 /usr/bin/other\n", 99, 1, c.Resources)
	if len(snapshot.Processes) != 2 || snapshot.Processes[0].Ownership != "uncertain" || snapshot.Processes[1].Ownership != "codex_descendant" {
		t.Fatalf("processes: %+v", snapshot)
	}
	for _, w := range []string{"agent_process_high_cpu", "agent_process_high_memory", "long_running_commands_observed"} {
		if !slices.Contains(snapshot.Warnings, w) {
			t.Fatalf("missing %s: %+v", w, snapshot)
		}
	}
}
