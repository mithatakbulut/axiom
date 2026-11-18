package candidate

import (
	"strings"
	"testing"
	"time"
)

func TestIdentityKindsAndRequiredProvenance(t *testing.T) {
	now := time.Now().UTC()
	repo := "file:///workspace/project"
	base := strings.Repeat("a", 40)
	cases := []struct {
		name string
		item Identity
		ok   bool
	}{
		{"commit", Identity{SchemaVersion: 1, Kind: GitCommit, ID: base, Repository: repo, CapturedAt: now, Provenance: "git:HEAD"}, true},
		{"index", Identity{SchemaVersion: 1, Kind: GitIndex, ID: strings.Repeat("b", 40), Repository: repo, BaseRevision: base, CapturedAt: now, Provenance: "git:write-tree"}, true},
		{"snapshot", Identity{SchemaVersion: 1, Kind: ContentSource, ID: strings.Repeat("c", 64), CapturedAt: now, Provenance: "selected-source-snapshot"}, true},
		{"index requires base", Identity{SchemaVersion: 1, Kind: GitIndex, ID: strings.Repeat("b", 40), Repository: repo, CapturedAt: now, Provenance: "git:write-tree"}, false},
		{"content id is sha256", Identity{SchemaVersion: 1, Kind: ContentSource, ID: base, CapturedAt: now, Provenance: "snapshot"}, false},
		{"unknown kind", Identity{SchemaVersion: 1, Kind: "other", ID: base, CapturedAt: now, Provenance: "test"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.item.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() error=%v, want ok=%t", err, tc.ok)
			}
		})
	}
}
