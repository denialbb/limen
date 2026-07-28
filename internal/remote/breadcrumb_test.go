package remote

import (
	"testing"

	"github.com/denialbb/limen/internal/bus"
)

func TestDiffBreadcrumbSets(t *testing.T) {
	tests := []struct {
		name string
		prev map[string]string
		curr map[string]string
		want []bus.BreadcrumbFile
	}{
		{
			name: "empty to empty emits nothing",
			prev: map[string]string{},
			curr: map[string]string{},
			want: nil,
		},
		{
			name: "nil to nil emits nothing",
			prev: nil,
			curr: nil,
			want: nil,
		},
		{
			name: "one new file",
			prev: map[string]string{},
			curr: map[string]string{"a.go": " M"},
			want: []bus.BreadcrumbFile{{Path: "a.go", Status: " M"}},
		},
		{
			name: "status code change is emitted",
			prev: map[string]string{"a.go": "??"},
			curr: map[string]string{"a.go": " M"},
			want: []bus.BreadcrumbFile{{Path: "a.go", Status: " M"}},
		},
		{
			name: "equal sets emit nothing (delta-only)",
			prev: map[string]string{"a.go": " M", "b.go": "??"},
			curr: map[string]string{"a.go": " M", "b.go": "??"},
			want: nil,
		},
		{
			name: "unchanged entries suppressed, changed one emitted",
			prev: map[string]string{"a.go": " M", "b.go": "??"},
			curr: map[string]string{"a.go": " M", "b.go": "A "},
			want: []bus.BreadcrumbFile{{Path: "b.go", Status: "A "}},
		},
		{
			name: "disappeared entries are not emitted (signal, not ledger)",
			prev: map[string]string{"a.go": " M", "gone.go": "??"},
			curr: map[string]string{"a.go": " M"},
			want: nil,
		},
		{
			name: "multiple deltas sorted by path",
			prev: map[string]string{},
			curr: map[string]string{"z.go": "??", "a.go": " M", "m.go": "A "},
			want: []bus.BreadcrumbFile{
				{Path: "a.go", Status: " M"},
				{Path: "m.go", Status: "A "},
				{Path: "z.go", Status: "??"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := diffBreadcrumbSets(tc.prev, tc.curr)
			if len(got) != len(tc.want) {
				t.Fatalf("diffBreadcrumbSets() = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("diffBreadcrumbSets() = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}
