# A body-source read failure during the surface scan mislabels the declaration as deleted

Lands: when a working-side declaration whose body bytes cannot be read
during the surface scan is reported as unreadable (or fails the scan)
instead of silently classifying as "only deleted symbols".

## Observed

In the changed-surface scan, a declaration whose `sourceOfContext` read
fails is skipped from the working-side index; if the reference version
declares it, the orphaned reference key counts as a deletion and the
file can report "only deleted symbols: nothing remains to mutate" — a
mislabel: the declaration exists, its bytes were unreadable. The
realistic trigger is a tree mutating mid-scan; runs detect drift at the
producer boundary, but discovery has no such net.
