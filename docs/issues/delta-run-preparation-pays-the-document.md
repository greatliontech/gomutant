# A delta run's preparation pays the document, not the delta

Field report (greatliontech/pb at 57c88e3, findings document of 308
records, gomutant v0.57.11-0.20260920055743-9881159d78c3, under a
25-minute timeout and a 12 GiB watchdog):

- `gomutant run --changed HEAD~1`, one target: "inspecting prior
  findings: closure signpost over 308 prior record(s)" 4 minutes, then
  `prepare freshness` for the one target 5 minutes, then measured; 10
  minutes in all, 9.5 GiB peak resident.
- `gomutant run --changed HEAD~3`, 73 targets and 863 candidates over
  three commits: the same signpost, then preparation per target; the
  first target committed at 17 minutes, seven at 25 minutes when the
  timeout fired, with the pace estimate at about two hours for the run;
  7.8 GiB peak resident.

So a delta campaign reaches measurement (the freshness-proof stall of
the first report is resolved: the passes are priced, visible, and
bounded by the analysis budget), but preparation still spends the
first quarter hour on the document rather than the delta: a one-target
run and a 73-target run pay the same closure signpost over every prior
record, and a one-target freshness proof alone takes five minutes.
Expected: a delta run's preparation scales with the delta — the records
outside the delta's targets are read, never judged, until a run over
them asks.

Lands: cross-tool train chunk 277 (the delta run's inspection scoped
to its targets; chartered at 257's tick).
