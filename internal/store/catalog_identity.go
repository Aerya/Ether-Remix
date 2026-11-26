package store

func init() {
	// The catalogue and viewer state share this ancestry index. Allocated
	// keys have no decodable parent, even after metadata is withdrawn.
	Register("catalog_identity_ancestry", 1, `
CREATE TABLE catalog_allocated_child (
 key INTEGER PRIMARY KEY,
 show_key INTEGER NOT NULL,
 parent_key INTEGER NOT NULL,
 kind INTEGER NOT NULL,
 season INTEGER NOT NULL,
 episode INTEGER NOT NULL
);
CREATE INDEX catalog_allocated_child_show ON catalog_allocated_child(show_key,kind,season,episode,key);
CREATE INDEX catalog_allocated_child_parent ON catalog_allocated_child(parent_key,kind,key);`)
}
