package experience

import (
	"github.com/braedonsaunders/ether/internal/handles/capability"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("experience_receipts", "capability_headers")
	store.Register("experience_receipts", 5, `INSERT INTO capability_headers(namespace,digest,shard,viewer_id,surface,device_id,expires)
 SELECT 'experience.receipt',hash,(viewer_id % 64),viewer_id,protocol,device_id,expires*1000 FROM experience_receipts WHERE true
 ON CONFLICT(namespace,digest) DO UPDATE SET shard=excluded.shard,viewer_id=excluded.viewer_id,surface=excluded.surface,device_id=excluded.device_id,expires=excluded.expires;
 INSERT INTO capability_claims(namespace,digest,purpose) SELECT 'experience.receipt',hash,action FROM experience_receipt_actions WHERE true ON CONFLICT(namespace,digest,purpose) DO NOTHING;
 DROP TABLE experience_receipt_actions;

 CREATE TRIGGER experience_receipt_admit BEFORE INSERT ON experience_receipts BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND viewer_id=NEW.viewer_id AND surface=NEW.protocol)>100000 OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64))>400000 THEN RAISE(ABORT,'receipt admission required') END; END;
 CREATE TRIGGER experience_receipt_header_immutable BEFORE UPDATE ON capability_headers WHEN OLD.namespace='experience.receipt' OR NEW.namespace='experience.receipt' BEGIN SELECT RAISE(ABORT,'receipt header is immutable'); END;
 CREATE TRIGGER experience_receipt_immutable BEFORE UPDATE ON experience_receipts BEGIN
 SELECT RAISE(ABORT,'receipt is immutable'); END;
 CREATE TRIGGER experience_receipt_retire AFTER DELETE ON experience_receipts BEGIN
 DELETE FROM capability_headers WHERE namespace='experience.receipt' AND digest=OLD.hash AND viewer_id=OLD.viewer_id AND surface=OLD.protocol AND device_id=OLD.device_id; END;
 CREATE TRIGGER experience_receipt_header_retire AFTER DELETE ON capability_headers WHEN OLD.namespace='experience.receipt' BEGIN
 DELETE FROM experience_receipts WHERE hash=OLD.digest AND viewer_id=OLD.viewer_id AND protocol=OLD.surface AND device_id=OLD.device_id; END;
`)
	store.RegisterDialect("experience_receipts", 5, store.Postgres, `INSERT INTO capability_headers(namespace,digest,shard,viewer_id,surface,device_id,expires)
 SELECT 'experience.receipt',hash,(viewer_id % 64),viewer_id,protocol,device_id,expires*1000 FROM experience_receipts WHERE true
 ON CONFLICT(namespace,digest) DO UPDATE SET shard=excluded.shard,viewer_id=excluded.viewer_id,surface=excluded.surface,device_id=excluded.device_id,expires=excluded.expires;
 INSERT INTO capability_claims(namespace,digest,purpose) SELECT 'experience.receipt',hash,action FROM experience_receipt_actions WHERE true ON CONFLICT(namespace,digest,purpose) DO NOTHING;
 DROP TABLE experience_receipt_actions;

 CREATE FUNCTION experience_receipt_admit_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(CAST(`+capability.AdmissionLockSQL(ReceiptCapabilityNamespace, "NEW.viewer_id")+` AS BIGINT));
 IF NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND viewer_id=NEW.viewer_id AND surface=NEW.protocol)>100000 OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64))>400000 THEN RAISE EXCEPTION 'receipt admission required'; END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER experience_receipt_admit BEFORE INSERT ON experience_receipts FOR EACH ROW EXECUTE FUNCTION experience_receipt_admit_fn();
 CREATE FUNCTION experience_receipt_immutable_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt is immutable'; END $$;
 CREATE TRIGGER experience_receipt_header_immutable BEFORE UPDATE ON capability_headers FOR EACH ROW WHEN (OLD.namespace='experience.receipt' OR NEW.namespace='experience.receipt') EXECUTE FUNCTION experience_receipt_immutable_fn();
 CREATE TRIGGER experience_receipt_immutable BEFORE UPDATE ON experience_receipts FOR EACH ROW EXECUTE FUNCTION experience_receipt_immutable_fn();
 CREATE FUNCTION experience_receipt_retire_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 DELETE FROM capability_headers WHERE namespace='experience.receipt' AND digest=OLD.hash AND viewer_id=OLD.viewer_id AND surface=OLD.protocol AND device_id=OLD.device_id; RETURN OLD; END $$;
 CREATE TRIGGER experience_receipt_retire AFTER DELETE ON experience_receipts FOR EACH ROW EXECUTE FUNCTION experience_receipt_retire_fn();
 CREATE FUNCTION experience_receipt_header_retire_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF OLD.namespace='experience.receipt' THEN DELETE FROM experience_receipts WHERE hash=OLD.digest AND viewer_id=OLD.viewer_id AND protocol=OLD.surface AND device_id=OLD.device_id; END IF; RETURN OLD; END $$;
 CREATE TRIGGER experience_receipt_header_retire AFTER DELETE ON capability_headers FOR EACH ROW EXECUTE FUNCTION experience_receipt_header_retire_fn();
`)
	// The admission backstop charges only live receipts, matching Go
	// admission: an expired receipt authorizes nothing and maintenance drains
	// it, so a heavy viewer's expired backlog cannot exhaust the quota. The
	// database clock is read at insert, after the issuer's cutoff, so the
	// trigger never charges a row the issuer did not. Own component:
	// experience_receipts and capability_headers are pinned to the delivered
	// schema boundary.
	store.RegisterAfter("experience_receipts_retention", "experience_receipts")
	store.Register("experience_receipts_retention", 1, `DROP TRIGGER experience_receipt_admit;
 CREATE TRIGGER experience_receipt_admit BEFORE INSERT ON experience_receipts BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND viewer_id=NEW.viewer_id AND surface=NEW.protocol AND expires>unixepoch()*1000)>100000 OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND expires>unixepoch()*1000)>400000 THEN RAISE(ABORT,'receipt admission required') END; END;
`)
	store.RegisterDialect("experience_receipts_retention", 1, store.Postgres, `CREATE OR REPLACE FUNCTION experience_receipt_admit_fn() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE live_after BIGINT; BEGIN
 PERFORM pg_advisory_xact_lock(CAST(`+capability.AdmissionLockSQL(ReceiptCapabilityNamespace, "NEW.viewer_id")+` AS BIGINT));
 live_after := CAST(FLOOR(EXTRACT(EPOCH FROM clock_timestamp())) AS BIGINT)*1000;
 IF NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND viewer_id=NEW.viewer_id AND surface=NEW.protocol AND expires>live_after)>100000 OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(NEW.viewer_id % 64) AND expires>live_after)>400000 THEN RAISE EXCEPTION 'receipt admission required'; END IF;
 RETURN NEW; END $$;
`)
	// The quota backstop ran per inserted row: two COUNT(*) scans over every
	// live receipt header on the viewer's shard, for each of a Home page's
	// ~240 receipts. On the lab (31k live headers) that held the SQLite
	// writer, and on PostgreSQL the shard admission lock, for over a second
	// per page, so playback opens queued behind it. The row trigger now only
	// proves the exact header exists (one primary-key probe). Go admission
	// (capability.AdmitPageHeadersInTx) counts both quotas once per batch,
	// under the same shard lock and in the same transaction. SQLite has one
	// writing process, so that count is the whole guarantee. PostgreSQL may
	// have nodes on different versions, so its backstop stays, run once per
	// statement over that statement's distinct scopes.
	store.Register("experience_receipts_retention", 2, `DROP TRIGGER experience_receipt_admit;
 CREATE TRIGGER experience_receipt_admit BEFORE INSERT ON experience_receipts BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) THEN RAISE(ABORT,'receipt admission required') END; END;
`)
	store.RegisterDialect("experience_receipts_retention", 2, store.Postgres, `CREATE OR REPLACE FUNCTION experience_receipt_admit_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(CAST(`+capability.AdmissionLockSQL(ReceiptCapabilityNamespace, "NEW.viewer_id")+` AS BIGINT));
 IF NOT EXISTS(SELECT 1 FROM capability_headers h WHERE h.namespace='experience.receipt' AND h.digest=NEW.hash AND h.shard=(NEW.viewer_id % 64) AND h.viewer_id=NEW.viewer_id AND h.surface=NEW.protocol AND h.device_id=NEW.device_id AND h.expires=NEW.expires*1000) THEN RAISE EXCEPTION 'receipt admission required'; END IF;
 RETURN NEW; END $$;
 CREATE FUNCTION experience_receipt_quota_fn() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE live_after BIGINT; BEGIN
 live_after := CAST(FLOOR(EXTRACT(EPOCH FROM clock_timestamp())) AS BIGINT)*1000;
 IF EXISTS(SELECT 1 FROM (SELECT DISTINCT viewer_id, protocol FROM admitted) s WHERE (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(s.viewer_id % 64) AND viewer_id=s.viewer_id AND surface=s.protocol AND expires>live_after)>100000 OR (SELECT COUNT(*) FROM capability_headers WHERE namespace='experience.receipt' AND shard=(s.viewer_id % 64) AND expires>live_after)>400000) THEN RAISE EXCEPTION 'receipt admission required'; END IF;
 RETURN NULL; END $$;
 CREATE TRIGGER experience_receipt_quota AFTER INSERT ON experience_receipts REFERENCING NEW TABLE AS admitted FOR EACH STATEMENT EXECUTE FUNCTION experience_receipt_quota_fn();
`)
}
