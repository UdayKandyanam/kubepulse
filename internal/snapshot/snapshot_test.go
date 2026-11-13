package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMemory(t *testing.T) {
	cases := map[string]int64{
		"1024":  1024,
		"1Ki":   1024,
		"512Mi": 512 * 1024 * 1024,
		"2Gi":   2 * 1024 * 1024 * 1024,
		"500M":  500 * 1000 * 1000,
		"1.5Gi": int64(1.5 * 1024 * 1024 * 1024),
	}
	for in, want := range cases {
		got, err := ParseMemory(in)
		if err != nil {
			t.Errorf("ParseMemory(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMemory(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := ParseMemory("lots"); err == nil {
		t.Error("ParseMemory(lots) should fail")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	data := []byte(`{"cluster":"lab","pods":[{"name":"web-1","namespace":"shop","phase":"Running","containers":[]}]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Cluster != "lab" || len(s.Pods) != 1 || s.Pods[0].Ref() != "shop/web-1" {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("Load of a missing file should fail")
	}
}
