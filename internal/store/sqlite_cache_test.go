package store

import "testing"

// The lasting invariants of the server-wide cache budget: the warm pool's
// combined page cache stays within the cap, never takes more than the
// memory share on a host above the floor, and grows with host memory.
func TestSQLiteCacheBudgetInvariants(t *testing.T) {
	hosts := []uint64{0, 256 << 20, 1 << 30, 2 << 30, 8 << 30, 32 << 30, 256 << 30}
	var prev int64
	for _, mem := range hosts {
		per := sqliteCacheKiB(mem)
		if per <= 0 {
			t.Fatalf("mem %d: per-conn cache %d KiB", mem, per)
		}
		if total := per * 1024 * sqliteIdleConns; total > sqliteCacheBudgetCap {
			t.Fatalf("mem %d: warm pool %d bytes exceeds cap %d", mem, total, sqliteCacheBudgetCap)
		}
		if mem/sqliteCacheBudgetShare >= sqliteCacheBudgetFloor {
			if total := uint64(per * 1024 * sqliteIdleConns); total > mem/sqliteCacheBudgetShare {
				t.Fatalf("mem %d: warm pool %d exceeds share %d", mem, total, mem/sqliteCacheBudgetShare)
			}
		}
		if per < prev {
			t.Fatalf("cache shrank as memory grew at %d: %d < %d", mem, per, prev)
		}
		prev = per
	}
	// A small host must not keep the old fixed 64 MiB per connection.
	if got := sqliteCacheKiB(1 << 30); got >= 65536 {
		t.Fatalf("1 GiB host per-conn cache %d KiB, want below the old fixed 65536", got)
	}
}

func TestSQLiteMmapScalesWithDatabase(t *testing.T) {
	if got := sqliteMmapBytes(0); got != sqliteMmapFloor {
		t.Fatalf("empty database mmap %d, want floor %d", got, sqliteMmapFloor)
	}
	big := sqliteMmapBytes(11 << 30)
	if big <= 11<<30 || big > sqliteMmapCap {
		t.Fatalf("11 GiB database mmap %d, want the file plus headroom within the cap", big)
	}
	if got := sqliteMmapBytes(1 << 40); got != sqliteMmapCap {
		t.Fatalf("huge database mmap %d, want cap %d", got, sqliteMmapCap)
	}
}
