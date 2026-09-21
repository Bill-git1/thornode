package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Warm-up and ongoing mining must share one sequential single-block loop.
func TestZecEntrypointMinesSingleBlocksSequentially(t *testing.T) {
	for _, mode := range []string{"success", "rpc-error", "timeout", "http-error", "transport-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write("zebrad", `#!/bin/sh
while [ ! -e "$FIXTURE/stop" ]; do /bin/sleep 0.01; done
`)
			write("curl", `#!/bin/sh
case "$*" in
  *getblockcount*) echo '{"result":900}';;
  *'params":[1]'*)
    count=0
    if [ -e "$FIXTURE/count" ]; then count=$(cat "$FIXTURE/count"); fi
    count=$((count + 1))
    echo "$count" >"$FIXTURE/count"
    echo "generate-$count" >>"$FIXTURE/events"
    if [ "$count" = 1 ]; then
      while [ ! -e "$FIXTURE/release" ]; do /bin/sleep 0.01; done
      case "$MODE" in
        rpc-error) echo '{"error":{"code":-1,"message":"generate failed"},"result":null}'; exit 0;;
        timeout) exit 28;;
        http-error) exit 22;;
        transport-error) exit 7;;
      esac
    fi
    echo '{"result":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],"error":null}';;
  *) exit 1;;
esac
`)
			write("sleep", `#!/bin/sh
if [ "$1" = 7 ]; then
  echo paced >>"$FIXTURE/events"
  touch "$FIXTURE/stop"
  /bin/sleep 0.05
else
  exec /bin/sleep "$@"
fi
`)
			cmd := exec.Command("sh", filepath.Join(repoRoot(t), "build/docker/mocknet/zcash/entrypoint.sh"))
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FIXTURE="+dir, "BLOCK_TIME=7", "BOOTSTRAP_TIMEOUT=5", "MODE="+mode)
			output, err := os.Create(filepath.Join(dir, "output"))
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				write("release", "")
				write("stop", "")
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			})
			events := func() string { data, _ := os.ReadFile(filepath.Join(dir, "events")); return string(data) }
			waitFor := func(check func() bool) {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for !check() {
					if time.Now().After(deadline) {
						data, _ := os.ReadFile(filepath.Join(dir, "output"))
						t.Fatalf("timeout: events=%s output=%s", events(), data)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			waitFor(func() bool { return strings.Contains(events(), "generate-1") })
			// No second request may start while the first one is in flight.
			time.Sleep(100 * time.Millisecond)
			if events() != "generate-1\n" {
				t.Fatalf("overlapping generation: %s", events())
			}
			write("release", "")
			select {
			case err := <-done:
				done <- err
				if (err == nil) != (mode == "success") {
					t.Fatalf("unexpected mining result: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("miner did not stop")
			}
			if mode == "success" {
				var expected strings.Builder
				for i := 1; i <= 400; i++ {
					fmt.Fprintf(&expected, "generate-%d\n", i)
				}
				expected.WriteString("paced\n")
				if events() != expected.String() {
					t.Fatalf("wrong warm-up count or block interval: %s", events())
				}
			} else {
				if events() != "generate-1\n" {
					t.Fatalf("generation continued after failure: %s", events())
				}
				data, err := os.ReadFile(filepath.Join(dir, "output"))
				if err != nil {
					t.Fatal(err)
				}
				want := "single-block generation request failed"
				if mode == "rpc-error" {
					want = `single-block generation failed: {"error":{"code":-1,"message":"generate failed"},"result":null}`
				}
				if !strings.Contains(string(data), want) {
					t.Fatalf("missing mining failure diagnostic %q: %s", want, data)
				}
			}
		})
	}
}
