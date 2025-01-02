package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/tools/thorscan"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Scan Info
////////////////////////////////////////////////////////////////////////////////////////

func ScanInfo(block *thorscan.BlockResponse) {
	Version(block)
	Churn(block)
	SetNodeMimir(block)
	SetMimir(block)
	KeygenFailure(block)
	TORAnchorDrift(block)
	UpgradeProposalAndApproval(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// Version
////////////////////////////////////////////////////////////////////////////////////////

func Version(block *thorscan.BlockResponse) {
	for _, event := range block.BeginBlockEvents {
		if event["type"] == types.VersionEventType {
			msg := fmt.Sprintf("`[%d]` Network Version Upgraded: `%s`", block.Header.Height, event["version"])
			Notify(config.Notifications.Info, msg, nil, false, nil)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Set Mimir
////////////////////////////////////////////////////////////////////////////////////////

func SetMimir(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] != types.SetMimirEventType {
				continue
			}

			// if the transaction does not contain mimir message it auto triggered
			source := "auto"

		msgs: // determine if this is an admin or node mimir
			for _, msg := range tx.Tx.GetMsgs() {
				if msgMimir, ok := msg.(*thorchain.MsgMimir); ok {
					signer := msgMimir.Signer.String()
					for _, admin := range thorchain.ADMINS {
						if admin == signer {
							source = "admin"
							break msgs
						}
					}

					source = "node"
				}
			}

			var msg string
			switch event["key"] {
			case "NODEPAUSECHAINGLOBAL":
				msg = formatNodePauseMessage(block.Header.Height, tx, event)
			default:
				msg = formatMimirMessage(block.Header.Height, source, event["key"], event["value"])
			}

			Notify(config.Notifications.Info, msg, nil, false, nil)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Set Node Mimir
////////////////////////////////////////////////////////////////////////////////////////

func SetNodeMimir(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] == types.SetNodeMimirEventType {
				msg := formatNodeMimirMessage(block.Header.Height, event["address"], event["key"], event["value"])
				Notify(config.Notifications.Info, "", []string{msg}, false, nil)
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Keygen Failure
////////////////////////////////////////////////////////////////////////////////////////

func KeygenFailure(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}

		for _, event := range tx.Result.Events {
			if event["type"] != types.TSSKeygenFailure {
				continue
			}

			// get nodes at keygen height
			heightStr := event["height"]
			height, err := strconv.ParseInt(heightStr, 10, 64)
			if err != nil {
				log.Panic().Err(err).Str("height", heightStr).Msg("failed to parse keygen height")
			}
			nodes := []openapi.Node{}
			err = ThornodeCachedRetryGet("thorchain/nodes", height, &nodes)
			if err != nil {
				log.Panic().Err(err).Msg("failed to get nodes")
			}

			// gather pubkey and operator mappings
			pubToAddr := make(map[string]string)
			addrToOperator := make(map[string]string)
			for _, node := range nodes {
				if node.PubKeySet.Secp256k1 == nil {
					continue
				}
				pubToAddr[*node.PubKeySet.Secp256k1] = node.NodeAddress
				addrToOperator[node.NodeAddress] = node.NodeOperatorAddress
			}

			// gather blame nodes
			blames := []string{}
			blameNodes := make(map[string]bool)
			for _, blame := range strings.Split(event["blame"], ",") {
				blame = strings.TrimSpace(blame)
				if blame == "" {
					continue
				}
				blames = append(blames, blame)
				blameNodes[blame] = true
			}

			// find tsspool message
			var msgTssPool *thorchain.MsgTssPool
			found := false
			for _, msg := range tx.Tx.GetMsgs() {
				if _, ok := msg.(*thorchain.MsgTssPool); ok {
					msgTssPool, _ = msg.(*thorchain.MsgTssPool)
					found = true
				}
			}
			if !found {
				log.Panic().Msg("failed to find tsspool message for keygen failure event")
			}

			// gather all members
			members := make(map[string]bool)
			for _, pk := range msgTssPool.PubKeys {
				members[pubToAddr[pk]] = true
			}

			// gather unblamed members
			others := make(map[string]bool)
			for member := range members {
				if blameNodes[member] {
					continue
				}
				others[member] = true
			}

			// build the blame and others strings
			blameStrs := []string{}
			for _, blame := range blames {
				blameStr := fmt.Sprintf(
					"`%s:%s`",
					addrToOperator[blame][len(addrToOperator[blame])-4:], blame[len(blame)-4:],
				)
				blameStrs = append(blameStrs, blameStr)
			}
			othersStrs := []string{}
			for other := range others {
				othersStrs = append(othersStrs, other[len(other)-4:])
			}

			// build the fields
			fields := NewOrderedMap()
			fields.Set("Keygen Height", event["height"])
			fields.Set("Reason", event["reason"])
			fields.Set("Blame", strings.Join(blameStrs, ", "))
			fields.Set("Others", fmt.Sprintf("`%s`", strings.Join(othersStrs, ", ")))

			// notify
			title := fmt.Sprintf("`[%d]` Keygen Failure", block.Header.Height)
			Notify(config.Notifications.Info, title, nil, false, fields)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Churn
////////////////////////////////////////////////////////////////////////////////////////

type ChurnState int64

const (
	ChurnStateComplete ChurnState = iota
	ChurnStateKeygen
	ChurnStateMigrating
)

type ChurnInfo struct {
	State           ChurnState                 `json:"state"`
	KeyshareBackups map[string]map[string]bool `json:"keyshare_backups"`
}

func Churn(block *thorscan.BlockResponse) {
	// get the current state
	info := ChurnInfo{}
	err := Load("churn", &info)
	if err != nil {
		log.Debug().Err(err).Msg("failed to load churn state")
	}

	// track keyshare backups
	updated := false
	for _, tx := range block.Txs {
		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}

		for _, msg := range tx.Tx.GetMsgs() {
			msgTssPool, ok := msg.(*thorchain.MsgTssPool)
			if !ok {
				continue
			}

			// track keyshare backups
			if len(msgTssPool.KeysharesBackup) > 1 {
				pk := string(msgTssPool.PoolPubKey)
				if info.KeyshareBackups == nil {
					info.KeyshareBackups = make(map[string]map[string]bool)
				}
				if info.KeyshareBackups[pk] == nil {
					info.KeyshareBackups[pk] = make(map[string]bool)
				}
				updated = true
				info.KeyshareBackups[pk][msgTssPool.Signer.String()] = true
			}
		}
	}
	if updated {
		err = Store("churn", info)
		if err != nil {
			log.Panic().Err(err).Msg("failed to save churn state")
		}
	}

	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			switch event["type"] {
			case types.TSSKeygenMetricEventType: // check for keygen started
				if info.State == ChurnStateComplete {
					info.State = ChurnStateKeygen
					err = Store("churn", info)
					if err != nil {
						log.Panic().Err(err).Msg("failed to save churn state")
					}
					title := fmt.Sprintf("`[%d]` Keygen Started", block.Header.Height)
					Notify(config.Notifications.Info, title, nil, false, nil)
				}
			case thorchain.EventTypeActiveVault: // check for active vault (keygens complete)
				if info.State == ChurnStateKeygen {
					info.State = ChurnStateMigrating
					err = Store("churn", info)
					if err != nil {
						log.Panic().Err(err).Msg("failed to save churn state")
					}
					notifyChurnStarted(block.Header.Height, info.KeyshareBackups)
				}
			default:
				continue
			}
		}
	}

	// if migrating, check for completion on every block
	if info.State == ChurnStateMigrating && !vaultsMigrating(block.Header.Height) {
		// reset churn info for next churn
		info.State = ChurnStateComplete
		info.KeyshareBackups = make(map[string]map[string]bool)

		err = Store("churn", info)
		if err != nil {
			log.Panic().Err(err).Msg("failed to save churn state")
		}
		title := fmt.Sprintf("`[%d]` Churn Complete", block.Header.Height)
		Notify(config.Notifications.Info, title, nil, false, nil)
	}
}

func vaultsMigrating(height int64) bool {
	network := openapi.NetworkResponse{}
	err := ThornodeCachedRetryGet("thorchain/network", height, &network)
	if err != nil {
		log.Panic().Err(err).Msg("failed to get network")
	}
	return network.VaultsMigrating
}

func notifyChurnStarted(height int64, keyshareBackups map[string]map[string]bool) {
	// get nodes at current and previous height
	oldNodes := []openapi.Node{}
	newNodes := []openapi.Node{}
	err := ThornodeCachedRetryGet("thorchain/nodes", height-1, &oldNodes)
	if err != nil {
		log.Panic().Err(err).Int64("height", height-1).Msg("failed to get old nodes")
	}
	err = ThornodeCachedRetryGet("thorchain/nodes", height, &newNodes)
	if err != nil {
		log.Panic().Err(err).Int64("height", height).Msg("failed to get new nodes")
	}

	// determine the nodes that were removed
	oldActive := make(map[string]openapi.Node)
	newActive := make(map[string]openapi.Node)
	for _, oldNode := range oldNodes {
		if oldNode.Status != types.NodeStatus_Active.String() {
			continue
		}
		oldActive[oldNode.NodeAddress] = oldNode
	}
	for _, newNode := range newNodes {
		if newNode.Status != types.NodeStatus_Active.String() {
			continue
		}
		newActive[newNode.NodeAddress] = newNode
	}

	// determine the nodes that were added
	added := []openapi.Node{}
	for _, newNode := range newActive {
		if _, ok := oldActive[newNode.NodeAddress]; !ok {
			added = append(added, newNode)
		}
	}

	// determine the nodes that were removed
	left := []openapi.Node{}
	removed := []openapi.Node{}
	for _, oldNode := range oldActive {
		if _, ok := newActive[oldNode.NodeAddress]; ok {
			continue
		}
		if oldNode.LeaveHeight > 0 {
			if oldNode.LeaveHeight < height {
				left = append(left, oldNode)
			} else {
				removed = append(removed, oldNode)
			}
		}
	}

	// standby nodes
	standbyNodes := []string{}

	// find worst removed
	if len(removed) > 0 {
		worstIdx := 0
		for i, node := range removed {
			if node.SlashPoints > removed[worstIdx].SlashPoints {
				worstIdx = i
			}
		}
		worst := removed[worstIdx]
		removed = append(removed[:worstIdx], removed[worstIdx+1:]...)
		standbyNodes = append(standbyNodes, fmt.Sprintf("`%s` (worst)", worst.NodeAddress[len(worst.NodeAddress)-4:]))
	}

	// find lowest bond
	if len(removed) > 0 {
		lowestIdx := 0
		for i, node := range removed {
			if cosmos.NewUintFromString(node.TotalBond).LT(cosmos.NewUintFromString(removed[lowestIdx].TotalBond)) {
				lowestIdx = i
			}
		}
		lowest := removed[lowestIdx]
		removed = append(removed[:lowestIdx], removed[lowestIdx+1:]...)
		standbyNodes = append(standbyNodes, fmt.Sprintf("`%s` (lowest bond)", lowest.NodeAddress[len(lowest.NodeAddress)-4:]))
	}

	// find oldest removed
	if len(removed) > 0 {
		oldestIdx := 0
		for i, node := range removed {
			if node.ActiveBlockHeight < removed[oldestIdx].ActiveBlockHeight {
				oldestIdx = i
			}
		}
		oldest := removed[oldestIdx]
		removed = append(removed[:oldestIdx], removed[oldestIdx+1:]...)
		standbyNodes = append(standbyNodes, fmt.Sprintf("`%s` (oldest)", oldest.NodeAddress[len(oldest.NodeAddress)-4:]))
	}

	title := fmt.Sprintf("[%d] Churn Started", height)

	// compute the keyshare backups counts for new vault members
	lines := []string{"_Keyshare Backups_"}
	vaults := []openapi.Vault{}
	err = ThornodeCachedRetryGet("thorchain/vaults/asgard", height, &vaults)
	if err != nil {
		log.Panic().Err(err).Msg("failed to get vaults")
	}
	for _, vault := range vaults {
		if vault.Status != types.VaultStatus_ActiveVault.String() {
			continue
		}
		pk := *vault.PubKey
		lines = append(lines,
			fmt.Sprintf(
				"`%s`: %d/%d (%.2f%%)",
				pk[len(pk)-4:], len(keyshareBackups[pk]), len(vault.Membership),
				100*float64(len(keyshareBackups[pk]))/float64(len(vault.Membership)),
			),
		)
	}
	lines = append(lines, "")

	fields := NewOrderedMap()

	// active nodes
	if len(added) > 0 {
		activeNodes := []string{}
		for _, node := range added {
			activeNodes = append(activeNodes, fmt.Sprintf("`%s`", node.NodeAddress[len(node.NodeAddress)-4:]))
		}
		fields.Set("Active", strings.Join(activeNodes, ", "))
	}

	// standby nodes
	for _, node := range left {
		standbyNodes = append(standbyNodes, fmt.Sprintf("`%s` (leave)", node.NodeAddress[len(node.NodeAddress)-4:]))
	}
	for _, node := range removed {
		standbyNodes = append(standbyNodes, fmt.Sprintf("`%s`", node.NodeAddress[len(node.NodeAddress)-4:]))
	}
	fields.Set("Standby", strings.Join(standbyNodes, ", "))

	Notify(config.Notifications.Info, title, lines, false, fields)
}

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

func formatNodePauseMessage(height int64, tx thorscan.BlockTx, event map[string]string) string {
	legacyMsg, ok := tx.Tx.GetMsgs()[0].(sdk.LegacyMsg)
	if !ok {
		log.Panic().Msg("failed to cast to legacy message")
	}
	signer := legacyMsg.GetSigners()[0].String()
	pauseHeight, err := strconv.ParseInt(event["value"], 10, 64)
	if err != nil {
		log.Panic().Str("value", event["value"]).Err(err).Msg("failed to parse pause height")
	}

	action := fmt.Sprintf("**Node `%s` Unpaused**", signer[len(signer)-4:])
	if height <= pauseHeight {
		action = fmt.Sprintf("**Node `%s` Paused**: %d blocks", signer[len(signer)-4:], pauseHeight-height)
	}

	return fmt.Sprintf("`[%d]` %s", height, action)
}

func formatMimirMessage(height int64, source, key, value string) string {
	// get value at previous height
	mimirs := make(map[string]int64)
	err := ThornodeCachedRetryGet("thorchain/mimir", height-1, &mimirs)
	if err != nil {
		log.Panic().Int64("height", height-1).Err(err).Msg("failed to get mimirs")
	}

	if previous, ok := mimirs[key]; ok {
		return fmt.Sprintf("`[%d]` **%s**: %d -> %s (%s)", height, key, previous, value, source)
	}
	return fmt.Sprintf("`[%d]` **%s**: %s (%s)", height, key, value, source)
}

func formatNodeMimirMessage(height int64, node, key, value string) string {
	// convert value to int64
	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		log.Panic().Err(err).Str("value", value).Msg("failed to parse value")
	}

	// get all active nodes at current height
	nodes := []openapi.Node{}
	err = ThornodeCachedRetryGet("thorchain/nodes", height, &nodes)
	if err != nil {
		log.Panic().Int64("height", height).Err(err).Msg("failed to get active nodes")
	}
	activeNodes := make(map[string]bool)
	for _, node := range nodes {
		if node.Status == types.NodeStatus_Active.String() {
			activeNodes[node.NodeAddress] = true
		}
	}

	// get value at previous height
	mimirs := openapi.MimirNodesResponse{}
	err = ThornodeCachedRetryGet("thorchain/mimir/nodes_all", height-1, &mimirs)
	if err != nil {
		log.Panic().Int64("height", height-1).Err(err).Msg("failed to get node mimirs")
	}

	var previous *int64
	votes := make(map[int64]int64)
	for _, mimir := range mimirs.Mimirs {
		// skip votes that are not this key
		if *mimir.Key != key {
			continue
		}

		// TODO: fix in thornode - missing value in response is "0"
		value := int64(0)
		if mimir.Value != nil {
			value = *mimir.Value
		}

		// skip votes from non-active nodes
		if _, ok := activeNodes[*mimir.Signer]; !ok {
			continue
		}

		// see if there was a previous value
		if *mimir.Signer == node {
			previous = &value
			continue
		}

		// count the votes
		if _, ok := votes[value]; !ok {
			votes[value] = 1
		} else {
			votes[value]++
		}
	}

	// add the new vote
	votes[valueInt]++

	// compute the percent voted for the node vote value
	votePercent := 100 * float64(votes[valueInt]) / float64(len(activeNodes))

	// base message
	msg := fmt.Sprintf(
		"`[%d]` Node `%s` Vote - **%s**=%d (%.2f%%)",
		height, node[len(node)-4:], key, valueInt, votePercent,
	)
	if previous != nil {
		msg = fmt.Sprintf(
			"`[%d]` Node `%s` Vote - **%s**=%d (_change from `%d`_) (%.2f%%)",
			height, node[len(node)-4:], key, valueInt, *previous, votePercent,
		)
	}

	// add the votes and validator count
	for vote, count := range votes {
		msg += fmt.Sprintf(" | `%d`: %d votes", vote, count)
	}
	msg += fmt.Sprintf(" | Validators: %d", len(activeNodes))

	return msg
}

////////////////////////////////////////////////////////////////////////////////////////
// TOR Anchor Drift
////////////////////////////////////////////////////////////////////////////////////////

func TORAnchorDrift(block *thorscan.BlockResponse) {
	if block.Header.Height%config.TORAnchorCheckBlocks != 0 {
		return
	}

	// get mimirs
	mimirs := map[string]int64{}
	err := ThornodeCachedRetryGet("thorchain/mimir", block.Header.Height, &mimirs)
	if err != nil {
		log.Panic().Err(err).Msg("failed to get mimirs")
	}

	// get pools
	pools := []openapi.Pool{}
	err = ThornodeCachedRetryGet("thorchain/pools", block.Header.Height, &pools)
	if err != nil {
		log.Panic().Err(err).Msg("failed to get pools")
	}

	// find all TOR pools
	torPools := []openapi.Pool{}
	minPrice := cosmos.NewUint(common.One)
	maxPrice := cosmos.NewUint(common.One)
	for _, pool := range pools {
		var asset common.Asset
		asset, err = common.NewAsset(pool.Asset)
		if err != nil {
			log.Panic().Err(err).Msg("failed to parse pool asset")
		}
		if mimirs[fmt.Sprintf("TORANCHOR-%s", asset.MimirString())] > 0 {
			price := cosmos.NewUintFromString(pool.AssetTorPrice)
			if price.LT(minPrice) {
				minPrice = price
			}
			if price.GT(maxPrice) {
				maxPrice = price
			}
			torPools = append(torPools, pool)
		}
	}

	// skip if not over the drift threshold
	driftBPS := maxPrice.Sub(minPrice).MulUint64(constants.MaxBasisPts).Quo(maxPrice)
	if driftBPS.LT(cosmos.NewUint(config.Thresholds.TORAnchorDriftBasisPoints)) {
		return
	}

	// sort tor pools by price
	sort.Slice(torPools, func(i, j int) bool {
		iPrice := cosmos.NewUintFromString(torPools[i].AssetTorPrice)
		jPrice := cosmos.NewUintFromString(torPools[j].AssetTorPrice)
		return iPrice.LT(jPrice)
	})

	// build notification
	fields := NewOrderedMap()
	maxFieldLenth := 0
	for _, pool := range torPools {
		shortAsset := strings.Split(pool.Asset, "-")[0]
		if len(shortAsset) > maxFieldLenth {
			maxFieldLenth = len(shortAsset)
		}
	}
	for _, pool := range torPools {
		shortAsset := strings.Split(pool.Asset, "-")[0]
		if len(shortAsset) < maxFieldLenth {
			shortAsset = strings.Repeat(" ", maxFieldLenth-len(shortAsset)) + shortAsset
		}
		price := float64(cosmos.NewUintFromString(pool.AssetTorPrice).Uint64()) / common.One
		fields.Set(shortAsset, FormatUSD(price))
	}

	title := fmt.Sprintf("`[%d]` TOR Anchor Drift (%.02f%%)", block.Header.Height, float64(driftBPS.Uint64())/100)
	Notify(config.Notifications.Info, title, nil, false, fields)
}

////////////////////////////////////////////////////////////////////////////////////////
// Upgrade Proposal and Approval
////////////////////////////////////////////////////////////////////////////////////////

func UpgradeProposalAndApproval(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {

			fields := NewOrderedMap()
			tag := false
			var title string
			switch event["type"] {
			case "propose_upgrade":
				title = fmt.Sprintf("`[%d]` Upgrade Proposed: `%s`", block.Header.Height, event["name"])
				tag = true
				fields.Set("Height", fmt.Sprintf("`%s`", event["height"]))
				fields.Set("Info", event["info"])
			case "approve_upgrade":
				title = fmt.Sprintf("`[%d]` Upgrade Approved: `%s`", block.Header.Height, event["name"])

				// fetch proposal stats
				proposal := openapi.UpgradeProposal{}
				path := fmt.Sprintf("thorchain/upgrade_proposal/%s", event["name"])
				err := ThornodeCachedRetryGet(path, block.Header.Height, &proposal)
				if err != nil {
					log.Panic().Err(err).Msg("failed to get upgrade proposal")
				}
				if proposal.ApprovedPercent == nil || proposal.ValidatorsToQuorum == nil {
					break
				}

				// add progress fields
				percent, err := strconv.ParseFloat(*proposal.ApprovedPercent, 64)
				if err != nil {
					log.Panic().Err(err).Msg("failed to parse approved percent")
				}
				fields.Set("Approval Percent", fmt.Sprintf("`%.2f%%`", percent))
				fields.Set("Remaining Votes Required", fmt.Sprintf("`%d`", *proposal.ValidatorsToQuorum))
			default:
				continue
			}

			fields.Set("Node", fmt.Sprintf("`%s`", event["thor_address"]))
			Notify(config.Notifications.Info, title, nil, tag, fields)
		}
	}
}
