---
name: blockchain-code-review
description: Comprehensive code review for blockchain consensus safety, detecting non-deterministic code, float math, map iteration, division issues, and WASM API changes. Use when reviewing merge requests, branches, or conducting security audits of blockchain code.
allowed-tools: Read, Grep, Glob, Bash, Edit
---

# Blockchain Code Review Skill

This skill performs comprehensive security and consensus-safety reviews for blockchain code, with specific focus on THORChain/Cosmos SDK codebases. It identifies patterns that could cause consensus failures, chain halts, or security vulnerabilities.

## Overview

When activated, this skill will:

1. Fetch merge request details using GitLab CLI (`glab`)
2. Analyze code changes for consensus-breaking patterns
3. Check CI/CD pipeline status
4. Report findings with severity levels and file locations

## Usage

### Reviewing a Merge Request

```text
Review MR 1234
Review the current branch
```

### Getting MR Information

Use `glab` CLI to fetch MR details:

```bash
# Get MR details with JSON output
glab mr view [MR_ID] --output json

# Get MR diff
glab mr diff [MR_ID]

# Get CI status
glab ci view
```

## Consensus-Breaking Patterns to Check

### 1. CRITICAL: Non-Deterministic Operations

#### Float/Floating-Point Math

- **Pattern:** Use of `float32`, `float64`, or floating-point operations
- **Risk:** Different CPU architectures may round differently, causing consensus failure
- **Search:** Look for `float32`, `float64`, `math.Float`, division with floats
- **Exceptions:** Client-only code, display/UI logic, non-consensus calculations
- **Example:**

  ```go
  // BAD - Consensus breaking
  price := float64(amount) / float64(total)

  // GOOD - Use integer math with sdk.Dec or sdk.Uint
  price := sdk.NewDec(amount).Quo(sdk.NewDec(total))
  ```

#### Map Iteration

- **Pattern:** Iterating over Go maps with `for range`
- **Risk:** Go randomizes map iteration order, causing non-deterministic state
- **Search:** Look for `for.*range.*map`, `range.*\[.*\].*{`
- **Safe Usage:** Only iterate to extract keys for sorting, or to clear map
- **Fix:** Sort keys before iterating, or use deterministic data structures
- **Example:**

  ```go
  // BAD - Non-deterministic order
  for key, val := range myMap {
      processItem(key, val)
  }

  // GOOD - Sort keys first
  keys := make([]string, 0, len(myMap))
  for k := range myMap {
      keys = append(keys, k)
  }
  sort.Strings(keys)
  for _, k := range keys {
      processItem(k, myMap[k])
  }
  ```

#### Random Number Generation

- **Pattern:** Use of `rand.Random()`, `time.Now()` in state machine
- **Risk:** Different values on different nodes
- **Search:** `rand.`, `time.Now()`, `time.Since()`
- **Exception:** Using block header timestamp is safe
- **Example:**

  ```go
  // BAD - Non-deterministic
  seed := time.Now().UnixNano()
  random := rand.New(rand.NewSource(seed))

  // GOOD - Use block hash or deterministic seed
  seed := binary.BigEndian.Uint64(ctx.BlockHeader().Hash())
  ```

#### Goroutines and Concurrency

- **Pattern:** Goroutines, channels, sync primitives in state machine
- **Risk:** Thread pre-emption is non-deterministic
- **Search:** `go func`, `goroutine`, `sync.Mutex`, `sync.WaitGroup`, `chan `
- **Exception:** Client code, API handlers, background processes outside consensus
- **Review:** Any goroutine in keeper, handler, or module logic needs scrutiny

### 2. HIGH: Division and Arithmetic Issues

#### Division by Zero

- **Pattern:** Division operations without zero checks
- **Risk:** Panic causing chain halt
- **Search:** `/`, `.Quo(`, `.Div(`
- **Fix:** Always validate divisor is non-zero before division
- **Example:**

  ```go
  // BAD - Could panic
  result := amount.Quo(divisor)

  // GOOD - Check for zero
  if divisor.IsZero() {
      return errors.New("division by zero")
  }
  result := amount.Quo(divisor)
  ```

#### Integer Overflow/Underflow

- **Pattern:** Arithmetic on `int64`, `uint64` without bounds checking
- **Risk:** Silent overflow causing incorrect state
- **Search:** Large constants, multiplication, addition chains
- **Fix:** Use `sdk.Int`, `sdk.Uint` with overflow checking
- **Example:**

  ```go
  // BAD - Can overflow
  total := a * b * c

  // GOOD - Use SDK types with checks
  total := sdk.NewInt(a).Mul(sdk.NewInt(b)).Mul(sdk.NewInt(c))
  ```

### 3. HIGH: State Machine Determinism

#### BeginBlocker/EndBlocker Logic

- **Pattern:** Complex logic in `BeginBlocker` or `EndBlocker`
- **Risk:** These are common sources of non-determinism; unhandled panics here halt chain
- **Review:** Scrutinize any logic in these functions heavily
- **Check for:**
  - Map iterations
  - External calls
  - Potential panics
  - Time-based logic

#### Unhandled Panics

- **Pattern:** Operations that can panic without recovery
- **Risk:** Chain halt if panic occurs in BeginBlocker/EndBlocker
- **Search:** Array indexing, type assertions, nil dereferences
- **Fix:** Validate inputs, use safe operations, recover panics appropriately
- **Example:**

  ```go
  // BAD - Can panic if index out of bounds
  value := mySlice[index]

  // GOOD - Check bounds
  if index >= len(mySlice) {
      return errors.New("index out of bounds")
  }
  value := mySlice[index]
  ```

### 4. CRITICAL: WASM API Consensus Changes

#### WASM-Exposed API Endpoints

- **Location:** `app/wasm.go:15` (`wasmAcceptedQueries`)
- **Pattern:** Changes to API endpoints in the accepted queries list
- **Risk:** Modifying response types/fields breaks WASM contract execution consistency
- **Check for:**
  - Adding/removing endpoints from `wasmAcceptedQueries`
  - Changing field types in response structs
  - Adding/removing fields from response structs
  - Renaming fields in response structs
- **Current endpoints:**
  - `/types.Query/Network`
  - `/types.Query/LiquidityProvider`
  - `/types.Query/MimirWithKey`
  - `/types.Query/Node`
  - `/types.Query/OutboundFee`
  - `/types.Query/Pool`
  - `/types.Query/QuoteSwap`
  - `/types.Query/SecuredAsset`
  - `/types.Query/OraclePrice`
  - `/types.Query/SwapQueue`

### 5. MEDIUM: Serialization Issues

#### JSON Marshaling

- **Pattern:** Custom JSON marshaling without deterministic ordering
- **Risk:** Different JSON output on different nodes
- **Search:** `json.Marshal`, custom `MarshalJSON`
- **Fix:** Use protobuf or ensure deterministic field ordering

#### Protobuf Changes

- **Pattern:** Modifying `.proto` files
- **Risk:** Breaking changes cause deserialization failures
- **Check for:**
  - Changing field types
  - Removing fields
  - Changing field numbers
  - Reordering fields
- **Note:** After proto changes, must run `make generate`

### 6. MEDIUM: External Dependencies

#### Network/File System Access

- **Pattern:** Network calls, file reads in handlers/keepers
- **Risk:** Non-deterministic results
- **Search:** `http.`, `ioutil.ReadFile`, `os.Open`
- **Exception:** Bifrost observers/signers (outside consensus)

#### Environment Variables

- **Pattern:** Reading env vars during execution
- **Risk:** Different values on different nodes
- **Search:** `os.Getenv`, `os.LookupEnv`
- **Exception:** Startup configuration only

### 7. LOW: Code Quality Issues

#### Error Handling

- **Pattern:** Ignored errors, unchecked results
- **Risk:** Silent failures leading to incorrect state
- **Search:** `_ =`, `err != nil` patterns

#### Testing Coverage

- **Check:** Modified files have corresponding tests
- **Check:** Regression tests updated if behavior changed
- **Files:** `test/regression/suites/**/*.yaml`

## Review Process

### Step 1: Gather Context

```bash
# If reviewing an MR, get details
glab mr view [MR_ID] --output json

# Get the diff
glab mr diff [MR_ID]

# Check CI status
glab ci view
```

### Step 2: Identify Changed Files

- Focus on files in:
  - `x/thorchain/` - Core consensus logic
  - `app/` - Application setup, WASM config
  - `*.proto` - Data structure definitions
  - `common/` - Shared utilities
  - `constants/` - System constants

### Step 3: Pattern Search

Use `Grep` to search for patterns:

```bash
# Float usage
grep -r "float32\|float64" --include="*.go"

# Map iteration
grep -r "for.*range.*map" --include="*.go"

# Division operations
grep -r "\.Quo\(|\.Div\(|/" --include="*.go"

# Random/time usage
grep -r "time.Now\(\)|rand\." --include="*.go"

# Goroutines
grep -r "go func\|goroutine" --include="*.go"
```

### Step 4: Manual Review

For each flagged pattern:

1. Read the surrounding code context
2. Determine if it's in consensus-critical path
3. Assess actual risk level
4. Note file path and line number

### Step 5: Check Special Files

- If `app/wasm.go` changed: Check WASM API modifications
- If `.proto` files changed: Check for breaking changes
- If `BeginBlocker`/`EndBlocker` changed: Scrutinize heavily

### Step 6: Report Findings

Generate structured report with:

- **Severity:** CRITICAL, HIGH, MEDIUM, LOW
- **Category:** Non-determinism, Division, WASM, etc.
- **Location:** `file.go:line`
- **Description:** What the issue is
- **Impact:** Why it's a problem
- **Recommendation:** How to fix

## Example Report Format

```markdown
## Code Review Summary

**MR:** #1234
**Branch:** feature/new-swap-logic
**CI Status:** ✅ Passing / ❌ Failing
**Files Changed:** 12

### CRITICAL Issues (0)

### HIGH Issues (2)

#### 1. Map Iteration in State Machine

- **Location:** `x/thorchain/handler_swap.go:145`
- **Pattern:** Iterating over map without sorting keys
- **Impact:** Non-deterministic execution order could cause consensus failure
- **Recommendation:** Extract and sort keys before iteration

#### 2. Division Without Zero Check

- **Location:** `x/thorchain/manager_pool.go:89`
- **Pattern:** Division operation without validating divisor
- **Impact:** Could panic and halt chain if divisor is zero
- **Recommendation:** Add zero check before division

### MEDIUM Issues (1)

#### 1. Protobuf Field Type Changed

- **Location:** `x/thorchain/types/msg.proto:23`
- **Pattern:** Changed field from `string` to `bytes`
- **Impact:** Breaking change for existing data
- **Recommendation:** Deprecate old field, add new field with different number

### LOW Issues (0)

### Passed Checks ✅

- No float math detected
- No random number generation in state machine
- No WASM API changes
- No goroutines in consensus code
- BeginBlocker/EndBlocker unchanged
```

## Integration with /review Command

This skill should automatically activate when the `/review` command is used. The `/review` command should:

1. Detect the current branch or MR being reviewed
2. Invoke this skill to perform the analysis
3. Present findings to the user

## Additional Checks

### Code Style

- After Go file changes: Check `goimports -w` was run
- After proto changes: Check `make generate` was run
- After Markdown changes: Check `trunk fmt` was run

### Build & Test

```bash
# Build check
make build

# Run tests
make test

# Run specific test suite (if applicable)
RUN=TestName make test

# Lint check
make lint
```

## Notes

- This is a THORChain/Cosmos SDK specific review
- Patterns may need adjustment for other blockchain frameworks
- Always consider the context: client code vs consensus code
- When in doubt, escalate to manual review
- False positives are acceptable; false negatives are not

## References

- THORChain Architecture: `/docs/architecture/`
- Cosmos SDK Determinism: https://docs.cosmos.network/
- WASM Integration: `app/wasm.go`
- Testing Guide: `CLAUDE.md` in repo root
