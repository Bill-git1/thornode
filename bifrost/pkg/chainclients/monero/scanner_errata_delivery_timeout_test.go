package monero

import (
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
)

// deliverReorgErrata sends on an unbuffered queue while holding scanMu, from
// inside FetchTxs. If the observer's errata loop has already exited, an
// unbounded send parks the block-scanner goroutine forever and
// BlockScanner.Stop() hangs on wg.Wait(). Both the send and the acknowledgement
// must therefore be bounded, and the failure must be a returned error so the
// replay is redriven on the next FetchTxs.
func TestDeliverReorgErrataTimesOutWhenObserverIsGone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		ackRequired bool
	}{
		// Nobody ever receives: the send itself must time out.
		{name: "observer not consuming", ackRequired: false},
		// Received but never acknowledged: the ack wait must time out.
		{name: "observer never acknowledges", ackRequired: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
			scanner.errataDeliveryTimeout = 50 * time.Millisecond

			queue := make(chan stypes.ErrataBlock)
			if tc.ackRequired {
				scanner.SetErrataQueueWithAck(queue)
				// Drain the send but never write to block.Persisted.
				go func() { <-queue }()
			} else {
				scanner.SetErrataQueue(queue)
			}

			done := make(chan error, 1)
			go func() {
				done <- scanner.deliverReorgErrata([]reorgErrataRecord{
					{Height: 100, TxIDs: []string{testXMRHash("stranded-tx")}},
				})
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected a timeout error, got nil")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("deliverReorgErrata blocked forever; scanMu is held here, " +
					"so this wedges the scanner and hangs BlockScanner.Stop()")
			}
		})
	}
}
