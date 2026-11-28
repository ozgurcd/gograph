package search

// PreviouslyPassedTest is a selected test that already passed on identical
// inputs, so the run commands leave it out.
type PreviouslyPassedTest struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	File    string `json:"file"`
}

// SkipPassed removes from the selection every runnable test for which passed
// reports a pass on identical inputs, rebuilding each package's -run patterns
// and commands, and lists the removed tests in PreviouslyPassed. Benchmarks
// are never skipped. A package whose tests all passed is dropped.
func (s *TestSelection) SkipPassed(passed func(importPath, dir, name string) bool) {
	kept := s.Packages[:0]
	for _, pkg := range s.Packages {
		var tests []SelectedTest
		var runNames, benchNames []string
		for _, test := range pkg.Tests {
			switch {
			case isGoTestEntryName(test.Name, "Benchmark"):
				benchNames = append(benchNames, test.Name)
			case passed(pkg.ImportPath, pkg.Dir, test.Name):
				s.PreviouslyPassed = append(s.PreviouslyPassed, PreviouslyPassedTest{Package: pkg.ImportPath, Name: test.Name, File: test.File})
				s.TestCount--
				if test.Resolution == "exact" {
					s.ExactCount--
				} else {
					s.PossibleCount--
				}
				continue
			default:
				runNames = append(runNames, test.Name)
			}
			tests = append(tests, test)
		}
		if len(tests) == len(pkg.Tests) {
			kept = append(kept, pkg)
			continue
		}
		if len(tests) == 0 {
			continue
		}
		pkg.Tests = tests
		pkg.RunAll = false
		pkg.RunPatterns = []string{}
		if len(runNames) > 0 {
			pkg.RunPatterns = anchoredTestPatterns(runNames, maxRunPatternBytes)
		}
		pkg.BenchPattern = anchoredTestPattern(benchNames)
		pkg.Commands = goTestCommands(pkg.ImportPath, s.BuildTags, false, pkg.RunPatterns, pkg.BenchPattern)
		kept = append(kept, pkg)
	}
	s.Packages = kept
}
