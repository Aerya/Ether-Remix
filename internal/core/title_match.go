package core

// TitleKeyRange maps an inclusive range of rating keys by a constant offset.
type TitleKeyRange struct {
	First, Last, Delta int64
}
