package transaction

type JournalSnapshot struct {
	Transaction Transaction
	Operations  []Operation
	Attempts    []Attempt
	Transitions []Transition
}

// Ledger is the Host-owned read contract shared by semantic, inspection, and
// durable backends. Snapshot must observe all four collections at one atomic
// read boundary so exported evidence cannot combine states that never coexisted.
type Ledger interface {
	GetTransaction(string) (Transaction, error)
	GetOperation(string) (Operation, error)
	ListOperations(string) ([]Operation, error)
	GetAttempt(string) (Attempt, error)
	ListAttempts(string) ([]Attempt, error)
	ListTransitions(string) ([]Transition, error)
	Snapshot(string) (JournalSnapshot, error)
}

var _ Ledger = (*MemoryLedger)(nil)
