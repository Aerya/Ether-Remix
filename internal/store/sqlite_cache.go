package store

import (
	"os"
	"strconv"

	"github.com/braedonsaunders/ether/internal/hoststat"
)

// SQLite page-cache sizing (#643). cache_size is per connection, so a fixed
// per-connection value multiplies by every pooled connection: 64 MiB each
// is most of a small host's headroom once the pool is warm, yet too little
// for a writer walking a multi-gigabyte plugin library's indexes. Size it
// from one server-wide budget instead: a fraction of the memory the
// process may actually use (the smaller of the container limit and
// physical RAM), divided across the connections the pool keeps warm.
// mmap_size is different: SQLite only maps pages that exist, and mapped
// pages are the OS page cache (evictable, shared), so scale it with the
// database file and let a large library's indexes be read through the map
// instead of copying cold pages through the page cache.
const (
	// sqliteIdleConns is the warm pool the per-connection cache budget is
	// divided across (openSQLite's SetMaxIdleConns).
	sqliteIdleConns = 8
	// sqliteCacheBudgetFloor and Cap bound the server-wide page-cache
	// budget. The floor keeps a tiny or unsized host usable; the cap
	// leaves the rest of RAM to the heap and the OS page cache behind
	// the memory map, which is the cheaper path for clean pages.
	sqliteCacheBudgetFloor = 64 << 20
	sqliteCacheBudgetCap   = 1 << 30
	// sqliteCacheBudgetShare is the fraction of effective memory the
	// server-wide page-cache budget may take.
	sqliteCacheBudgetShare = 8 // one eighth
	// sqliteMmapFloor preserves the previous fixed map for a small or
	// new database; the cap bounds virtual address space, not memory.
	sqliteMmapFloor = 256 << 20
	sqliteMmapCap   = 16 << 30
)

// effectiveMemory returns the memory this process may use: the container
// limit when there is one, physical RAM otherwise, the smaller when both
// are known, and 0 when neither can be read.
func effectiveMemory() uint64 {
	cgroup, _ := hoststat.CgroupLimit("")
	ram := hoststat.PhysicalMemory()
	switch {
	case cgroup > 0 && ram > 0:
		return min(cgroup, ram)
	case cgroup > 0:
		return cgroup
	default:
		return ram
	}
}

// sqliteCacheKiB is the per-connection cache_size magnitude in KiB (the
// pragma takes a negative KiB count). Pure in effective memory: the
// server-wide budget is one share of it, clamped, split across the warm
// pool, so per-connection KiB * sqliteIdleConns never exceeds the cap and
// a small host never budgets more than its share.
func sqliteCacheKiB(effective uint64) int64 {
	budget := uint64(sqliteCacheBudgetFloor)
	if effective > 0 {
		budget = effective / sqliteCacheBudgetShare
	}
	budget = max(budget, sqliteCacheBudgetFloor)
	budget = min(budget, sqliteCacheBudgetCap)
	return int64(budget / sqliteIdleConns / 1024)
}

// sqliteMmapBytes is the mmap_size for a database file of dbBytes: the
// file plus half as much headroom for growth between restarts, clamped,
// and never so large that a 32-bit address space cannot hold it beside
// everything else.
func sqliteMmapBytes(dbBytes int64) int64 {
	mmap := dbBytes + dbBytes/2
	mmap = max(mmap, sqliteMmapFloor)
	mmap = min(mmap, sqliteMmapCap)
	if strconv.IntSize == 32 {
		mmap = min(mmap, sqliteMmapFloor)
	}
	return mmap
}

// sqliteCachePragmas resolves the two pragmas for the database at path
// (absent or empty file: the floors).
func sqliteCachePragmas(path string) (cacheKiB, mmapBytes int64) {
	var dbBytes int64
	if st, err := os.Stat(path); err == nil {
		dbBytes = st.Size()
	}
	return sqliteCacheKiB(effectiveMemory()), sqliteMmapBytes(dbBytes)
}
