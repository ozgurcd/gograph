package pathrank

import "testing"

func TestRankOrdering(t *testing.T) {
	base := Rank{}
	tests := []struct {
		name   string
		better Rank
		worse  Rank
	}{
		{name: "exact before possible", better: base, worse: Rank{Certainty: Possible}},
		{name: "ambiguous before possible", better: Rank{Certainty: Ambiguous}, worse: Rank{Certainty: Possible}},
		{name: "shorter", better: Rank{Length: 1}, worse: Rank{Length: 2}},
		{name: "production before tests", better: base, worse: Rank{IncludesTests: true}},
		{name: "typed before heuristics", better: base, worse: Rank{IncludesHeuristics: true}},
		{name: "local before cross repository", better: base, worse: Rank{CrossRepositoryEdgeCount: 1}},
		{name: "canonical tie break", better: Rank{Tie: "a"}, worse: Rank{Tie: "b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !test.better.Less(test.worse) || test.worse.Less(test.better) {
				t.Fatalf("rank order is not strict: better=%+v worse=%+v", test.better, test.worse)
			}
		})
	}
}

func TestRankClassKeyKeepsErasableCategoriesSeparate(t *testing.T) {
	ranks := []Rank{
		{},
		{Certainty: Ambiguous},
		{Certainty: Possible},
		{IncludesTests: true},
		{IncludesHeuristics: true},
	}
	seen := make(map[string]bool)
	for _, rank := range ranks {
		if seen[rank.ClassKey()] {
			t.Fatalf("duplicate class key for %+v", rank)
		}
		seen[rank.ClassKey()] = true
	}
}
