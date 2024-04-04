package types

////////////////////////////////////////////////////////////////////////////////////////
// Ops
////////////////////////////////////////////////////////////////////////////////////////

// OpConfig is the configuration passed to each operation during execution.
type OpConfig struct {
	// AdminAccount is the client for the mimir admin account.
	AdminAccount *Account

	// NodeAccounts is a slice clients for simulation validator keys.
	NodeAccounts []*Account

	// UserAccounts is a slice of clients for simulation user keys.
	UserAccounts []*Account
}

// OpResult is the result of an operation.
type OpResult struct {
	// Continue indicates that actor should continue to the next operation.
	Continue bool

	// Finish indicates that the actor should stop executing and return the error.
	Finish bool

	// Error is the error returned by the operation.
	Error error
}

// Op is an operation that can be executed by an actor.
type Op func(config *OpConfig) OpResult
