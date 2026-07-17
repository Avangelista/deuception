package prefs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Avangelista/big2-tui/internal/game"
)

func TestLoadMissingFile(t *testing.T) {
	got := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !reflect.DeepEqual(got, Prefs{}) {
		t.Errorf("missing file: got %+v, want zero Prefs", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	want := Prefs{
		Rules:     game.Rules{Straights: game.StraightsHongKong, Flush: game.FlushBySuit, Pass: game.PassReenter, Lead: game.LeadWinner},
		Reactions: []string{"a", "b", "c"},
		Letters:   map[string]string{"SHA256:abc": "R", "local": "K"},
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(path); !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); !reflect.DeepEqual(got, Prefs{}) {
		t.Errorf("corrupt file: got %+v, want zero Prefs", got)
	}
}

func TestSaveIsAtomicRename(t *testing.T) {
	// A successful Save leaves no stray temp file beside the target.
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := Save(path, Prefs{Letters: map[string]string{"x": "A"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}
