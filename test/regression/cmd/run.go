package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/rs/zerolog/log"
)

////////////////////////////////////////////////////////////////////////////////////////
// Retry
////////////////////////////////////////////////////////////////////////////////////////

var (
	retryCount = map[string]int{}
	retryMu    sync.Mutex
)

////////////////////////////////////////////////////////////////////////////////////////
// Run
////////////////////////////////////////////////////////////////////////////////////////

func run(out io.Writer, path string, routine int) (failExportInvariants bool, err error) {
	localLog := consoleLogger(out)

	home := "/" + strconv.Itoa(routine)
	localLog.Info().Str("path", path).Msgf("Loading regression test")

	// clear data directory
	localLog.Debug().Msg("Clearing data directory")
	thornodePath := filepath.Join(home, ".thornode")
	cmdOut, err := exec.Command("rm", "-rf", thornodePath).CombinedOutput()
	if err != nil {
		fmt.Println(string(cmdOut))
		log.Fatal().Err(err).Msg("failed to clear data directory")
	}

	// use same environment for all commands
	env := []string{
		"HOME=" + home,
		"SIGNER_NAME=thorchain",
		"SIGNER_PASSWD=password",
		"CHAIN_HOME_FOLDER=" + thornodePath,
		"THOR_TENDERMINT_INSTRUMENTATION_PROMETHEUS=false",
		// block time should be short, but all consecutive checks must complete within timeout
		fmt.Sprintf("THOR_TENDERMINT_CONSENSUS_TIMEOUT_COMMIT=%s", time.Second*3/2*getTimeFactor()),
		// all ports will be offset by the routine number
		fmt.Sprintf("THOR_COSMOS_API_ADDRESS=tcp://0.0.0.0:%d", 1317+routine),
		fmt.Sprintf("THOR_TENDERMINT_RPC_LISTEN_ADDRESS=tcp://0.0.0.0:%d", 26657+routine),
		fmt.Sprintf("THOR_TENDERMINT_P2P_LISTEN_ADDRESS=tcp://0.0.0.0:%d", 27000+routine),
		"CREATE_BLOCK_PORT=" + strconv.Itoa(8080+routine),
		"GOCOVERDIR=/mnt/coverage",
	}

	// if DEBUG is set also output thornode debug logs
	if os.Getenv("DEBUG") != "" {
		env = append(env, "THOR_TENDERMINT_LOG_LEVEL=debug")
	}

	// init chain with dog mnemonic
	localLog.Debug().Msg("Initializing chain")
	cmd := exec.Command("thornode", "init", "local", "--chain-id", "thorchain", "--recover")
	cmd.Stdin = bytes.NewBufferString(dogMnemonic + "\n")
	cmd.Env = env
	cmdOut, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println(string(cmdOut))
		log.Fatal().Err(err).Msg("failed to initialize chain")
	}

	// init keys with dog mnemonic
	localLog.Debug().Msg("Initializing keys")
	cmd = exec.Command("thornode", "keys", "--keyring-backend=file", "add", "--recover", "thorchain")
	cmd.Stdin = bytes.NewBufferString(dogMnemonic + "\npassword\npassword\n")
	cmd.Env = env
	cmdOut, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println(string(cmdOut))
		log.Fatal().Err(err).Msg("failed to initialize keys")
	}

	// init chain
	localLog.Debug().Msg("Initializing chain")
	cmd = exec.Command("thornode", "init", "local", "--chain-id", "thorchain", "-o")
	cmd.Env = env
	cmdOut, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println(string(cmdOut))
		log.Fatal().Err(err).Msg("failed to initialize chain")
	}

	// create routine local state (used later by custom template functions in operations)
	nativeTxIDsMu.Lock()
	nativeTxIDs[routine] = []string{}
	nativeTxIDsMu.Unlock()
	tmpls := template.Must(templates.Clone())

	// ensure no naming collisions
	if tmpls.Lookup(filepath.Base(path)) != nil {
		log.Fatal().Msgf("test name collision: %s", filepath.Base(path))
	}

	ops, opLines, env, failExportInvariants := parseOps(localLog, path, tmpls, env)

	// warn if no operations found
	if len(ops) == 0 {
		err = errors.New("no operations found")
		localLog.Err(err).Msg("")
		return false, err
	}

	localLog.Info().Str("path", path).Int("blocks", blockCount(ops)).Msgf("Running regression test")

	// clear block directory
	blocksPath := filepath.Join("/mnt/blocks", strings.TrimPrefix(path, "suites/"))
	blocksPath = strings.TrimSuffix(blocksPath, ".yaml")
	_ = os.RemoveAll(blocksPath)

	// execute all state operations
	stateOpCount := 0
	for i, op := range ops {
		if _, ok := op.(*OpState); ok {
			localLog.Info().Int("line", opLines[i]).Msgf(">>> [%d] %s", i+1, op.OpType())
			err = op.Execute(out, path, opLines[i], routine, cmd.Process, nil)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to execute state operation")
			}
			stateOpCount++
		}
	}
	ops = ops[stateOpCount:]
	opLines = opLines[stateOpCount:]

	// validate genesis
	localLog.Debug().Msg("Validating genesis")
	cmd = exec.Command("thornode", "genesis", "validate")
	cmd.Env = env
	cmdOut, err = cmd.CombinedOutput()
	if err != nil {
		cmdErr := err
		// dump the genesis
		fmt.Println(ColorPurple + "Genesis:" + ColorReset)
		var f *os.File
		f, err = os.OpenFile(filepath.Join(home, ".thornode/config/genesis.json"), os.O_RDWR, 0o644)
		if err != nil {
			log.Fatal().Err(err).Str("path", path).Msg("failed to open genesis file")
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		f.Close()

		// dump error and exit
		fmt.Println(string(cmdOut))
		log.Fatal().Err(cmdErr).Msg("genesis validation failed")
	}

	// render config
	localLog.Debug().Msg("Rendering config")
	cmd = exec.Command("thornode", "render-config")
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to render config")
	}

	// overwrite private validator key
	localLog.Debug().Msg("Overwriting private validator key")
	keyPath := filepath.Join(home, ".thornode/config/priv_validator_key.json")
	cmd = exec.Command("cp", "/mnt/priv_validator_key.json", keyPath)
	err = cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to overwrite private validator key")
	}

	logLevel := "info"
	switch os.Getenv("DEBUG") {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
		logLevel = os.Getenv("DEBUG")
	}

	// setup process io
	thornode := exec.Command("/regtest/cover-thornode", "--log_level", logLevel, "start")
	thornode.Env = env

	stderr, err := thornode.StderrPipe()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to setup thornode stderr")
	}
	stderrScanner := bufio.NewScanner(stderr)
	stderrLines := make(chan string, 100)
	go func() {
		for stderrScanner.Scan() {
			stderrLines <- stderrScanner.Text()
		}
	}()
	if os.Getenv("DEBUG") != "" {
		thornode.Stdout = os.Stdout
		thornode.Stderr = os.Stderr
	}

	// start thornode process
	localLog.Debug().Msg("Starting thornode")
	err = thornode.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start thornode")
	}

	// wait for thornode to listen on block creation port
	time.Sleep(time.Second)
	for i := 0; ; i++ {
		if i%100 == 0 {
			localLog.Debug().Msg("Waiting for thornode to listen")
		}
		time.Sleep(100 * time.Millisecond)
		var conn net.Conn
		conn, err = net.Dial("tcp", fmt.Sprintf("localhost:%d", 8080+routine))
		if err == nil {
			conn.Close()
			break
		}
	}

	// run the operations
	var returnErr error
	localLog.Info().Msgf("Executing %d operations", len(ops))
	for i, op := range ops {

		// prefetch sequences of check operations
		if os.Getenv("AUTO_UPDATE") == "" && op.OpType() == "check" && ops[i-1].OpType() != "check" {
			wg := sync.WaitGroup{}
			for j := i; j < len(ops); j++ {
				if ops[j].OpType() != "check" {
					break
				}
				wg.Add(1)
				go func(j int) {
					defer wg.Done()
					// trunk-ignore(golangci-lint/forcetypeassert)
					ops[j].(*OpCheck).prefetch(routine)
				}(j)
			}
			wg.Wait()
		}

		localLog.Info().Int("line", opLines[i]).Msgf(">>> [%d] %s", stateOpCount+i+1, op.OpType())
		returnErr = op.Execute(out, path, opLines[i], routine, thornode.Process, stderrLines)
		if returnErr != nil {
			localLog.Error().Err(returnErr).
				Int("line", opLines[i]).
				Int("op", stateOpCount+i+1).
				Str("type", op.OpType()).
				Str("path", path).
				Msg("operation failed")
			dumpLogs(out, stderrLines)
			break
		}

		// if in AUTO_UPDATE and the operation was a check, re-render future operations
		if os.Getenv("AUTO_UPDATE") != "" && op.OpType() == "check" {
			newOps, _, _, _ := parseOps(localLog, path, tmpls, env)
			remainingOps := len(ops) - i - 1
			newOps = newOps[len(newOps)-remainingOps:]
			for j, newOp := range newOps {
				ops[i+j+1] = newOp
			}
		}
	}

	// log success
	if returnErr == nil {
		localLog.Info().Msg("All operations succeeded")
	}

	// stop thornode process
	localLog.Debug().Msg("Stopping thornode")
	err = thornode.Process.Signal(syscall.SIGUSR1)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to stop thornode")
	}

	// wait for process to exit
	_, err = thornode.Process.Wait()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to wait for thornode")
	}

	// retry context deadline exceeded errors
	if returnErr != nil && strings.Contains(returnErr.Error(), "context deadline exceeded") {
		log.Warn().Err(returnErr).Str("path", path).Msg("retrying suite with timeout")
		fmt.Println()

		// track retries per path
		retryMu.Lock()
		retryCount[path]++
		if retryCount[path] > 3 {
			retryMu.Unlock()
			return false, returnErr
		}
		retryMu.Unlock()

		return run(out, path, routine)
	}

	// if failed and debug enabled restart to allow inspection
	if returnErr != nil && os.Getenv("DEBUG") != "" {

		// remove validator key (otherwise thornode will hang in begin block)
		localLog.Debug().Msg("Removing validator key")
		cmd = exec.Command("rm", keyPath)
		cmdOut, err = cmd.CombinedOutput()
		if err != nil {
			fmt.Println(string(cmdOut))
			log.Fatal().Err(err).Msg("failed to remove validator key")
		}

		// restart thornode
		localLog.Debug().Msg("Restarting thornode")
		thornode = exec.Command("thornode", "--log_level", logLevel, "start")
		thornode.Env = env
		thornode.Stdout = os.Stdout
		thornode.Stderr = os.Stderr
		err = thornode.Start()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to restart thornode")
		}

		// wait for thornode
		localLog.Debug().Msg("Waiting for thornode")
		_, err = thornode.Process.Wait()
		if err != nil {
			log.Fatal().Err(err).Msg("failed to wait for thornode")
		}
	}

	return failExportInvariants, returnErr
}
