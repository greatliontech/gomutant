# TestRunPlanOnlyStopsBeforeExecution hangs in gatherWindow under the full suite

One full-suite run (2026-08-20, machine otherwise idle) panicked the
root package's 10m test timeout with the main goroutine parked in
`gatherWindow`'s channel receive under
`TestRunPlanOnlyStopsBeforeExecution` (`run.go:2835`, `Tree.Run`'s
work-window gather). The same test passes 3/3 solo at ~6.6s, so the
hang needs suite-order or load conditions — a producer that exited
without closing the work channel, or a window budget the loaded
scheduler starves.

Not reproduced solo; no mechanism cited yet — the next occurrence
should capture the full goroutine dump (the test binary's timeout
panic already prints it; keep the log).

Lands: when the hang reproduces with a dump that shows the
unclosed-producer or starved-budget mechanism, or with the next
change to Tree.Run's work-window plumbing.
