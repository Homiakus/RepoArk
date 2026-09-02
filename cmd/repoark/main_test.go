package main

import "testing"

func TestInteractiveWebAliases(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "default", args: nil, want: true},
		{name: "web", args: []string{"web"}, want: true},
		{name: "legacy tui alias", args: []string{"tui"}, want: true},
		{name: "serve compatibility alias", args: []string{"serve"}, want: true},
		{name: "backup remains CLI", args: []string{"backup"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWebCommand(tt.args); got != tt.want {
				t.Fatalf("isWebCommand(%v)=%t want %t", tt.args, got, tt.want)
			}
		})
	}
}
