package queue

// Decide whether a pre-existing deadline for an item in a delay-queue should be
// updated if an attempt is made to offer/add a new deadline for said item. Whether
// the deadline changes or not has zero impact on the data blob associated with the
// entry in the queue.
type DeadlinePolicy int

const (
	PreferLatest DeadlinePolicy = iota
	PreferEarliest
)

// Decide whether a pre-existing data blob in a delay-queue should be replaced if an
// an attempt is made to add/offer a new data blob in its place. Whether the data is
// replaced has no bearing on the deadline (priority) of the item in the queue.
type ReplacementPolicy int

const (
	KeepExisting ReplacementPolicy = iota
	ReplaceExisting
)

func (rp ReplacementPolicy) replacementValue(original, replacement interface{}) (result interface{}) {
	switch rp {
	case KeepExisting:
		result = original
	case ReplaceExisting:
		fallthrough
	default:
		result = replacement
	}
	return
}

func (dp DeadlinePolicy) nextDeadline(a, b Priority) (result Priority) {
	switch dp {
	case PreferEarliest:
		if a.ts.Before(b.ts) {
			result = a
		} else {
			result = b
		}
	case PreferLatest:
		fallthrough
	default:
		if a.ts.After(b.ts) {
			result = a
		} else {
			result = b
		}
	}
	return
}
