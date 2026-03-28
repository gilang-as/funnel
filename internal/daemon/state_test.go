package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func newTempState(t *testing.T) *State {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return st
}

func TestState_AddAndList(t *testing.T) {
	st := newTempState(t)

	if err := st.Add(SavedTorrent{ID: "aaa", Magnet: "magnet:?aaa"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(SavedTorrent{ID: "bbb", Magnet: "magnet:?bbb"}); err != nil {
		t.Fatal(err)
	}

	list := st.List()
	if len(list) != 2 {
		t.Fatalf("want 2 torrents, got %d", len(list))
	}
}

func TestState_Persist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, _ := LoadState(path)
	_ = st.Add(SavedTorrent{ID: "aaa", Magnet: "magnet:?aaa"})

	// Reload from same file.
	st2, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	list := st2.List()
	if len(list) != 1 || list[0].ID != "aaa" {
		t.Fatalf("unexpected list after reload: %+v", list)
	}
}

func TestState_Remove(t *testing.T) {
	st := newTempState(t)
	_ = st.Add(SavedTorrent{ID: "aaa", Magnet: "magnet:?aaa"})
	_ = st.Add(SavedTorrent{ID: "bbb", Magnet: "magnet:?bbb"})

	if err := st.Remove("aaa"); err != nil {
		t.Fatal(err)
	}
	list := st.List()
	if len(list) != 1 || list[0].ID != "bbb" {
		t.Fatalf("unexpected list after remove: %+v", list)
	}
}

func TestState_Update(t *testing.T) {
	st := newTempState(t)
	_ = st.Add(SavedTorrent{ID: "aaa", Magnet: "magnet:?aaa"})

	if err := st.Update("aaa", func(s *SavedTorrent) {
		s.Paused = true
		s.Name = "MyTorrent"
	}); err != nil {
		t.Fatal(err)
	}

	list := st.List()
	if !list[0].Paused || list[0].Name != "MyTorrent" {
		t.Fatalf("Update did not apply: %+v", list[0])
	}
}

func TestState_Update_NotFound(t *testing.T) {
	st := newTempState(t)
	// Update of non-existent ID should be a no-op (no error).
	if err := st.Update("nonexistent", func(s *SavedTorrent) { s.Paused = true }); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestState_LoadNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no", "such", "state.json")
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.List()) != 0 {
		t.Fatal("expected empty state")
	}
}

func TestState_LoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("not json {{{"), 0644)
	_, err := LoadState(path)
	if err == nil {
		t.Fatal("expected error on corrupted file")
	}
}
