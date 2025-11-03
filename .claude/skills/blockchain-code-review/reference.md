# Blockchain Code Review - Extended Reference

## Real-World Examples from Cosmos SDK

### Example 1: authz Module Time Bug

The authz module had a consensus failure because it used local clock times instead of block header timestamps. Local time is subjective to each node, causing different nodes to make different authorization decisions.

**Fix:** Always use `ctx.BlockTime()` or `ctx.BlockHeader().Time` instead of `time.Now()`.

### Example 2: Map Iteration Consensus Failure

A production chain halted because module logic iterated over a map to process items. Different nodes processed items in different orders, leading to different state roots.

**Fix:** Extract keys to slice, sort them, then iterate in sorted order.

## Detailed Pattern Examples

### Non-Deterministic Patterns

#### Bad: Floating Point in Rewards Calculation

```go
// DON'T DO THIS - Consensus breaking
func CalculateRewards(totalRewards, totalStake int64) int64 {
    ratio := float64(totalRewards) / float64(totalStake)
    return int64(ratio * 1000000)
}
```

**Why it's bad:**

- Different CPUs may round floating point differently
- Results in different reward amounts on different nodes
- Consensus failure and chain halt

**Fix:**

```go
// DO THIS - Use SDK decimal types
func CalculateRewards(totalRewards, totalStake sdk.Int) sdk.Int {
    if totalStake.IsZero() {
        return sdk.ZeroInt()
    }
    ratio := sdk.NewDecFromInt(totalRewards).QuoInt(totalStake)
    return ratio.MulInt64(1000000).TruncateInt()
}
```

#### Bad: Map Iteration for Processing

```go
// DON'T DO THIS - Non-deterministic
func ProcessPendingTxs(txMap map[string]Transaction) {
    for hash, tx := range txMap {
        if err := processTx(tx); err != nil {
            deleteTx(hash)
        }
    }
}
```

**Why it's bad:**

- Map iteration order is randomized in Go
- Different nodes process transactions in different orders
- State divergence and consensus failure

**Fix:**

```go
// DO THIS - Sort keys first
func ProcessPendingTxs(txMap map[string]Transaction) error {
    // Extract keys
    hashes := make([]string, 0, len(txMap))
    for hash := range txMap {
        hashes = append(hashes, hash)
    }

    // Sort for deterministic order
    sort.Strings(hashes)

    // Process in sorted order
    for _, hash := range hashes {
        tx := txMap[hash]
        if err := processTx(tx); err != nil {
            deleteTx(hash)
        }
    }
    return nil
}
```

#### Bad: Using time.Now() for Expiration

```go
// DON'T DO THIS - Non-deterministic
func (k Keeper) ExpireOldProposals(ctx sdk.Context) {
    currentTime := time.Now()
    for _, proposal := range k.GetAllProposals(ctx) {
        if proposal.Deadline.Before(currentTime) {
            k.DeleteProposal(ctx, proposal.ID)
        }
    }
}
```

**Why it's bad:**

- Each node has slightly different system time
- Different nodes expire different proposals
- Consensus failure

**Fix:**

```go
// DO THIS - Use block time
func (k Keeper) ExpireOldProposals(ctx sdk.Context) {
    blockTime := ctx.BlockTime()
    proposals := k.GetAllProposals(ctx)

    // Sort by ID for deterministic order
    sort.Slice(proposals, func(i, j int) bool {
        return proposals[i].ID < proposals[j].ID
    })

    for _, proposal := range proposals {
        if proposal.Deadline.Before(blockTime) {
            k.DeleteProposal(ctx, proposal.ID)
        }
    }
}
```

### Division Patterns

#### Bad: Division Without Zero Check

```go
// DON'T DO THIS - Can panic
func CalculateShare(amount, total sdk.Uint) sdk.Uint {
    return amount.Mul(sdk.NewUint(100)).Quo(total)
}
```

**Why it's bad:**

- If total is zero, this panics
- Panic in consensus code halts the chain
- No recovery in BeginBlocker/EndBlocker

**Fix:**

```go
// DO THIS - Check for zero
func CalculateShare(amount, total sdk.Uint) (sdk.Uint, error) {
    if total.IsZero() {
        return sdk.ZeroUint(), errors.New("total cannot be zero")
    }
    return amount.Mul(sdk.NewUint(100)).Quo(total), nil
}
```

#### Bad: Integer Overflow

```go
// DON'T DO THIS - Can overflow
func CalculateCompound(principal int64, rate int64, periods int64) int64 {
    result := principal
    for i := int64(0); i < periods; i++ {
        result = result * rate / 10000
    }
    return result
}
```

**Why it's bad:**

- Multiplication can silently overflow
- Results in incorrect calculations
- Can lead to loss of funds

**Fix:**

```go
// DO THIS - Use SDK types with overflow protection
func CalculateCompound(principal, rate sdk.Int, periods int64) sdk.Int {
    result := principal
    denominator := sdk.NewInt(10000)

    for i := int64(0); i < periods; i++ {
        result = result.Mul(rate).Quo(denominator)
    }
    return result
}
```

### WASM API Patterns

#### Bad: Changing WASM API Response Structure

```go
// BEFORE - In api/types/query.proto
message QueryPoolResponse {
    string asset = 1;
    string balance_rune = 2;
    string balance_asset = 3;
    string status = 4;
}

// AFTER - DON'T DO THIS if endpoint is in wasmAcceptedQueries
message QueryPoolResponse {
    string asset = 1;
    int64 balance_rune = 2;  // Changed from string to int64
    int64 balance_asset = 3;  // Changed from string to int64
    string status = 4;
}
```

**Why it's bad:**

- Deployed WASM contracts expect string types
- Type change breaks contract deserialization
- Consensus failure when WASM contract executes

**Fix:**

```go
// DO THIS - Deprecate old fields, add new ones
message QueryPoolResponse {
    string asset = 1;
    string balance_rune = 2 [(deprecated) = true];
    string balance_asset = 3 [(deprecated) = true];
    string status = 4;
    int64 balance_rune_int = 5;  // New field with different number
    int64 balance_asset_int = 6;  // New field with different number
}
```

#### Bad: Adding New WASM Query Without Migration Plan

```go
// In app/wasm.go - DON'T ADD without careful consideration
var wasmAcceptedQueries = wasmkeeper.AcceptedQueries{
    // ... existing queries ...
    "/types.Query/NewRiskyQuery": &apitypes.QueryNewRiskyQueryResponse{},  // Risky!
}
```

**Why it's bad:**

- Once added, this becomes part of consensus
- Changing the response structure is breaking
- Need to maintain backward compatibility forever

**Fix:**

- Thoroughly design the API before adding
- Consider versioning strategy
- Document that this is consensus-critical
- Add comprehensive tests
- Consider if query is truly needed for WASM

## BeginBlocker/EndBlocker Pitfalls

### Bad: Unhandled Panic in EndBlocker

```go
// DON'T DO THIS
func EndBlocker(ctx sdk.Context, k keeper.Keeper) []abci.ValidatorUpdate {
    // This can panic if slice is empty
    validators := k.GetAllValidators(ctx)
    topValidator := validators[0]  // Panic if empty!

    return k.ProcessValidatorUpdates(ctx, topValidator)
}
```

**Why it's bad:**

- Panics in EndBlocker halt the entire chain
- No way to recover from this state
- Requires manual intervention and potentially a hard fork

**Fix:**

```go
// DO THIS - Validate before accessing
func EndBlocker(ctx sdk.Context, k keeper.Keeper) []abci.ValidatorUpdate {
    validators := k.GetAllValidators(ctx)

    if len(validators) == 0 {
        ctx.Logger().Error("no validators found")
        return []abci.ValidatorUpdate{}
    }

    topValidator := validators[0]
    return k.ProcessValidatorUpdates(ctx, topValidator)
}
```

### Bad: Map Iteration in BeginBlocker

```go
// DON'T DO THIS
func BeginBlocker(ctx sdk.Context, k keeper.Keeper) {
    rewardsMap := k.GetPendingRewards(ctx)

    // Non-deterministic iteration!
    for addr, amount := range rewardsMap {
        k.DistributeReward(ctx, addr, amount)
    }
}
```

**Fix:**

```go
// DO THIS
func BeginBlocker(ctx sdk.Context, k keeper.Keeper) {
    rewardsMap := k.GetPendingRewards(ctx)

    // Get addresses and sort
    addresses := make([]string, 0, len(rewardsMap))
    for addr := range rewardsMap {
        addresses = append(addresses, addr)
    }
    sort.Strings(addresses)

    // Distribute in deterministic order
    for _, addr := range addresses {
        k.DistributeReward(ctx, addr, rewardsMap[addr])
    }
}
```

## Goroutine Anti-Patterns

### Bad: Goroutine in Handler

```go
// DON'T DO THIS - Non-deterministic
func (h Handler) HandleSwap(ctx sdk.Context, msg MsgSwap) error {
    // Launch goroutine to process
    go func() {
        h.keeper.ProcessSwap(ctx, msg)
    }()

    return nil
}
```

**Why it's bad:**

- Goroutines execute non-deterministically
- State changes may happen in different orders
- Race conditions possible

**Fix:**

```go
// DO THIS - Synchronous execution
func (h Handler) HandleSwap(ctx sdk.Context, msg MsgSwap) error {
    // Process synchronously and deterministically
    return h.keeper.ProcessSwap(ctx, msg)
}
```

## Safe Patterns

### Using KVStore for Deterministic Iteration

```go
// GOOD - KVStore provides deterministic ordering
func (k Keeper) ProcessAllPools(ctx sdk.Context) error {
    store := ctx.KVStore(k.storeKey)
    iterator := sdk.KVStorePrefixIterator(store, PoolPrefix)
    defer iterator.Close()

    // Iteration order is deterministic (lexicographic by key)
    for ; iterator.Valid(); iterator.Next() {
        var pool Pool
        k.cdc.MustUnmarshal(iterator.Value(), &pool)

        if err := k.ProcessPool(ctx, pool); err != nil {
            return err
        }
    }

    return nil
}
```

### Using Block Hash for Pseudo-Randomness

```go
// GOOD - Deterministic pseudo-random using block hash
func (k Keeper) SelectRandomValidator(ctx sdk.Context, validators []Validator) Validator {
    // Use block hash as seed - same on all nodes
    blockHash := ctx.BlockHeader().Hash()
    seed := binary.BigEndian.Uint64(blockHash)

    // Deterministic selection
    index := seed % uint64(len(validators))
    return validators[index]
}
```

## Testing for Determinism

### Test: Same Input, Same Output

```go
func TestDeterministicExecution(t *testing.T) {
    // Run same operation multiple times
    results := make([]string, 10)

    for i := 0; i < 10; i++ {
        ctx, keeper := setupTest()

        // Execute operation
        keeper.ProcessRewards(ctx)

        // Get state hash
        results[i] = ctx.MultiStore().GetCommitID().Hash.String()
    }

    // All results should be identical
    for i := 1; i < len(results); i++ {
        require.Equal(t, results[0], results[i], "non-deterministic execution detected")
    }
}
```

## Search Patterns for grep/ripgrep

### Finding Float Usage

```bash
# Search for float types
rg "float32|float64" --type go

# Search for math.Float* functions
rg "math\.Float" --type go

# Search for floating point division
rg "\d+\.\d+" --type go
```

### Finding Map Iteration

```bash
# Basic map iteration
rg "for .* range .*(map\[|Map)" --type go

# Variations
rg "for [a-zA-Z0-9_]+, [a-zA-Z0-9_]+ := range" --type go
```

### Finding Division

```bash
# SDK division methods
rg "\.Quo\(|\.Div\(" --type go

# Regular division
rg " / " --type go
```

### Finding Time Usage

```bash
# time.Now() calls
rg "time\.Now\(\)" --type go

# time.Since() calls
rg "time\.Since\(" --type go

# Should use block time instead
rg "ctx\.BlockTime\(\)|ctx\.BlockHeader\(\)\.Time" --type go
```

### Finding Goroutines

```bash
# Goroutine launches
rg "go func|go [a-zA-Z]" --type go

# Concurrency primitives
rg "sync\.Mutex|sync\.WaitGroup|sync\.RWMutex" --type go

# Channels
rg "make\(chan |chan [a-zA-Z]" --type go
```

### Finding Random

```bash
# Random usage
rg "rand\." --type go

# Crypto random (usually OK for non-consensus)
rg "crypto/rand" --type go
```

### Finding Panics

```bash
# Explicit panics
rg "panic\(" --type go

# Array/slice indexing (potential panic)
rg "\[[a-zA-Z0-9_]+\](?! *=)" --type go

# Type assertions without ok check
rg "\.\([a-zA-Z]+\)(?!\s*,)" --type go
```

## CI/CD Integration

### Check CI Status Before Review

```bash
# Get CI status
glab ci view

# Get specific pipeline
glab ci view <pipeline-id>

# List recent pipelines
glab ci list

# Check if tests passed
glab ci view --output json | jq '.status'
```

### Automated Checks

Consider adding to CI:

- Static analysis with `gosec` or `staticcheck`
- Custom linter for consensus patterns
- Determinism tests (run same code multiple times)
- Regression test suite

## Quick Reference Card

| Pattern                      | Severity | Search String      | Safe Alternative      |
| ---------------------------- | -------- | ------------------ | --------------------- |
| `float32`, `float64`         | CRITICAL | `float32\|float64` | `sdk.Dec`, `sdk.Int`  |
| `for range map`              | CRITICAL | `for.*range.*map`  | Sort keys first       |
| `time.Now()`                 | CRITICAL | `time\.Now\(\)`    | `ctx.BlockTime()`     |
| `rand.`                      | CRITICAL | `rand\.`           | Block hash seed       |
| `go func`                    | HIGH     | `go func`          | Synchronous execution |
| `.Quo(` without check        | HIGH     | `\.Quo\(`          | Check for zero first  |
| Panic in BeginBlock/EndBlock | CRITICAL | Manual review      | Validate all inputs   |
| WASM API changes             | CRITICAL | `app/wasm.go`      | Versioning strategy   |
| Map in BeginBlocker          | CRITICAL | Manual review      | KVStore iterator      |

## Additional Resources

- Cosmos SDK Security Best Practices: https://docs.cosmos.network/
- THORChain Architecture Docs: `/docs/architecture/`
- Go Map Iteration Determinism: https://go.dev/blog/maps
- Static Analysis Tools: CodeQL, gosec, staticcheck
- Tendermint Consensus: https://docs.tendermint.com/

## Continuous Improvement

This reference should be updated as new patterns are discovered. When consensus failures occur in production:

1. Document the root cause
2. Add the pattern to this reference
3. Update search patterns
4. Add to automated checks if possible
5. Share with team

## Version History

- 2025-01: Initial version based on Cosmos SDK best practices and THORChain specifics
