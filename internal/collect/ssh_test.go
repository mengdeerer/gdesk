package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathFitsDarwin(t *testing.T) {
	c, e := New()
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(nil)
	expanded := filepath.Join(c.socketDir, strings.Repeat("a", 40))
	if len(expanded) >= 104 {
		t.Fatalf("socket path too long: %d", len(expanded))
	}
	info, e := os.Stat(c.socketDir)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatal("socket directory must be private")
	}
}
func TestBoundedOutput(t *testing.T) {
	b := &limitedBuffer{max: 4}
	if _, e := b.Write([]byte("1234")); e != nil {
		t.Fatal(e)
	}
	if _, e := b.Write([]byte("5")); e == nil {
		t.Fatal("unbounded remote output")
	}
}
