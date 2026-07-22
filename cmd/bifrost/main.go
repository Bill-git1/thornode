package main

import (
	"context"
	"errors" // TEMP: keyverify
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"    // TEMP: keyverify
	"strconv" // TEMP: keyverify
	"strings"
	"sync"
	"syscall"
	"time"

	golog "github.com/ipfs/go-log"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	flag "github.com/spf13/pflag"
	libtss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss" // TEMP: keyverify

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame" // TEMP: keyverify
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
	kvecdsa "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keyverify/ecdsa" // TEMP: keyverify
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/tss"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/keyverify" // TEMP: keyverify

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/observer"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero"
	"gitlab.com/thorchain/thornode/v3/bifrost/pubkeymanager"
	"gitlab.com/thorchain/thornode/v3/bifrost/signer"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	btss "gitlab.com/thorchain/thornode/v3/bifrost/tss"
	"gitlab.com/thorchain/thornode/v3/cmd"
	tcommon "gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/constants"
)

const (
	serverIdentity = "bifrost"
)

func printVersion() {
	fmt.Printf("%s v%s, rev %s\n", serverIdentity, constants.Version, constants.GitCommit)
}

func main() {
	showVersion := flag.Bool("version", false, "Shows version")
	logLevel := flag.StringP("log-level", "l", "info", "Log Level")
	pretty := flag.BoolP("pretty-log", "p", false, "Enables unstructured prettified logging. This is useful for local debugging")
	deckDump := flag.String("deck-dump", "", "Path to a deck dump file")
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

	initPrefix()
	initLog(*logLevel, *pretty)
	config.Init()
	config.InitBifrost()
	cfg := config.GetBifrost()

	// metrics
	m, err := metrics.NewMetrics(cfg.Metrics)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create metric instance")
	}
	if err = m.Start(); err != nil {
		log.Fatal().Err(err).Msg("fail to start metric collector")
	}
	if len(cfg.Thorchain.SignerName) == 0 {
		log.Fatal().Msg("signer name is empty")
	}
	if len(cfg.Thorchain.SignerPasswd) == 0 {
		log.Fatal().Msg("signer password is empty")
	}
	kb, _, err := thorclient.GetKeyringKeybase(cfg.Thorchain.ChainHomeFolder, cfg.Thorchain.SignerName, cfg.Thorchain.SignerPasswd)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to get keyring keybase")
	}

	k := thorclient.NewKeysWithKeybase(kb, cfg.Thorchain.SignerName, cfg.Thorchain.SignerPasswd)
	// thorchain bridge
	thorchainBridge, err := thorclient.NewThorchainBridge(cfg.Thorchain, m, k)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create new thorchain bridge")
	}
	if err = thorchainBridge.EnsureNodeWhitelistedWithTimeout(); err != nil {
		log.Fatal().Err(err).Msg("node account is not whitelisted, can't start")
	}
	// PubKey Manager
	pubkeyMgr, err := pubkeymanager.NewPubKeyManager(thorchainBridge, m)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create pubkey manager")
	}
	if err = pubkeyMgr.Start(); err != nil {
		log.Fatal().Err(err).Msg("fail to start pubkey manager")
	}

	// Legacy shares back the process-wide TSS stack and remain a startup
	// prerequisite. XMR FROST shares are isolated by exact-vault readiness, so a
	// missing historical XMR vault is retried without disabling healthy chains.
	if !recoverLegacyKeysharesBeforeStartup(cfg, thorchainBridge) {
		return
	}
	recoveryCtx, cancelRecovery := context.WithCancel(context.Background())
	go superviseFrostKeyshareRecovery(recoveryCtx, cfg, thorchainBridge)

	// setup TSS signing
	priKey, err := k.GetPrivateKey()
	if err != nil {
		log.Fatal().Err(err).Msg("fail to get secp256k1 private key")
	}

	tmPrivateKey := tcommon.CosmosPrivateKeyToTMPrivateKey(priKey)

	priKeyEddsa, err := k.GetPrivateKeyEDDSA()
	if err != nil {
		log.Fatal().Err(err).Msg("fail to get eddsa private key")
	}

	tmPrivateKeyEddsa := tcommon.CosmosPrivateKeyToTMPrivateKey(priKeyEddsa)

	consts := constants.NewConstantValue()
	jailTimeKeygen := time.Duration(consts.GetInt64Value(constants.JailTimeKeygen)) * constants.ThorchainBlockTime
	jailTimeKeysign := time.Duration(consts.GetInt64Value(constants.JailTimeKeysign)) * constants.ThorchainBlockTime
	if cfg.Signer.KeygenTimeout >= jailTimeKeygen {
		log.Fatal().
			Stringer("keygenTimeout", cfg.Signer.KeygenTimeout).
			Stringer("keygenJail", jailTimeKeygen).
			Msg("keygen timeout must be shorter than jail time")
	}
	// FrostKeygenTimeout is intentionally NOT bound by jailTimeKeygen.
	// FROST DKG is a 3-round p2p protocol whose round-2 message exchange
	// can legitimately exceed the aggressive jail times used in test
	// environments. The 60s
	// floor inside NewFrostKeygenCoordinator + per-environment overrides
	// in docker-compose are the relevant safety nets here.
	if cfg.Signer.KeysignTimeout >= jailTimeKeysign {
		log.Fatal().
			Stringer("keysignTimeout", cfg.Signer.KeysignTimeout).
			Stringer("keysignJail", jailTimeKeysign).
			Msg("keysign timeout must be shorter than jail time")
	}

	// Start P2P with bonded node gating enabled
	comm, stateManager, err := p2p.StartP2PWithBridge(
		cfg.TSS,
		tmPrivateKey,
		app.DefaultNodeHome,
		thorchainBridge,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to start p2p")
	}

	tssIns, err := tss.NewTss(
		comm,
		stateManager,
		tmPrivateKey,
		tmPrivateKeyEddsa,
		common.TssConfig{
			EnableMonitor:      true,
			KeyGenTimeout:      cfg.Signer.KeygenTimeout,
			FrostKeygenTimeout: cfg.Signer.FrostKeygenTimeout,
			KeySignTimeout:     cfg.Signer.KeysignTimeout,
			PartyTimeout:       cfg.Signer.PartyTimeout,
			PreParamTimeout:    cfg.Signer.PreParamTimeout,
		},
		nil,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create tss instance")
	}

	if err = tssIns.Start(); err != nil {
		log.Err(err).Msg("fail to start tss instance")
	}

	cfgChains := cfg.GetChains()

	// ensure we have a protocol for chain RPC Hosts
	for _, chainCfg := range cfgChains {
		if chainCfg.Disabled {
			continue
		}
		if len(chainCfg.RPCHost) == 0 {
			log.Fatal().Err(err).Stringer("chain", chainCfg.ChainID).Msg("missing chain RPC host")
			return
		}
		if !strings.HasPrefix(chainCfg.RPCHost, "http") {
			chainCfg.RPCHost = fmt.Sprintf("http://%s", chainCfg.RPCHost)
		}
	}
	poolMgr := thorclient.NewPoolMgr(thorchainBridge)
	chains, restart := chainclients.LoadChains(k, cfgChains, tssIns, thorchainBridge, m, pubkeyMgr, poolMgr)
	if len(chains) == 0 {
		log.Fatal().Msg("fail to load any chains")
	}
	tssKeysignMetricMgr := metrics.NewTssKeysignMetricMgr()
	// TEMP: keyverify
	keyverifyMarkers := keyverify.NewMarkerStore(app.DefaultNodeHome)
	healthServer := NewHealthServer(cfg.TSS.InfoAddress, tssIns, chains, keyverifyMarkers, cfg.Signer.KeyverifyDisabled) // TEMP: keyverify (markerStore, disabled params)
	go func() {
		defer log.Info().Msg("health server exit")
		if err = healthServer.Start(); err != nil {
			log.Error().Err(err).Msg("fail to start health server")
		}
	}()

	// TEMP: keyverify — scheduler block; remove as a unit with the keyverify rollout.
	// Cancel is invoked explicitly in the graceful-shutdown path below; we avoid
	// defer because subsequent log.Fatal calls would skip it (gocritic exitAfterDefer).
	// keyverifyDone is closed once all keyverify background goroutines return,
	// so shutdown can wait for any in-flight Verify (which is still using
	// tssIns.GetP2PComm and comm.CancelSubscribe via deferred cleanup) to
	// finish before tssIns.Stop() tears down the comm/host underneath it.
	//
	// keyverifyStopChan is the out-of-band exit signal passed to every
	// LiveVerifier-built TssKeyVerify; processVerify selects on it as an
	// explicit "abandon now" trigger (independent of ctx). It's closed
	// inside the shutdown sequence below alongside keyverifyCancel() so
	// the parameter is actually wired up rather than a dead receiver.
	keyverifyCtx, keyverifyCancel := context.WithCancel(context.Background())
	keyverifyDone := make(chan struct{})
	keyverifyStopChan := make(chan struct{})
	var keyverifyWG sync.WaitGroup

	// Sweep keyverify-msgID parked p2p streams on every node. Disabled nodes
	// need this because they never run a local Verify for inbound keyverify
	// msgIDs. Enabled nodes also need it after a vault is locally marked .ok:
	// the scheduler skips that vault, but peers that are still failing it can
	// keep broadcasting keyverify attempts under new window msgIDs that no
	// local Verify will release. The sweep is scoped to keyverify.MsgIDPrefix
	// (plus StreamUnknown) so it can never reset a keygen/keysign/FROST
	// stream whose payload the remote peer has not finished reading — Reset
	// is abortive and discards the peer's buffered-but-unread data. The same
	// race against an in-flight keyverify attempt's own streams remains and
	// is accepted: worst case is one spurious attempt failure, retried next
	// window.
	log.Info().Msg("starting keyverify parked-stream sweeper")
	keyverifyWG.Add(1)
	go func() {
		defer keyverifyWG.Done()
		defer log.Info().Msg("keyverify parked-stream sweeper exit")
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-keyverifyCtx.Done():
				return
			case <-ticker.C:
				comm.ReleaseStreamsByPrefix(keyverify.MsgIDPrefix)
			}
		}
	}()

	if cfg.Signer.KeyverifyDisabled {
		// Operator opted out: never start the scheduler. Peers still fan
		// keyverify messages out to the on-chain vault membership (there is no
		// opt-in handshake), and the unconditional sweeper above releases those
		// parked inbound streams.
		//
		// keyverifyCancel() in the shutdown sequence stops the sweeper;
		// close(keyverifyStopChan) remains a harmless no-op (nothing receives
		// on it in this branch).
		log.Info().Msg("keyverify scheduler disabled by config (keyverify_disabled)")
	} else {
		localNodePubKey := tssIns.GetLocalNodePubKey()
		live := keyverify.NewLiveVerifier(
			tssIns.GetP2PComm(),
			tssIns.GetBroadcastChan(),
			stateManager,
			common.TssConfig{
				KeyGenTimeout:          cfg.Signer.KeygenTimeout,
				KeySignTimeout:         cfg.Signer.KeysignTimeout,
				PartyTimeout:           cfg.Signer.PartyTimeout,
				PreParamTimeout:        cfg.Signer.PreParamTimeout,
				KeyverifyRoundTimeout:  cfg.Signer.KeyverifyRoundTimeout,
				KeyverifyRetryInterval: cfg.Signer.KeyverifyRetryInterval,
				KeyverifyMaxRetries:    cfg.Signer.KeyverifyMaxRetries,
			},
			localNodePubKey,
			tmPrivateKey,
			keyverifyStopChan,
		)
		listVaults := func(_ context.Context) ([]keyverify.Vault, error) {
			vs, vErr := thorchainBridge.GetAsgards()
			if vErr != nil {
				return nil, vErr
			}
			out := make([]keyverify.Vault, 0, len(vs))
			for _, v := range vs {
				if !v.IsAsgard() {
					continue
				}
				if !v.IsActive() {
					continue
				}
				if !v.Contains(tcommon.PubKey(localNodePubKey)) {
					continue
				}
				members := make([]string, 0, len(v.GetMembership()))
				for _, pk := range v.GetMembership() {
					members = append(members, pk.String())
				}
				out = append(out, keyverify.Vault{PubKey: v.PubKey.String(), Members: members})
			}
			return out, nil
		}
		getHeight := func(_ context.Context) (int64, error) {
			return thorchainBridge.GetBlockHeight()
		}
		isTimeoutErr := func(err error) bool { return errors.Is(err, blame.ErrTssTimeOut) }
		extractTimeoutInfo := func(err error) ([]string, []keyverify.Culprit, string) {
			var tErr *kvecdsa.TimeoutError
			if !errors.As(err, &tErr) {
				return nil, nil, ""
			}
			details := make([]keyverify.Culprit, 0, len(tErr.Details))
			for _, d := range tErr.Details {
				details = append(details, keyverify.Culprit{
					PubKey:            d.PubKey,
					LastAcceptedRound: d.LastAcceptedRound,
					LastWireRound:     d.LastWireRound,
					LastSeenAt:        d.LastSeenAt,
					SharesAccepted:    d.SharesAccepted,
					SharesExpected:    d.SharesExpected,
				})
			}
			return tErr.Culprits, details, tErr.LocalRound
		}
		extractCulprits := func(err error, members []string) (string, []string, bool) {
			var tssErr *libtss.Error
			if errors.As(err, &tssErr) {
				// conversion.GetParties sorts members lexicographically and
				// uses the post-sort slice index as the PartyID.Id string.
				// Reverse that mapping here so culprits surface as thorpubs.
				sorted := append([]string(nil), members...)
				sort.Strings(sorted)
				pks := make([]string, 0, len(tssErr.Culprits()))
				for _, p := range tssErr.Culprits() {
					idx, perr := strconv.Atoi(p.Id)
					if perr != nil || idx < 0 || idx >= len(sorted) {
						log.Warn().Str("culprit_id", p.Id).Msg("unable to map culprit id to thorpub; using raw id")
						pks = append(pks, p.Id)
						continue
					}
					pks = append(pks, sorted[idx])
				}
				return tssErr.Error(), pks, true
			}
			return "", nil, false
		}
		sched := keyverify.NewScheduler(
			keyverifyMarkers,
			live,
			log.With().Str("module", "keyverify").Logger(),
			getHeight,
			listVaults,
			localNodePubKey,
			isTimeoutErr,
			extractCulprits,
			extractTimeoutInfo,
			cfg.Signer.KeyverifyPerVaultTimeout,
		)
		keyverifyWG.Add(1)
		go func() {
			defer keyverifyWG.Done()
			defer log.Info().Msg("keyverify scheduler exit")
			sched.Start(keyverifyCtx)
		}()
	}
	go func() {
		keyverifyWG.Wait()
		close(keyverifyDone)
	}()

	ctx := context.Background()

	// start observer notifier
	ag, err := observer.NewAttestationGossip(comm.GetHost(), k, cfg.Thorchain.ChainEBifrost, thorchainBridge, m, cfg.AttestationGossip)

	// start observer
	obs, err := observer.NewObserver(pubkeyMgr, chains, thorchainBridge, m, cfgChains[tcommon.BTCChain].BlockScanner.DBPath, tssKeysignMetricMgr, ag, *deckDump)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create observer")
	}
	// Register committed-event callbacks before Start launches the gossip event
	// client. NewObserver also does this defensively for non-main call sites.
	ag.SetObserverHandleObservedTxCommitted(obs)
	ag.SetObserverHandleErrataTxCommitted(obs)
	if err = obs.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("fail to start observer")
	}

	if xmrClient, ok := chains[tcommon.XMRChain].(*monero.Client); ok {
		ag.SetObserverApplySpentOutputRefs(xmrClient.ApplySpentOutputRefsWithOutcome)
	}

	// start signer
	sign, err := signer.NewSigner(cfg, thorchainBridge, k, pubkeyMgr, tssIns, chains, m, tssKeysignMetricMgr, obs)
	if err != nil {
		log.Fatal().Err(err).Msg("fail to create instance of signer")
	}
	if err = sign.Start(); err != nil {
		log.Fatal().Err(err).Msg("fail to start signer")
	}

	// wait....
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ch:
	case <-restart:
	}
	log.Info().Msg("stop signal received")
	cancelRecovery()

	// TEMP: keyverify — fire both stop signals together. keyverifyCancel
	// cancels the per-attempt ctx (processVerify exits via <-ctx.Done()
	// within milliseconds), and close(keyverifyStopChan) closes the
	// out-of-band exit channel that processVerify also selects on (so the
	// stopChan parameter passed into NewLiveVerifier is actually wired up,
	// not a dead receiver).
	keyverifyCancel()
	close(keyverifyStopChan)

	// stop observer
	if err = obs.Stop(); err != nil {
		log.Fatal().Err(err).Msg("fail to stop observer")
	}
	// stop signer
	if err = sign.Stop(); err != nil {
		log.Fatal().Err(err).Msg("fail to stop signer")
	}
	// TEMP: keyverify — wait for the scheduler goroutine to drain before
	// tearing down the TSS comm/host. The scheduler's in-flight Verify may
	// still be calling tssIns.GetP2PComm()/comm.CancelSubscribe via deferred
	// cleanup; tssIns.Stop() must not race with that.
	//
	// In practice this returns within milliseconds: keyverifyCancel + the
	// out-of-band stopChan close above cause processVerify to exit immediately
	// (one of two select arms fires), then the verifier's deferred
	// CancelSubscribe + 4-topic teardown finishes synchronously before
	// Verify returns and the scheduler goroutine falls through. The 10s cap
	// is a safety net for a wedged tss-lib party.Start() that ignores both
	// signals — not the strict per-vault-timeout-style drain. We deliberately
	// don't wait the full per-vault timeout (180s by default) on shutdown;
	// if the in-flight
	// attempt doesn't honor the two stop signals within 10s, racing
	// tssIns.Stop() is the lesser evil vs. blocking operator shutdown.
	// The per-vault defer recover() in runOneAttempt catches any panic
	// resulting from the race.
	select {
	case <-keyverifyDone:
	case <-time.After(10 * time.Second):
		log.Warn().Msg("keyverify scheduler did not exit within 10s; proceeding with TSS shutdown anyway")
	}
	// stop go tss
	tssIns.Stop()
	if err = healthServer.Stop(); err != nil {
		log.Fatal().Err(err).Msg("fail to stop health server")
	}
}

func recoverLegacyKeysharesBeforeStartup(cfg config.Bifrost, bridge thorclient.ThorchainBridge) bool {
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	for attempt := 1; ; attempt++ {
		if err := btss.RecoverLegacyKeyShares(cfg, bridge); err == nil {
			return true
		} else {
			log.Error().Err(err).Int("attempt", attempt).Msg("legacy keyshare recovery incomplete; retrying before TSS startup")
		}
		timer := time.NewTimer(constants.ThorchainBlockTime)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false
		}
	}
}

func superviseFrostKeyshareRecovery(ctx context.Context, cfg config.Bifrost, bridge thorclient.ThorchainBridge) {
	for attempt := 1; ; attempt++ {
		if err := btss.RecoverFrostKeyShares(cfg, bridge); err == nil {
			return
		} else {
			log.Error().Err(err).Int("attempt", attempt).Msg("XMR FROST keyshare recovery incomplete; healthy vaults remain available while retrying")
		}

		timer := time.NewTimer(constants.ThorchainBlockTime)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func initPrefix() {
	cosmosSDKConfg := cosmos.GetConfig()
	cosmosSDKConfg.SetBech32PrefixForAccount(cmd.Bech32PrefixAccAddr, cmd.Bech32PrefixAccPub)
	cosmosSDKConfg.Seal()
}

func initLog(level string, pretty bool) {
	l, err := zerolog.ParseLevel(level)
	if err != nil {
		log.Warn().Msgf("%s is not a valid log-level, falling back to 'info'", level)
	}
	var out io.Writer = os.Stdout
	if pretty {
		out = zerolog.ConsoleWriter{Out: os.Stdout}
	}
	zerolog.SetGlobalLevel(l)
	log.Logger = log.Output(out).With().Caller().Str("service", serverIdentity).Logger()

	logLevel := golog.LevelInfo
	switch l {
	case zerolog.DebugLevel:
		logLevel = golog.LevelDebug
	case zerolog.InfoLevel:
		logLevel = golog.LevelInfo
	case zerolog.ErrorLevel:
		logLevel = golog.LevelError
	case zerolog.FatalLevel:
		logLevel = golog.LevelFatal
	case zerolog.PanicLevel:
		logLevel = golog.LevelPanic
	}
	golog.SetAllLoggers(logLevel)
	if err = golog.SetLogLevel("tss-lib", level); err != nil {
		log.Fatal().Err(err).Msg("fail to set tss-lib loglevel")
	}
}
