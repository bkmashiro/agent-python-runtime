package transaction

// Ledger is the Host-owned read contract shared by semantic, inspection, and
// future durable backends. Mutation remains sealed behind Coordinator so callers
// cannot bypass authority, ordering, or compare-and-swap checks.
type Ledger interface {
	GetTransaction(string) (Transaction, error)
	GetOperation(string) (Operation, error)
	ListOperations(string) ([]Operation, error)
	GetAttempt(string) (Attempt, error)
	ListAttempts(string) ([]Attempt, error)
	ListTransitions(string) ([]Transition, error)
}

var _ Ledger = (*MemoryLedger)(nil)
