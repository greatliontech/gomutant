# Runtime-input provenance

Go testlog reports filesystem access but not whether the accessed object was a
pre-existing input or an output created by the measured process. Final path
inspection cannot recover that distinction after rename, symlink replacement,
hardlinking, or deletion. gomutant therefore records runtime-observation
disagreement as explicit non-reusable evidence and remeasures it rather than
claiming automatic freshness.

Automatic reusable evidence for these runs requires observation-time object
provenance across the measured process tree. A sound implementation must retain
external targets reached through symlinks and hardlinks, follow created objects
through rename and deletion, detect incomplete tracing, and refuse reuse on
unsupported hosts rather than excluding paths by naming convention.

Lands: when runtime observation can attribute filesystem object creation and
access across the measured process tree on every supported execution host, and
findings from producer-created temporary outputs remain reusable while changes
to genuine external inputs still stale them.
