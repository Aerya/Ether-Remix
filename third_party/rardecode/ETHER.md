BSD-2-Clause upstream github.com/nwaples/rardecode/v2 v2.4.5
(commit ad22ba91a2ab0d3ff6bca20cd978b33ee78faab1). LICENSE is preserved.

Ether carries a local copy because MaxDictionarySize upstream does not bound
RAR 3 PPMd allocations. Local changes pass that ceiling into PPMd and filter
buffers, cap retained RAR 3 VM filter definitions by the grant (at most 64),
and stop implicit solid skipping beyond declared output plus a bounded window. Plugin calls reserve
one eighth of their memory grant for each decoder buffer, leaving room for
metadata, retained/reallocated windows, filters and returned ranges.
