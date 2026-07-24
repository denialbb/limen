package retrieval_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestSplitPreserveAnalyzer_CamelCaseSplitsAndPreserves(t *testing.T) {
	a := retrieval.SplitPreserveAnalyzer{}
	got := sortedTerms(a.Analyze("getUserName"))
	want := []string{"get", "getusername", "name", "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitPreserveAnalyzer_SnakeCaseAndNonAlnum(t *testing.T) {
	a := retrieval.SplitPreserveAnalyzer{}
	got := sortedTerms(a.Analyze("foo_bar-baz qux"))
	want := []string{"bar", "baz", "foo", "foo_bar-baz", "qux"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func sortedTerms(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}