package types

import "errors"

// ErrTxExpired means this signing attempt can no longer be mined. The signer
// must retire it without observing success or rebuilding it with a new digest;
// THORChain remains responsible for rescheduling the outbound.
var ErrTxExpired = errors.New("transaction expired")
