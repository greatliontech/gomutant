# Staged campaign reports a clean index snapshot as unstaged drift

## Symptom

With gomutant `v0.38.1-0.20260819140653-cbd486451f51`, a staged
changed-scope campaign refuses an executed target:

    gomutant: tree changed under measurement: <symbol>: unstaged drift over
    the measured package's inputs; stage or stash it to pin the snapshot

`git status --short` before and after the refusal contains only index-column
changes. It reports no unstaged or untracked path, and the refusal does not
name a drifted input.

## Reproducer

Observed in `graphicsgo/vulkan-go` on 2026-08-20. Reconstruct the measured
index in a disposable worktree from commits `e61a4ac` and `6bc0c40`, and start
with no prior findings document:

    git worktree add --detach /tmp/vulkan-gomutant-repro e61a4ac
    git diff e61a4ac 6bc0c40 | git -C /tmp/vulkan-gomutant-repro apply --index
    rm -f /tmp/vulkan-gomutant-findings.json

Run from `/tmp/vulkan-gomutant-repro`:

    gomutant run --staged --changed HEAD \
      --symbol github.com/thegrumpylion/vulkan-go/internal/generator.Generator.generateDocMapping \
      --budget 1 --jobs 1 --oracle-timeout 3m --timeout 10m \
      --scratch-namespace internal/generator:.vkgen-test-* \
      --bracket-path vkgen/vk.xml \
      --bracket-path vkgen/policy/wrapper.json \
      --bracket-path vkgen/policy/pointer.json \
      --bracket-path vkgen/include/vulkan/vulkan_beta.h \
      --bracket-path vkgen/include/vulkan/vulkan_core.h \
      --bracket-path vkgen/include/vulkan/vk_platform.h \
      --findings /tmp/vulkan-gomutant-findings.json

The baseline and one mutant execute, then the target is discarded as drifted.
The breadth campaign reports the same refusal for this target and 44 more.

Using the default findings path exposes an additional self-interference arm:
the campaign creates `.gomutant/findings.json`, then reports that untracked
appearance as the target-input drift. Moving findings outside the module
removes that visible write but does not remove the unnamed refusal above.

## Required evidence

The refusal needs to report every path or input identity that differs from the
staged snapshot. A staged campaign over this reproducer should either complete
without drift or identify a concrete post-run difference that `git status` or
the runtime-input bracket can verify.

Lands: user decision.
