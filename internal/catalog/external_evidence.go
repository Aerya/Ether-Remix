package catalog

// RefreshExternalEvidence invalidates browse/count caches after an atomic import.
func (c *Catalog) RefreshExternalEvidence() { c.writeGen.Add(1) }
