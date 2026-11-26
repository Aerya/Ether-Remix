package backup

// Tests stage small bundles on whatever volume the machine has; the
// owner-facing floor is not what they exercise.
func init() { DisableStageFloorForTests() }
