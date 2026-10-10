package gomutant

import (
	"slices"

	"github.com/greatliontech/gofresh"
)

func compartmentHeadersFromView(headers []gofresh.TestVariantFileHeader) []CompartmentFileHeader {
	if headers == nil {
		return nil
	}
	out := make([]CompartmentFileHeader, len(headers))
	for i, h := range headers {
		out[i] = CompartmentFileHeader{File: h.File, Hash: h.Hash, Embedded: h.Embedded}
		if b := h.Bindings; b != nil {
			out[i].Bindings = &CompartmentBindings{Package: b.Package, References: slices.Clone(b.References)}
			if b.Imports != nil {
				out[i].Bindings.Imports = make([]CompartmentImport, len(b.Imports))
			}
			for j, imp := range b.Imports {
				out[i].Bindings.Imports[j] = CompartmentImport{Name: imp.Name, Path: imp.Path}
			}
		}
	}
	return out
}

func compartmentHeadersToView(headers []CompartmentFileHeader) []gofresh.TestVariantFileHeader {
	if headers == nil {
		return nil
	}
	out := make([]gofresh.TestVariantFileHeader, len(headers))
	for i, h := range headers {
		out[i] = gofresh.TestVariantFileHeader{File: h.File, Hash: h.Hash, Embedded: h.Embedded}
		if b := h.Bindings; b != nil {
			out[i].Bindings = &gofresh.TestVariantFileBindings{Package: b.Package, References: slices.Clone(b.References)}
			if b.Imports != nil {
				out[i].Bindings.Imports = make([]gofresh.TestVariantImport, len(b.Imports))
			}
			for j, imp := range b.Imports {
				out[i].Bindings.Imports[j] = gofresh.TestVariantImport{Name: imp.Name, Path: imp.Path}
			}
		}
	}
	return out
}

func cloneCompartmentHeaders(headers []CompartmentFileHeader) []CompartmentFileHeader {
	out := slices.Clone(headers)
	for i := range out {
		if b := out[i].Bindings; b != nil {
			copy := *b
			copy.References = slices.Clone(b.References)
			copy.Imports = slices.Clone(b.Imports)
			out[i].Bindings = &copy
		}
	}
	return out
}

// A partial measurement cannot manufacture a historical binding proof. An
// already complete, preserved binding surface can follow the licensed splice.
func splicedCompartmentLedger(prior *CompartmentLedger, current gofresh.TestVariantLedger) *CompartmentLedger {
	if prior == nil || !gofresh.DiffTestVariantLedgers(prior.ledger(), current).BindingsPreserved {
		return prior
	}
	return compartmentLedgerFromView(current)
}
