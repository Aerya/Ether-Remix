package collections

import (
	"fmt"

	"github.com/braedonsaunders/ether/internal/store"
)

// Explicit title sources need a reverse lookup even before their collection
// has been built. The index is maintained for every writer of spec_json.
func init() {
	store.Register("collections", 13, `
CREATE TABLE collections_title_refs (
 spec_id BIGINT NOT NULL REFERENCES collections_specs(id) ON DELETE CASCADE,
 title_key TEXT NOT NULL,
 PRIMARY KEY(spec_id,title_key)
);
CREATE INDEX collections_title_refs_key ON collections_title_refs(title_key,spec_id);
`)
	const sqliteRefs = `INSERT INTO collections_title_refs(spec_id,title_key)
 SELECT %s.id,k.value FROM json_each(%s.spec_json,'$.sources') s, json_each(s.value,'$.keys') k
 WHERE json_extract(s.value,'$.kind')='titles' AND k.type='text'
 ON CONFLICT(spec_id,title_key) DO NOTHING;`
	store.RegisterDialect("collections", 14, store.SQLite, `CREATE TRIGGER collections_title_refs_insert AFTER INSERT ON collections_specs BEGIN `+fmt.Sprintf(sqliteRefs, "NEW", "NEW")+` END;
CREATE TRIGGER collections_title_refs_update AFTER UPDATE OF spec_json ON collections_specs BEGIN
 DELETE FROM collections_title_refs WHERE spec_id=OLD.id;
 `+fmt.Sprintf(sqliteRefs, "NEW", "NEW")+`
END;
INSERT INTO collections_title_refs(spec_id,title_key)
 SELECT c.id,k.value FROM collections_specs c, json_each(c.spec_json,'$.sources') s, json_each(s.value,'$.keys') k
 WHERE json_extract(s.value,'$.kind')='titles' AND k.type='text'
 ON CONFLICT(spec_id,title_key) DO NOTHING;
`)
	const pgRows = `jsonb_array_elements(COALESCE(NULLIF(%s.spec_json::jsonb->'sources','null'::jsonb),'[]'::jsonb)) s,
 jsonb_array_elements_text(COALESCE(NULLIF(s->'keys','null'::jsonb),'[]'::jsonb)) k
 WHERE s->>'kind'='titles' ON CONFLICT(spec_id,title_key) DO NOTHING;`
	store.RegisterDialect("collections", 14, store.Postgres, `CREATE FUNCTION collections_title_refs_sync() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM collections_title_refs WHERE spec_id=NEW.id;
 INSERT INTO collections_title_refs(spec_id,title_key) SELECT NEW.id,k FROM `+fmt.Sprintf(pgRows, "NEW")+`
 RETURN NEW;
END $$;
CREATE TRIGGER collections_title_refs_sync AFTER INSERT OR UPDATE OF spec_json ON collections_specs
 FOR EACH ROW EXECUTE FUNCTION collections_title_refs_sync();
INSERT INTO collections_title_refs(spec_id,title_key) SELECT c.id,k FROM collections_specs c, `+fmt.Sprintf(pgRows, "c"))
}
