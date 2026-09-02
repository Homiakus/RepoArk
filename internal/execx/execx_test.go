package execx

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		res, err := Run(context.Background(), "", nil, "cmd", "/c", "echo", "hello")
		if err != nil || res.Stdout != "hello" {
			t.Fatalf("stdout=%q err=%v", res.Stdout, err)
		}
		return
	}
	res, err := Run(context.Background(), "", nil, "sh", "-c", "printf hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout=%q", res.Stdout)
	}
}

func TestRunReturnsContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX sleep command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, "", nil, "sh", "-c", "sleep 30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}
