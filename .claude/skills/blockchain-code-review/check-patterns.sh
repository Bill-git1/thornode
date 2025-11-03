#!/bin/bash
# Blockchain Consensus Pattern Checker
# Helper script to quickly scan for common consensus-breaking patterns

set -e

# Colors for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# Usage info
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
  echo "Usage: $0 [directory] [--json]"
  echo ""
  echo "Scans Go files for blockchain consensus-breaking patterns"
  echo ""
  echo "Arguments:"
  echo "  directory  Directory to scan (default: current directory)"
  echo "  --json     Output results in JSON format"
  exit 0
fi

# Set directory to scan
SCAN_DIR="${1:-.}"
JSON_OUTPUT=false

if [ "$2" = "--json" ] || [ "$1" = "--json" ]; then
  JSON_OUTPUT=true
fi

# Check if directory exists
if [ ! -d "$SCAN_DIR" ]; then
  echo "Error: Directory $SCAN_DIR does not exist"
  exit 1
fi

# Initialize counters
CRITICAL=0
HIGH=0
MEDIUM=0
LOW=0

# Temporary file for results
RESULTS_FILE=$(mktemp)

echo "Scanning $SCAN_DIR for consensus-breaking patterns..." >&2
echo "" >&2

# Function to add finding
add_finding() {
  local severity=$1
  local pattern=$2
  local description=$3
  local file=$4
  local line=$5

  echo "${severity}|${pattern}|${description}|${file}|${line}" >>"$RESULTS_FILE"

  case $severity in
  CRITICAL) ((CRITICAL++)) ;;
  HIGH) ((HIGH++)) ;;
  MEDIUM) ((MEDIUM++)) ;;
  LOW) ((LOW++)) ;;
  esac
}

# Check for float types
echo "  Checking for float types..." >&2
while IFS=: read -r file line content; do
  # Skip test files and generated files
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]] || [[ $file =~ pulsar\.go$ ]]; then
    continue
  fi

  add_finding "CRITICAL" "Float Type" "Floating-point type detected" "$file" "$line"
done < <(rg -n "float32|float64" --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for map iteration
echo "  Checking for map iteration..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]]; then
    continue
  fi

  # Check if it's actually iterating over a map
  if echo "$content" | grep -q "range.*map\|range [a-zA-Z]*Map\|range.*\[.*\]"; then
    add_finding "CRITICAL" "Map Iteration" "Map iteration without sorted keys" "$file" "$line"
  fi
done < <(rg -n "for .* range" --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for time.Now()
echo "  Checking for time.Now() usage..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]]; then
    continue
  fi

  add_finding "CRITICAL" "time.Now()" "Non-deterministic time usage" "$file" "$line"
done < <(rg -n "time\.Now\(\)" --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for rand usage
echo "  Checking for random number generation..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]]; then
    continue
  fi

  # Skip crypto/rand imports (usually OK)
  if echo "$content" | grep -q "crypto/rand"; then
    continue
  fi

  add_finding "CRITICAL" "Random" "Non-deterministic random usage" "$file" "$line"
done < <(rg -n '\brand\b' --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for goroutines
echo "  Checking for goroutines..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]]; then
    continue
  fi

  # Skip bifrost (observers/signers use goroutines legitimately)
  if [[ $file =~ bifrost/ ]]; then
    continue
  fi

  add_finding "HIGH" "Goroutine" "Goroutine detected in consensus code" "$file" "$line"
done < <(rg -n "go func|go [a-zA-Z_]+\(" --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for division operations (potential div by zero)
echo "  Checking for division operations..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]] || [[ $file =~ \.pb\.go$ ]]; then
    continue
  fi

  # Check for .Quo( or .Div( without prior IsZero check
  if echo "$content" | grep -q "\.Quo(\\|\.Div("; then
    add_finding "HIGH" "Division" "Division operation - verify zero check" "$file" "$line"
  fi
done < <(rg -n "\.Quo\(|\.Div\(" --type go "$SCAN_DIR" 2>/dev/null || true)

# Check for WASM API changes
echo "  Checking for WASM API changes..." >&2
if [ -f "$SCAN_DIR/app/wasm.go" ]; then
  # Check if wasm.go was modified (if git available)
  if command -v git &>/dev/null; then
    if git diff --name-only 2>/dev/null | grep -q "app/wasm.go"; then
      add_finding "CRITICAL" "WASM API" "WASM API file modified - review consensus impact" "app/wasm.go" "0"
    fi
  fi
fi

# Check for proto file changes
echo "  Checking for protobuf changes..." >&2
if command -v git &>/dev/null; then
  while IFS= read -r file; do
    add_finding "MEDIUM" "Protobuf" "Protobuf file modified - check for breaking changes" "$file" "0"
  done < <(git diff --name-only 2>/dev/null | grep "\.proto$" || true)
fi

# Check for BeginBlocker/EndBlocker
echo "  Checking BeginBlocker/EndBlocker..." >&2
while IFS=: read -r file line content; do
  if [[ $file =~ _test\.go$ ]]; then
    continue
  fi

  add_finding "HIGH" "BeginBlocker/EndBlocker" "BeginBlocker or EndBlocker logic - scrutinize carefully" "$file" "$line"
done < <(rg -n "func (Begin|End)Blocker" --type go "$SCAN_DIR" 2>/dev/null || true)

echo "" >&2

# Output results
if [ "$JSON_OUTPUT" = true ]; then
  # JSON output
  echo "{"
  echo '  "summary": {'
  echo "    \"critical\": $CRITICAL,"
  echo "    \"high\": $HIGH,"
  echo "    \"medium\": $MEDIUM,"
  echo "    \"low\": $LOW,"
  echo "    \"total\": $((CRITICAL + HIGH + MEDIUM + LOW))"
  echo "  },"
  echo '  "findings": ['

  first=true
  while IFS='|' read -r severity pattern description file line; do
    if [ "$first" = true ]; then
      first=false
    else
      echo ","
    fi

    echo -n "    {"
    echo -n "\"severity\": \"$severity\", "
    echo -n "\"pattern\": \"$pattern\", "
    echo -n "\"description\": \"$description\", "
    echo -n "\"file\": \"$file\", "
    echo -n "\"line\": $line"
    echo -n "}"
  done <"$RESULTS_FILE"

  echo ""
  echo "  ]"
  echo "}"
else
  # Human-readable output
  echo -e "${GREEN}=== Scan Complete ===${NC}"
  echo ""
  echo "Summary:"
  echo -e "  ${RED}CRITICAL: $CRITICAL${NC}"
  echo -e "  ${YELLOW}HIGH:     $HIGH${NC}"
  echo -e "  MEDIUM:   $MEDIUM"
  echo -e "  LOW:      $LOW"
  echo "  ----------------------"
  echo "  TOTAL:    $((CRITICAL + HIGH + MEDIUM + LOW))"
  echo ""

  if [ $((CRITICAL + HIGH + MEDIUM + LOW)) -gt 0 ]; then
    echo "Detailed Findings:"
    echo ""

    # Sort by severity
    for sev in CRITICAL HIGH MEDIUM LOW; do
      grep "^${sev}|" "$RESULTS_FILE" 2>/dev/null | while IFS='|' read -r severity pattern description file line; do
        color=$NC
        case $severity in
        CRITICAL) color=$RED ;;
        HIGH) color=$YELLOW ;;
        esac

        echo -e "${color}[$severity]${NC} $pattern"
        echo "  File: $file:$line"
        echo "  Description: $description"
        echo ""
      done
    done
  else
    echo -e "${GREEN}No consensus-breaking patterns detected!${NC}"
  fi
fi

# Cleanup
rm -f "$RESULTS_FILE"

# Exit with error code if critical or high severity issues found
if [ $CRITICAL -gt 0 ] || [ $HIGH -gt 0 ]; then
  exit 1
fi

exit 0
