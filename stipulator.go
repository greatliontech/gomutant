package gomutant

import (
	"fmt"
	"sort"

	"github.com/greatliontech/stipulator/bindingsurface"
)

const surfaceLabel = "stipulator:surface:"

// ParseStipulatorTargets validates Stipulator's binding-surface report and
// reduces compatible surfaces to gomutant's target model.
func ParseStipulatorTargets(data []byte) ([]Target, error) {
	report, err := bindingsurface.ParseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("gomutant: parse stipulator binding surfaces: %w", err)
	}

	targets := make([]Target, 0, len(report.GetSurfaces()))
	for _, surface := range report.GetSurfaces() {
		if surface.GetBackend() != "go" {
			return nil, fmt.Errorf(
				"gomutant: stipulator surface %s uses unsupported implementation backend %q for symbol %s; export backend go",
				surface.GetId(), surface.GetBackend(), surface.GetSymbol(),
			)
		}
		oracleSet := map[string]bool{}
		for _, binding := range surface.GetBindings() {
			if binding.GetBackend() != "go" {
				return nil, fmt.Errorf(
					"gomutant: stipulator surface %s uses unsupported binding backend %q for symbol %s; export a surface with an all-go oracle",
					surface.GetId(), binding.GetBackend(), binding.GetSymbol(),
				)
			}
			oracleSet[binding.GetSymbol()] = true
		}
		labelSet := map[string]bool{surfaceLabel + surface.GetId(): true}
		for _, requirement := range surface.GetRequirementIds() {
			labelSet[requirement] = true
		}
		targets = append(targets, Target{
			Symbol:         surface.GetSymbol(),
			Oracle:         sortedStringSet(oracleSet),
			Labels:         sortedStringSet(labelSet),
			OracleExplicit: true,
		})
	}
	return targets, nil
}

func sortedStringSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
