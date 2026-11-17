package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

func newTestFileStore(t *testing.T) (*FileStore, string, *[]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "saves")
	root := filepath.Join(t.TempDir(), "Game")
	os.MkdirAll(root, 0o755)
	var lines []string
	s := NewFileStore(dir, root, func(m string) { lines = append(lines, m) })
	return s, root, &lines
}

// preload puts content in the save file of the store of root in dir.
func preload(t *testing.T, s *FileStore, content []byte) {
	t.Helper()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func reopen(s *FileStore) (*FileStore, *[]string) {
	var lines []string
	n := &FileStore{MemStore: NewMemStore(), dir: s.dir, name: s.name, Log: func(m string) { lines = append(lines, m) }}
	n.write = n.writeFile
	n.load()
	return n, &lines
}

func TestStoreNeverOverwritesAFileItCouldNotUse(t *testing.T) {
	big := []byte(`{"version":1,"values":{"a":"` + strings.Repeat("x", 5<<20) + `"}}`)
	newer := []byte(`{"version":2,"values":{"future":[1,2,3]}}`)
	cases := map[string][]byte{"too big": big, "newer version": newer}
	for name, content := range cases {
		s, _, _ := newTestFileStore(t)
		preload(t, s, content)
		s2, lines := reopen(s)
		if len(*lines) != 1 || !strings.Contains((*lines)[0], "не будет перезаписан") && !strings.Contains((*lines)[0], "не будет перезаписан") {
			t.Fatalf("%s: expected one explanation, got %v", name, *lines)
		}
		if _, err := s2.Set("mine", vm.Num(1)); err != nil {
			t.Fatal(err)
		}
		if err := s2.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := s2.Close(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(s.Path()); string(got) != string(content) {
			t.Fatalf("%s: the file was overwritten", name)
		}
		if v, ok, _ := s2.Get("mine"); !ok || v.N != 1 {
			t.Fatalf("%s: memory must keep working in RAM", name)
		}
	}
}

func TestStoreReadErrorKeepsTheFile(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	if err := os.MkdirAll(s.Path(), 0o700); err != nil { // a folder where the file should be: unreadable
		t.Fatal(err)
	}
	s2, lines := reopen(s)
	if len(*lines) != 1 {
		t.Fatalf("log: %v", *lines)
	}
	s2.Set("k", vm.Num(1))
	s2.Sync()
	if st, err := os.Stat(s.Path()); err != nil || !st.IsDir() {
		t.Fatal("the unreadable entry was replaced")
	}
}

func TestStoreDamagedFileIsMovedAsideNotLost(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	preload(t, s, []byte("{broken"))
	s2, lines := reopen(s)
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "повреждён") {
		t.Fatalf("log: %v", *lines)
	}
	s2.Set("k", vm.Num(1))
	if err := s2.Sync(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.Path() + ".bak-1"); string(b) != "{broken" {
		t.Fatalf("the damaged file was not kept: %q", b)
	}
	if reopened, _ := reopen(s); reopened.Len() != 1 {
		t.Fatal("the new file is not a good save")
	}
	// damaged again: the next copy gets the next number
	preload(t, s, []byte("again"))
	s3, _ := reopen(s)
	s3.Set("k2", vm.Num(2))
	s3.Sync()
	if b, _ := os.ReadFile(s.Path() + ".bak-2"); string(b) != "again" {
		t.Fatalf("second copy: %q", b)
	}
}

func TestStoreFileMode0600AndStaleTempsAreCleaned(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	os.MkdirAll(s.dir, 0o700)
	old := filepath.Join(s.dir, ".vide-save-oldleftover")
	fresh := filepath.Join(s.dir, ".vide-save-freshleftover")
	other := filepath.Join(s.dir, "keep.txt")
	for _, f := range []string{old, fresh, other} {
		os.WriteFile(f, []byte("x"), 0o600)
	}
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(other, past, past)
	s2, _ := reopen(s)
	if _, err := os.Stat(old); err == nil {
		t.Error("an old leftover temp file must be deleted")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh temp file may belong to a running copy: keep it")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("only .vide-save-* files may be deleted")
	}
	s2.Set("k", vm.Num(1))
	s2.Sync()
	if st, err := os.Stat(s.Path()); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("save file: %v %v", st, err)
	}
}

// slowWriter replaces the file write: the first call blocks until released.
type slowWriter struct {
	mu      sync.Mutex
	calls   [][]byte
	started chan struct{}
	release chan struct{}
	fail    int // fail this many calls first
}

func (w *slowWriter) write(data []byte) error {
	w.mu.Lock()
	w.calls = append(w.calls, append([]byte(nil), data...))
	n := len(w.calls)
	fail := w.fail > 0
	if fail {
		w.fail--
	}
	w.mu.Unlock()
	if n == 1 && w.started != nil {
		close(w.started)
		<-w.release
	}
	if fail {
		return errors.New("disk full")
	}
	return nil
}

func (w *slowWriter) count() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.calls) }

func TestStoreWritesInTheBackgroundAndCoalesces(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	w := &slowWriter{started: make(chan struct{}), release: make(chan struct{})}
	s.write = w.write
	defer s.Close()
	s.Set("k", vm.Num(1))
	done := make(chan error)
	go func() { done <- s.Flush() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Flush blocked")
	}
	<-w.started // the writer is busy with snapshot 1
	for i := 2; i <= 5; i++ {
		s.Set("k", vm.Num(float64(i)))
		finished := make(chan struct{})
		go func() { s.Flush(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("Flush blocked behind a slow write")
		}
	}
	close(w.release)
	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for w.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.count() != 2 {
		t.Fatalf("snapshots 2..4 must be replaced by 5: %d writes", w.count())
	}
	if !strings.Contains(string(w.calls[1]), `"k":5`) {
		t.Fatalf("last write: %s", w.calls[1])
	}
}

func TestStoreCloseWritesSynchronously(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	s.Set("k", vm.Text("saved"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopen(s); got.Len() != 1 {
		t.Fatal("Close must leave the file written")
	}
	// after Close a Flush still saves (at once)
	s.Set("k2", vm.Num(2))
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopen(s); got.Len() != 2 {
		t.Fatal("Flush after Close must write")
	}
}

func TestStoreFailedWriteIsReportedOnceAndRetried(t *testing.T) {
	s, _, _ := newTestFileStore(t)
	w := &slowWriter{fail: 1}
	s.write = w.write
	defer s.Close()
	s.Set("k", vm.Num(1))
	s.Flush()
	deadline := time.Now().Add(2 * time.Second)
	for w.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	var err error
	for i := 0; i < 500 && err == nil; i++ {
		time.Sleep(time.Millisecond)
		err = s.Flush() // reports the failure and writes again
	}
	if err == nil {
		t.Fatal("the failed write was never reported")
	}
	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	if w.count() < 2 {
		t.Fatal("the failed write was not retried")
	}
}

func TestStoreRejectsBadTextAndRoundTripsExactly(t *testing.T) {
	m := NewMemStore()
	bad := map[string]vm.Value{
		"bad\xffkey": vm.Num(1), "ctl\x07key": vm.Num(1), "tab\tkey": vm.Num(1),
	}
	for k, v := range bad {
		if _, err := m.Set(k, v); err == nil || err.(*vm.HostError).Code != vm.CodeWrongType {
			t.Errorf("key %q: %v", k, err)
		}
	}
	for _, v := range []vm.Value{vm.Text("a\xffb"), vm.Text("bell\x07"), vm.NewList(vm.Text("x\x00"))} {
		if _, err := m.Set("v", v); err == nil || err.(*vm.HostError).Code != vm.CodeWrongType {
			t.Errorf("value %q: %v", v.String(), err)
		}
	}
	if m.Len() != 0 {
		t.Fatal("nothing bad may be stored")
	}
	s, _, _ := newTestFileStore(t)
	vals := map[string]vm.Value{
		"html": vm.Text("<b>&amp; \"q\" \\ /   привет 😀\n\t"), "list": vm.NewList(vm.Text("<>"), vm.Num(1e-7), vm.Num(-0.0)),
		"ключ": vm.Num(2),
	}
	for k, v := range vals {
		if _, err := s.Set(k, v); err != nil {
			t.Fatal(k, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, lines := reopen(s)
	if len(*lines) != 0 {
		t.Fatal(*lines)
	}
	for k, want := range vals {
		got, ok, _ := s2.Get(k)
		if !ok || got.String() != want.String() {
			t.Errorf("%s: %q != %q", k, got.String(), want.String())
		}
	}
	if s2.total != s.total {
		t.Fatalf("size accounting differs after a round trip: %d vs %d", s2.total, s.total)
	}
	// the counted size is the real size in the file: values plus the fixed frame
	data, _ := os.ReadFile(s.Path())
	if frame := len(`{"version":1,"values":{}}`) + len(vals) - 1 + len(vals); len(data) != s.total+frame {
		t.Fatalf("file %d bytes, counted %d + frame %d", len(data), s.total, frame)
	}
}
