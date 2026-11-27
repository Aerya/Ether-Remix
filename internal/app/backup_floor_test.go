package app

import "github.com/braedonsaunders/ether/internal/backup"

// Recovery and restore tests stage tiny backups; the owner-facing free-space
// floor would make them depend on the test machine's disk.
func init() { backup.DisableStageFloorForTests() }
