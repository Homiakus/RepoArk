package execx

import (
	"context"
	"runtime"
	"testing"
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
