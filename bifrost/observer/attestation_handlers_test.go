package observer

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

func TestHandleStreamObservedTxAttestation(t *testing.T) {
	agVal1, _, _, _, _, _ := setupTestGossip(t)
	agVal2, _, _, _, _, _ := setupTestGossip(t)

	numVals := 2
	agVals := []*AttestationGossip{agVal1, agVal2}
	valPrivs := make([]*secp256k1.PrivKey, numVals)
	valPubs := make([]common.PubKey, numVals)
	for i := 0; i < numVals; i++ {
		priv := agVals[i].privKey
		var ok bool
		valPrivs[i], ok = priv.(*secp256k1.PrivKey)
		require.True(t, ok, "Should be able to cast private key to secp256k1")
		bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, valPrivs[i].PubKey())
		require.NoError(t, err, "Should be able to convert pubkey to bech32")
		valPubs[i] = common.PubKey(bech32Pub)
	}

	// Set active vals
	agVal1.setActiveValidators(valPubs)
	agVal2.setActiveValidators(valPubs)

	val1Reader := bytes.NewBuffer(nil)
	val1Writer := &bytes.Buffer{}

	val2Reader := val1Writer
	val2Writer := val1Reader

	var sharedMu sync.Mutex

	val2Stream := &MockStream{
		reader: val2Reader,
		writer: val2Writer,
		peer:   agVal1.host.ID(),
		mu:     &sharedMu,
	}

	val1Stream := &MockStream{
		reader: val1Reader,
		writer: val1Writer,
		peer:   agVal2.host.ID(),
		mu:     &sharedMu,
	}

	observedTx := &common.ObservedTx{
		Tx: common.Tx{
			ID: "tx-id",
		},
	}

	signBz, err := observedTx.GetSignablePayload()
	require.NoError(t, err, "Should be able to get signable payload")

	val1Sig, err := valPrivs[0].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val2Sig, err := valPrivs[1].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val1Attestation := &common.Attestation{
		PubKey:    valPrivs[0].PubKey().Bytes(),
		Signature: val1Sig,
	}

	val1AttestTx := &common.AttestTx{
		ObsTx:       *observedTx,
		Attestation: val1Attestation,
	}

	val2Attestation := &common.Attestation{
		PubKey:    valPrivs[1].PubKey().Bytes(),
		Signature: val2Sig,
	}

	val2AttestTx := &common.AttestTx{
		ObsTx:       *observedTx,
		Attestation: val2Attestation,
	}

	val1AttestTxBz, err := val1AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	val2AttestTxBz, err := val2AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	ctx := context.Background()

	// Call the method with a timeout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal1.handleObservedTxAttestation(context.TODO(), *val1AttestTx)
		agVal1.batcher.sendPayloadToStream(ctx, val1Stream, val1AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal2.handleStreamObservedTx(val2Stream)
	}()

	wg.Wait()

	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal2.handleObservedTxAttestation(context.TODO(), *val2AttestTx)
		agVal2.batcher.sendPayloadToStream(ctx, val2Stream, val2AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal1.handleStreamObservedTx(val1Stream)
	}()

	wg.Wait()

	agVal1.mu.Lock()
	require.Len(t, agVal1.observedTxs, 1, "Should have 1 observed tx")
	for _, tx := range agVal1.observedTxs {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal1.mu.Unlock()

	agVal2.mu.Lock()
	require.Len(t, agVal2.observedTxs, 1, "Should have 1 observed tx")
	for _, tx := range agVal2.observedTxs {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal2.mu.Unlock()
}

func TestHandleStreamNetworkFeeAttestation(t *testing.T) {
	agVal1, _, _, _, _, _ := setupTestGossip(t)
	agVal2, _, _, _, _, _ := setupTestGossip(t)

	numVals := 2
	agVals := []*AttestationGossip{agVal1, agVal2}
	valPrivs := make([]*secp256k1.PrivKey, numVals)
	valPubs := make([]common.PubKey, numVals)
	for i := 0; i < numVals; i++ {
		priv := agVals[i].privKey
		var ok bool
		valPrivs[i], ok = priv.(*secp256k1.PrivKey)
		require.True(t, ok, "Should be able to cast private key to secp256k1")
		bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, valPrivs[i].PubKey())
		require.NoError(t, err, "Should be able to convert pubkey to bech32")
		valPubs[i] = common.PubKey(bech32Pub)
	}

	// Set active vals
	agVal1.setActiveValidators(valPubs)
	agVal2.setActiveValidators(valPubs)

	val1Reader := bytes.NewBuffer(nil)
	val1Writer := &bytes.Buffer{}

	val2Reader := val1Writer
	val2Writer := val1Reader

	var sharedMu sync.Mutex

	val2Stream := &MockStream{
		reader: val2Reader,
		writer: val2Writer,
		peer:   agVal1.host.ID(),
		mu:     &sharedMu,
	}

	val1Stream := &MockStream{
		reader: val1Reader,
		writer: val1Writer,
		peer:   agVal2.host.ID(),
		mu:     &sharedMu,
	}

	networkFee := &common.NetworkFee{
		Height:          1,
		Chain:           common.ETHChain,
		TransactionSize: 1000,
		TransactionRate: 10,
	}

	signBz, err := networkFee.GetSignablePayload()
	require.NoError(t, err, "Should be able to get signable payload")

	val1Sig, err := valPrivs[0].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val2Sig, err := valPrivs[1].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val1Attestation := &common.Attestation{
		PubKey:    valPrivs[0].PubKey().Bytes(),
		Signature: val1Sig,
	}

	val1AttestTx := &common.AttestNetworkFee{
		NetworkFee:  networkFee,
		Attestation: val1Attestation,
	}

	val2Attestation := &common.Attestation{
		PubKey:    valPrivs[1].PubKey().Bytes(),
		Signature: val2Sig,
	}

	val2AttestTx := &common.AttestNetworkFee{
		NetworkFee:  networkFee,
		Attestation: val2Attestation,
	}

	val1AttestTxBz, err := val1AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	val2AttestTxBz, err := val2AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	ctx := context.Background()

	// Call the method with a timeout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal1.handleNetworkFeeAttestation(context.TODO(), *val1AttestTx)
		agVal1.batcher.sendPayloadToStream(ctx, val1Stream, val1AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal2.handleStreamNetworkFee(val2Stream)
	}()

	wg.Wait()

	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal2.handleNetworkFeeAttestation(context.TODO(), *val2AttestTx)
		agVal2.batcher.sendPayloadToStream(ctx, val2Stream, val2AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal1.handleStreamNetworkFee(val1Stream)
	}()

	wg.Wait()

	agVal1.mu.Lock()
	require.Len(t, agVal1.networkFees, 1, "Should have 1 network fee")
	for _, tx := range agVal1.networkFees {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal1.mu.Unlock()

	agVal2.mu.Lock()
	require.Len(t, agVal2.networkFees, 1, "Should have 1 network fee")
	for _, tx := range agVal2.networkFees {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal2.mu.Unlock()
}

func TestHandleStreamSolvencyAttestation(t *testing.T) {
	agVal1, _, _, _, _, _ := setupTestGossip(t)
	agVal2, _, _, _, _, _ := setupTestGossip(t)

	numVals := 2
	agVals := []*AttestationGossip{agVal1, agVal2}
	valPrivs := make([]*secp256k1.PrivKey, numVals)
	valPubs := make([]common.PubKey, numVals)
	for i := 0; i < numVals; i++ {
		priv := agVals[i].privKey
		var ok bool
		valPrivs[i], ok = priv.(*secp256k1.PrivKey)
		require.True(t, ok, "Should be able to cast private key to secp256k1")
		bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, valPrivs[i].PubKey())
		require.NoError(t, err, "Should be able to convert pubkey to bech32")
		valPubs[i] = common.PubKey(bech32Pub)
	}

	// Set active vals
	agVal1.setActiveValidators(valPubs)
	agVal2.setActiveValidators(valPubs)

	val1Reader := bytes.NewBuffer(nil)
	val1Writer := &bytes.Buffer{}

	val2Reader := val1Writer
	val2Writer := val1Reader

	var sharedMu sync.Mutex

	val2Stream := &MockStream{
		reader: val2Reader,
		writer: val2Writer,
		peer:   agVal1.host.ID(),
		mu:     &sharedMu,
	}

	val1Stream := &MockStream{
		reader: val1Reader,
		writer: val1Writer,
		peer:   agVal2.host.ID(),
		mu:     &sharedMu,
	}

	solvency := &common.Solvency{
		Height: 1,
		Chain:  common.ETHChain,
		PubKey: common.PubKey("pubkey"),
		Coins: []common.Coin{
			{
				Asset:    common.ETHAsset,
				Amount:   cosmos.NewUint(100),
				Decimals: 18,
			},
		},
	}

	// Set the solvency ID
	id, err := solvency.Hash()
	require.NoError(t, err, "Should be able to hash solvency")
	solvency.Id = id

	signBz, err := solvency.GetSignablePayload()
	require.NoError(t, err, "Should be able to get signable payload")

	val1Sig, err := valPrivs[0].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val2Sig, err := valPrivs[1].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val1Attestation := &common.Attestation{
		PubKey:    valPrivs[0].PubKey().Bytes(),
		Signature: val1Sig,
	}

	val1AttestTx := &common.AttestSolvency{
		Solvency:    solvency,
		Attestation: val1Attestation,
	}

	val2Attestation := &common.Attestation{
		PubKey:    valPrivs[1].PubKey().Bytes(),
		Signature: val2Sig,
	}

	val2AttestTx := &common.AttestSolvency{
		Solvency:    solvency,
		Attestation: val2Attestation,
	}

	val1AttestTxBz, err := val1AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	val2AttestTxBz, err := val2AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	ctx := context.Background()

	// Call the method with a timeout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal1.handleSolvencyAttestation(context.TODO(), *val1AttestTx)
		agVal1.batcher.sendPayloadToStream(ctx, val1Stream, val1AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal2.handleStreamSolvency(val2Stream)
	}()

	wg.Wait()

	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal2.handleSolvencyAttestation(context.TODO(), *val2AttestTx)
		agVal2.batcher.sendPayloadToStream(ctx, val2Stream, val2AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal1.handleStreamSolvency(val1Stream)
	}()

	wg.Wait()

	agVal1.mu.Lock()
	require.Len(t, agVal1.solvencies, 1, "Should have 1 solvency")
	for _, tx := range agVal1.solvencies {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal1.mu.Unlock()

	agVal2.mu.Lock()
	require.Len(t, agVal2.solvencies, 1, "Should have 1 solvency")
	for _, tx := range agVal2.solvencies {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal2.mu.Unlock()
}

func TestHandleStreamErrataTxAttestation(t *testing.T) {
	agVal1, _, _, _, _, _ := setupTestGossip(t)
	agVal2, _, _, _, _, _ := setupTestGossip(t)

	numVals := 2
	agVals := []*AttestationGossip{agVal1, agVal2}
	valPrivs := make([]*secp256k1.PrivKey, numVals)
	valPubs := make([]common.PubKey, numVals)
	for i := 0; i < numVals; i++ {
		priv := agVals[i].privKey
		var ok bool
		valPrivs[i], ok = priv.(*secp256k1.PrivKey)
		require.True(t, ok, "Should be able to cast private key to secp256k1")
		bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, valPrivs[i].PubKey())
		require.NoError(t, err, "Should be able to convert pubkey to bech32")
		valPubs[i] = common.PubKey(bech32Pub)
	}

	// Set active vals
	agVal1.setActiveValidators(valPubs)
	agVal2.setActiveValidators(valPubs)

	val1Reader := bytes.NewBuffer(nil)
	val1Writer := &bytes.Buffer{}

	val2Reader := val1Writer
	val2Writer := val1Reader

	var sharedMu sync.Mutex

	val2Stream := &MockStream{
		reader: val2Reader,
		writer: val2Writer,
		peer:   agVal1.host.ID(),
		mu:     &sharedMu,
	}

	val1Stream := &MockStream{
		reader: val1Reader,
		writer: val1Writer,
		peer:   agVal2.host.ID(),
		mu:     &sharedMu,
	}

	errata := &common.ErrataTx{
		Chain: common.ETHChain,
		Id:    "tx-id",
	}

	signBz, err := errata.GetSignablePayload()
	require.NoError(t, err, "Should be able to get signable payload")

	val1Sig, err := valPrivs[0].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val2Sig, err := valPrivs[1].Sign(signBz)
	require.NoError(t, err, "Should be able to sign payload")

	val1Attestation := &common.Attestation{
		PubKey:    valPrivs[0].PubKey().Bytes(),
		Signature: val1Sig,
	}

	val1AttestTx := &common.AttestErrataTx{
		ErrataTx:    errata,
		Attestation: val1Attestation,
	}

	val2Attestation := &common.Attestation{
		PubKey:    valPrivs[1].PubKey().Bytes(),
		Signature: val2Sig,
	}

	val2AttestTx := &common.AttestErrataTx{
		ErrataTx:    errata,
		Attestation: val2Attestation,
	}

	val1AttestTxBz, err := val1AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	val2AttestTxBz, err := val2AttestTx.Marshal()
	require.NoError(t, err, "Should be able to marshal attestation")

	ctx := context.Background()

	// Call the method with a timeout
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal1.handleErrataAttestation(context.TODO(), *val1AttestTx)
		agVal1.batcher.sendPayloadToStream(ctx, val1Stream, val1AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal2.handleStreamErrataTx(val2Stream)
	}()

	wg.Wait()

	wg.Add(2)
	go func() {
		defer wg.Done()
		agVal2.handleErrataAttestation(context.TODO(), *val2AttestTx)
		agVal2.batcher.sendPayloadToStream(ctx, val2Stream, val2AttestTxBz)
	}()

	go func() {
		defer wg.Done()
		agVal1.handleStreamErrataTx(val1Stream)
	}()

	wg.Wait()

	agVal1.mu.Lock()
	require.Len(t, agVal1.errataTxs, 1, "Should have 1 errata tx")
	for _, tx := range agVal1.errataTxs {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal1.mu.Unlock()

	agVal2.mu.Lock()
	require.Len(t, agVal2.errataTxs, 1, "Should have 1 errata tx")
	for _, tx := range agVal2.errataTxs {
		require.Len(t, tx.attestations, 2, "Should have 2 attestations")
		require.True(t, bytes.Equal(tx.attestations[0].attestation.PubKey, val1Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[0].attestation.Signature, val1Attestation.Signature))

		require.True(t, bytes.Equal(tx.attestations[1].attestation.PubKey, val2Attestation.PubKey))
		require.True(t, bytes.Equal(tx.attestations[1].attestation.Signature, val2Attestation.Signature))
	}
	agVal2.mu.Unlock()
}
