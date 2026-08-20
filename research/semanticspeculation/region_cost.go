package semanticspeculation

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

// BuildPhase4RegionCostProgram constructs an authority-free target-Guest program from
// the exact frozen focus span and its analyzer-proven dependency closure. It does
// not rewrite the candidate expression or execute any non-dependency source region.
func BuildPhase4RegionCostProgram(candidate Phase4RegionCase, analysis semantic.Analysis) (string, error) {
	if int(candidate.FocusRegionIndex) >= len(analysis.CandidateRegions) {
		return "", errors.New("phase 4 focus region is absent")
	}
	focus := analysis.CandidateRegions[candidate.FocusRegionIndex]
	if !candidate.ExpectedLocalReusable || !focus.LocallyReusable() {
		return "", errors.New("phase 4 cost program requires a locally reusable positive")
	}
	byID := make(map[string]int, len(analysis.CandidateRegions))
	for index, region := range analysis.CandidateRegions {
		byID[region.ID] = index
	}
	selected := map[int]bool{}
	var include func(int) error
	include = func(index int) error {
		if selected[index] {
			return nil
		}
		region := analysis.CandidateRegions[index]
		if !region.LocallyReusable() {
			return fmt.Errorf("dependency region %s is not locally reusable", region.ID)
		}
		for _, dependency := range region.DataDependencies {
			producer, ok := byID[dependency.ProducerRegionID]
			if !ok || producer >= index {
				return errors.New("phase 4 dependency closure is not prior and complete")
			}
			if err := include(producer); err != nil {
				return err
			}
		}
		selected[index] = true
		return nil
	}
	if err := include(int(candidate.FocusRegionIndex)); err != nil {
		return "", err
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	lines := strings.Split(candidate.Source, "\n")
	statements := make([]string, 0, len(indexes))
	focusStatement := ""
	for _, index := range indexes {
		statement, err := exactRegionSpan(lines, analysis.CandidateRegions[index].Span)
		if err != nil {
			return "", err
		}
		if index == int(candidate.FocusRegionIndex) {
			focusStatement = statement
		} else {
			statements = append(statements, statement)
		}
	}
	if focusStatement == "" {
		return "", errors.New("phase 4 focus statement is empty")
	}
	statements = append([]string{"import time as _p4_time"}, statements...)
	statements = append(statements,
		"_p4_start_ns = _p4_time.perf_counter_ns()",
		focusStatement,
		"_p4_end_ns = _p4_time.perf_counter_ns()",
		"result = {'constructed_region_execution_nanos': _p4_end_ns - _p4_start_ns}",
	)
	return strings.Join(statements, "\n") + "\n", nil
}

func exactRegionSpan(lines []string, span semantic.SourceSpan) (string, error) {
	if span.StartLine == 0 || span.StartLine != span.EndLine || int(span.StartLine) > len(lines) {
		return "", errors.New("phase 4 cost region must be a single source line")
	}
	line := lines[span.StartLine-1]
	if span.EndColumn < span.StartColumn || int(span.EndColumn) > len(line) {
		return "", errors.New("phase 4 cost region span is out of range")
	}
	statement := line[span.StartColumn:span.EndColumn]
	if strings.TrimSpace(statement) == "" {
		return "", errors.New("phase 4 cost region span is empty")
	}
	return statement, nil
}
