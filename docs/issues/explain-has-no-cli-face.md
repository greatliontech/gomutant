# explain has no CLI face

Lands: awaiting triage

`explain` answers why — a symbol's full machine-local clause list
and its per-survivor prescriptions, or the whole document's
promotion triage — and it is served over MCP alone: the CLI's
command set (`attest`, `discover`, `ephemeral`, `findings`,
`guidance`, `prune`, `retarget`, `run`, `version`) has no
counterpart, and the guidance names it "MCP-only" beside the inline
edit forms. The edit forms are MCP-native; `explain` is a read
surface with no structured input the CLI could not take, the same
class as `findings`, which has both faces.

Field report (greatliontech/pb, gomutant
v0.57.11-0.20260919151557-ee9616fd4b7b): a `run` over two
`internal/archive` targets ended "2 record(s) machine-local only
(disqualifiers named above)", the reuse lines naming "external
directory input: /" and "subject reachability is not closed". The
questions those lines raise — which observation produced the `/`
input, which call opened the closure — are `explain`'s to answer,
and from a shell the only route was the machine-local overlay's
JSON under `~/.cache/gomutant/repos/`, read by hand. A CI log has
not even that.

The collapse: one `explain` read surface with a CLI face beside the
MCP one, as `findings` is served, so a `run`'s "machine-local only"
line is followed by the command that says why.
