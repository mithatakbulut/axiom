package advisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vigil/internal/config"
)

const fakeCodex = `#!/bin/sh
[ -z "$OPENAI_API_KEY" ] && [ "$VIGIL_ADVISOR_CHILD" = 1 ] || exit 12
case "$1" in
 --version) echo "codex-cli 0.147.0" ;;
 app-server)
  read -r init
  printf '%s\n' '{"id":1,"result":{}}'
  read -r ack
  read -r first
  printf '%s\n' '{"id":2,"result":{"data":[],"nextCursor":"next"}}'
  read -r second
  case "$second" in *'"cursor":"next"'*) ;; *) exit 14 ;; esac
  printf '%s\n' '{"id":3,"result":{"data":[{"model":"test-sol","supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}}'
  ;;
 exec)
  case " $* " in *' -s read-only '*' approval_policy="never" '*' -m test-sol '*) ;; *) exit 15 ;; esac
  case " $* " in *' --ephemeral '*' --disable hooks '*) ;; *) exit 16 ;; esac
  input=$(/bin/cat)
  case "$input" in
   *fail_case*) printf '%s\n' '{"type":"turn.failed"}'; exit 0 ;;
   *huge_case*) /bin/dd if=/dev/zero bs=1024 count=256 2>/dev/null; exit 0 ;;
   *timeout_case*) /bin/sleep 30; exit 0 ;;
  esac
  printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"diagnosis\":\"Look at the failing assertion\",\"next_steps\":[\"Check setup\"],\"validation\":[\"Run focused tests\"]}"}}' '{"type":"turn.completed"}'
  ;;
 *) exit 11 ;;
esac
`

func TestRunner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte(fakeCodex), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "fixture-secret")
	r := Runner{Binary: path}
	model := config.Models{Advisor: "test-sol", ReasoningEffort: "high"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	result, err := r.Run(ctx, model, dir, map[string]string{"case": "success"})
	if err != nil || len(result.Validation) != 1 {
		t.Fatalf("run: %+v %v", result, err)
	}
	for _, name := range []string{"fail_case", "huge_case"} {
		if _, err = r.Run(ctx, model, dir, name); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
	}
	bad := model
	bad.Advisor = "missing"
	if err = r.Check(ctx, bad); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("missing model: %v", err)
	}
	bad = model
	bad.ReasoningEffort = "low"
	if err = r.Check(ctx, bad); err == nil || !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("unsupported effort: %v", err)
	}
	timeout, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	start := time.Now()
	if _, err = r.Run(timeout, model, dir, "timeout_case"); err == nil || !errors.Is(timeout.Err(), context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout failed: %v %v", err, time.Since(start))
	}
}
