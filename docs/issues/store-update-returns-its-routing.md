# The write's routing is re-derived after the write

Lands: cross-tool train chunk 283 (Store.Update returns the layer it
routed each record to)

Store.Update decides each committed record's layer under the document
lock — `committable[symbol]` — and reports nothing of it. The run
ledger classifies the record again after the write through the
store's predicate (`Store.Layer`, the same walk over the same
exemptions), records the answer, and serves it to the engine's
tallies and the faces' progress lines; `RunLedger.Finish` walks the
whole merged document through the predicate once more for the
promoted and machine-local counts. The predicate is pure over the
finding, the module directory and the exemptions loaded once at the
store's open, so the answers agree; the cost is the second walk per
commit, a full portable-line walk materializing every out-of-module
path of the record's manifests, and two sources for one fact where
the write's own decision could be the one. The collapse: Update
returns its routing per symbol (a report beside the error), the
ledger hands it to the tallies through Options.Commit's result, and
Finish reads the final merge's routing for the records it wrote,
judging only the standing rows it did not. The change reaches
Update's seams in both faces and their tests.
