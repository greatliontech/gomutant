package cmd

import gomutant "github.com/greatliontech/gomutant"

// layerMarker is the layer as every CLI face spells it beside a record:
// the machine-local marker alone, absence meaning repo — the findings
// rows, the prune and retarget rows alike (REQ-result-layers).
func layerMarker(layer string) string {
	if layer == gomutant.LayerLocal {
		return "  [machine-local]"
	}
	return ""
}
