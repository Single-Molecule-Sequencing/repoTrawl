package main

import (
	"reflect"
	"testing"
)

func TestPreprocessArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"no flags", []string{"repotrawl"}, []string{"repotrawl"}},
		{"single -v", []string{"repotrawl", "-v"}, []string{"repotrawl", "-v"}},
		{"-vv expands", []string{"repotrawl", "-vv"}, []string{"repotrawl", "-v", "-v"}},
		{"-vv with other flags", []string{"repotrawl", "-vv", "--dry-run"}, []string{"repotrawl", "-v", "-v", "--dry-run"}},
		{"-v -v unchanged", []string{"repotrawl", "-v", "-v"}, []string{"repotrawl", "-v", "-v"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preprocessArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
