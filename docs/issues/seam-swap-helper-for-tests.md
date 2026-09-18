# Some sixty hand-written seam save/restore pairs, seven restoring to a literal

Every test that writes a seam field saves the prior value and restores
it in a cleanup — about forty-five pairs across twelve root test files
and some fifteen more across the two faces' test files after the seams
became one struct per package; seven restore to a literal (the default)
rather than the prior value, correct only because the default is what
stood. One helper — swap(t, &seams.field, value) with a t.Cleanup
restoring the prior — makes a leaked seam unrepresentable and deletes
the boilerplate the struct made uniform.

Lands: cross-tool train chunk 219 (the test surface).
