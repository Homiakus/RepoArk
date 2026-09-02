package audit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLedgerGuardSerializesSeparateProcesses(t *testing.T) {
	if os.Getenv("REPOARK_AUDIT_LOCK_CHILD") == "1" {
		path := os.Getenv("REPOARK_AUDIT_LOCK_PATH")
		ready := os.Getenv("REPOARK_AUDIT_LOCK_READY")
		releasePath := os.Getenv("REPOARK_AUDIT_LOCK_RELEASE")
		release, err := acquireLedgerGuard(path)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(releasePath); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("parent did not release child audit lock")
	}

	root := t.TempDir()
	path := filepath.Join(root, "audit.jsonl")
	ready := filepath.Join(root, "child-ready")
	releasePath := filepath.Join(root, "child-release")

	cmd := exec.Command(os.Args[0], "-test.run=^TestLedgerGuardSerializesSeparateProcesses$")
	cmd.Env = append(os.Environ(),
		"REPOARK_AUDIT_LOCK_CHILD=1",
		"REPOARK_AUDIT_LOCK_PATH="+path,
		"REPOARK_AUDIT_LOCK_READY="+ready,
		"REPOARK_AUDIT_LOCK_RELEASE="+releasePath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("child exited before acquiring lock: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("child did not acquire lock: %v output=%s", err, output.String())
	}

	attempted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(attempted)
		_, err := Append(path, "backup", "a/b", "ok", "after child", nil)
		done <- err
	}()
	<-attempted
	select {
	case err := <-done:
		t.Fatalf("append returned while another process held the ledger lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("append did not resume after cross-process lock release")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child failed: %v output=%s", err, output.String())
	}
	if n, err := Verify(path); err != nil || n != 1 {
		t.Fatalf("verify after cross-process serialization n=%d err=%v", n, err)
	}
}
