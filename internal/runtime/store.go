package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// Limits of the memory of a game ("remember" / "recall", safety.md).
const (
	MaxStoreKeys     = 1000
	MaxStoreBytes    = 1 << 20 // keys and encoded values together
	MaxStoreKeyChars = 200
	maxStoreFile     = 4 << 20 // the file may be larger than the data: JSON syntax
	maxStoreDepth    = 8
)

// Store is the persistent memory of one game: values by key. Only numbers,
// texts, yes/no and lists of those are kept. Everything is deterministic:
// nothing depends on map order.
type Store interface {
	// Get returns a copy of the value (a list is never shared with the store).
	Get(key string) (v vm.Value, ok bool, work int)
	// Set stores a copy of the value. It fails with a HostError (TD402) when
	// the limits would be exceeded; the store is then unchanged.
	Set(key string, v vm.Value) (work int, err error)
	// Flush writes the changes to permanent storage (a no-op for memory).
	Flush() error
}

// ---- encoding ----

// toJSON turns a value into plain Go values for encoding/json.
func toJSON(v vm.Value, depth int, budget *int) (any, error) {
	if *budget--; *budget < 0 {
		return nil, fmt.Errorf("слишком большое значение")
	}
	switch v.K {
	case vm.KNum:
		if math.IsNaN(v.N) || math.IsInf(v.N, 0) {
			return nil, fmt.Errorf("число не годится")
		}
		return v.N, nil
	case vm.KText:
		if why := badText(v.S, true); why != "" {
			return nil, fmt.Errorf("в тексте %s", why)
		}
		return v.S, nil
	case vm.KBool:
		return v.IsTrue(), nil
	case vm.KList:
		if depth >= maxStoreDepth {
			return nil, fmt.Errorf("список вложен слишком глубоко")
		}
		out := make([]any, len(v.L.Items))
		for i, it := range v.L.Items {
			x, err := toJSON(it, depth+1, budget)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	}
	return nil, fmt.Errorf("этот вид значения не запоминается")
}

// badText says what is wrong with a text that is to be stored ("" if
// nothing): not valid UTF-8 (JSON would silently change it, so a round trip
// would not give the same text) or control characters. A value may hold
// newlines and tabs; a key may not.
func badText(s string, allowLayout bool) string {
	if !utf8.ValidString(s) {
		return "есть байты, которые не являются текстом UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(allowLayout && (r == '\n' || r == '\t')) {
			return "есть служебные символы"
		}
	}
	return ""
}

// fromJSON is the reverse; it accepts only what toJSON makes.
func fromJSON(x any, depth int, budget *int) (vm.Value, bool) {
	if *budget--; *budget < 0 {
		return vm.Value{}, false
	}
	switch t := x.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return vm.Value{}, false
		}
		return vm.Num(t), true
	case string:
		return vm.Text(t), true
	case bool:
		return vm.Bool(t), true
	case []any:
		if depth >= maxStoreDepth {
			return vm.Value{}, false
		}
		items := make([]vm.Value, len(t))
		for i, it := range t {
			v, ok := fromJSON(it, depth+1, budget)
			if !ok {
				return vm.Value{}, false
			}
			items[i] = v
		}
		return vm.NewList(items...), true
	}
	return vm.Value{}, false
}

// encodeJSON is the one encoder of the store: what it measures is what the
// file holds (no HTML escaping, no trailing newline).
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// sizeOf is the number of bytes a key and its value take in the file: the
// encoded key and the encoded value, the same bytes the file gets.
func sizeOf(key string, enc any) int {
	k, err1 := encodeJSON(key)
	b, err2 := encodeJSON(enc)
	if err1 != nil || err2 != nil {
		return 1 << 30
	}
	return len(k) + len(b)
}

// MemStore is a Store in memory: the default of a world, and the fake of tests.
type MemStore struct {
	vals  map[string]any // encoded values (plain Go values), never shared with scripts
	sizes map[string]int
	total int
	dirty bool
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{vals: map[string]any{}, sizes: map[string]int{}} }

// Get implements Store.
func (m *MemStore) Get(key string) (vm.Value, bool, int) {
	enc, ok := m.vals[key]
	if !ok {
		return vm.Value{}, false, 0
	}
	budget := MaxStoreBytes
	v, _ := fromJSON(enc, 0, &budget)
	return v, true, MaxStoreBytes - budget
}

// Set implements Store.
func (m *MemStore) Set(key string, v vm.Value) (int, error) {
	if key == "" || len([]rune(key)) > MaxStoreKeyChars {
		return 0, storeErr(fmt.Sprintf("Ключ в «remember» — непустой текст не длиннее %d символов.", MaxStoreKeyChars))
	}
	if why := badText(key, false); why != "" {
		return 0, &vm.HostError{Code: vm.CodeWrongType, Message: "Ключ в «remember»: " + why + ".",
			Hint: "Ключ — обычный текст из букв, цифр и знаков, например \"best_score\"."}
	}
	budget := MaxStoreBytes
	enc, err := toJSON(v, 0, &budget)
	work := MaxStoreBytes - budget
	if err != nil {
		if strings.Contains(err.Error(), "в тексте") {
			return work, &vm.HostError{Code: vm.CodeWrongType, Message: "Это значение не запомнить: " + err.Error() + ".",
				Hint: "Запоминайте обычный текст (переводы строк и табуляция можно)."}
		}
		return work, storeErr("Это значение не запомнить: " + err.Error() + ".")
	}
	size := sizeOf(key, enc)
	_, had := m.vals[key]
	if !had && len(m.vals) >= MaxStoreKeys {
		return work, storeErr(fmt.Sprintf("Слишком много записей в памяти игры: не больше %d ключей.", MaxStoreKeys))
	}
	if m.total-m.sizes[key]+size > MaxStoreBytes {
		return work, storeErr(fmt.Sprintf("Слишком много данных в памяти игры: не больше %d МБ.", MaxStoreBytes>>20))
	}
	m.total += size - m.sizes[key]
	m.vals[key], m.sizes[key] = enc, size
	m.dirty = true
	return work, nil
}

func storeErr(msg string) error {
	return &vm.HostError{Code: vm.CodeTooBig, Message: msg, Hint: "Запоминайте только нужное: например, лучший счёт, а не каждый кадр."}
}

// Flush implements Store.
func (m *MemStore) Flush() error { m.dirty = false; return nil }

// Len is the number of keys.
func (m *MemStore) Len() int { return len(m.vals) }

// Keys lists the keys, sorted.
func (m *MemStore) Keys() []string {
	keys := make([]string, 0, len(m.vals))
	for k := range m.vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- files ----

// FileStore is a MemStore saved as a JSON file in a folder of the user's own
// (not in the project: the game must not be able to reach it by a path). The
// file is read and written through projfs (no links, atomic write, bounded,
// permissions 0600).
//
// Writing never blocks the game loop. Flush encodes the changes and hands the
// newest snapshot to a single background writer; snapshots made while it is
// busy replace each other (only the latest is written), so at most one write is
// waiting and its size is bounded by MaxStoreBytes. Sync (World.Flush, at the
// end of the game) writes at once and waits. A write that failed is reported by
// the next Flush and tried again.
//
// A save file that could not be read, is larger than allowed, or comes from a
// newer version of the engine is never overwritten: the store is then
// read-only for the file (one line in the log) and works in memory. A file that
// is damaged (not JSON) is moved aside as <name>.bak-<n> before the first write.
type FileStore struct {
	*MemStore
	dir, name string
	// Log receives problems with the file (one line each). May be nil.
	Log func(string)

	readOnly atomic.Bool // never write: the file on disk is not ours to replace
	backup   bool        // the file is damaged: move it aside before the first write

	write func(data []byte) error // writes one snapshot; tests replace it

	mu      sync.Mutex
	pending []byte // the newest snapshot not yet written
	started bool
	closed  bool
	lastErr error
	wake    chan struct{}
	wmu     sync.Mutex  // one file write at a time
	retry   atomic.Bool // the last write failed: write again at the next Flush
}

// StoreID is the stable name of a project's save file: the sanitized folder
// name and a short hash of its absolute path, so that two projects in folders
// of the same name do not share their saves.
func StoreID(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)
	var b strings.Builder
	for _, r := range filepath.Base(abs) {
		switch {
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	name := strings.Trim(b.String(), "_.")
	if name == "" {
		name = "game"
	}
	sum := sha256.Sum256([]byte(abs))
	return name + "-" + hex.EncodeToString(sum[:4])
}

// DefaultSavesDir is the folder for the saves of all games: the user's config
// folder, vide2d/saves.
func DefaultSavesDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "vide2d", "saves"), nil
}

// staleTempAge is how old a leftover temporary file of an interrupted write
// must be to be deleted when a store opens.
const staleTempAge = 24 * time.Hour

// NewFileStore opens the memory of the project in the folder root, saved in
// savesDir (see DefaultSavesDir). A missing file is an empty store; a file that
// cannot be used is reported to log, the game starts with an empty memory and
// the file is kept (see FileStore).
func NewFileStore(savesDir, root string, log func(string)) *FileStore {
	f := &FileStore{MemStore: NewMemStore(), dir: savesDir, name: StoreID(root) + ".json", Log: log}
	f.write = f.writeFile
	f.load()
	return f
}

// Path is the file the store saves to.
func (f *FileStore) Path() string { return filepath.Join(f.dir, f.name) }

func (f *FileStore) logf(format string, a ...any) {
	if f.Log != nil {
		f.Log(fmt.Sprintf(format, a...))
	}
}

type storeFile struct {
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

const storeVersion = 1

func (f *FileStore) load() {
	r, err := projfs.OpenDir(f.dir)
	if err != nil {
		return // no folder yet: nothing saved
	}
	defer r.Close()
	r.RemoveStaleTemps(".", time.Now().Add(-staleTempAge), 1000, 20)
	data, err := r.ReadFile(f.name, maxStoreFile)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.readOnly.Store(true)
			f.logf("Сохранения игры не прочитались (%v): игра начинает с пустой памяти, а файл %s не будет перезаписан.", err, f.Path())
		}
		return
	}
	var head struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(data, &head)
	if head.Version > storeVersion {
		f.readOnly.Store(true)
		f.logf("Файл сохранений %s записан более новой версией движка (%d): игра начинает с пустой памяти, файл не будет перезаписан.", f.Path(), head.Version)
		return
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil || sf.Version != storeVersion {
		f.backup = true
		f.logf("Файл сохранений %s повреждён: игра начинает с пустой памяти, а старый файл будет сохранён рядом с суффиксом .bak.", f.Path())
		return
	}
	keys := make([]string, 0, len(sf.Values))
	for k := range sf.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the same keys survive the limits every time
	dropped := 0
	for _, k := range keys {
		budget := MaxStoreBytes
		v, ok := fromJSON(sf.Values[k], 0, &budget)
		if !ok {
			dropped++
			continue
		}
		if _, err := f.MemStore.Set(k, v); err != nil {
			dropped++
		}
	}
	f.MemStore.dirty = false
	if dropped > 0 {
		f.logf("В сохранениях игры %d %s не подошли и пропущены.", dropped, plural(dropped, "запись", "записи", "записей"))
	}
}

// encode makes the snapshot of the whole memory.
func (f *FileStore) encode() ([]byte, error) {
	return encodeJSON(storeFile{Version: storeVersion, Values: f.vals})
}

// writeFile writes one snapshot: the real thing behind f.write.
func (f *FileStore) writeFile(data []byte) error {
	if f.readOnly.Load() {
		return nil
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	r, err := projfs.OpenDir(f.dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if f.backup {
		moved := false
		for n := 1; n <= 99 && !moved; n++ {
			err := r.Rename(f.name, fmt.Sprintf("%s.bak-%d", f.name, n))
			switch {
			case err == nil:
				moved = true
			case errors.Is(err, fs.ErrNotExist):
				moved = true // nothing left to move
			case errors.Is(err, fs.ErrExist):
			default:
				return fmt.Errorf("не удалось отложить повреждённый файл: %w", err)
			}
		}
		if !moved {
			f.readOnly.Store(true)
			return fmt.Errorf("слишком много копий .bak рядом с %s: файл не перезаписан", f.Path())
		}
		f.backup = false
	}
	return r.WriteFileMode(f.name, data, 0o600)
}

// Flush hands the changes to the background writer and returns at once. It
// returns the error of an earlier write that failed (once).
func (f *FileStore) Flush() error {
	f.mu.Lock()
	err := f.lastErr
	f.lastErr = nil
	f.mu.Unlock()
	if f.readOnly.Load() {
		f.dirty = false // kept in memory only
		return err
	}
	if !f.dirty && !f.retry.Load() {
		return err
	}
	data, eerr := f.encode()
	if eerr != nil {
		return eerr
	}
	f.dirty = false
	f.retry.Store(false)
	f.mu.Lock()
	f.pending = data
	if f.closed {
		f.mu.Unlock()
		if derr := f.drain(); derr != nil {
			return derr
		}
		return err
	}
	if !f.started {
		f.started = true
		f.wake = make(chan struct{}, 1)
		go f.run(f.wake)
	}
	select {
	case f.wake <- struct{}{}:
	default: // a wake-up is already waiting
	}
	f.mu.Unlock()
	return err
}

// run is the background writer.
func (f *FileStore) run(wake chan struct{}) {
	for range wake {
		if err := f.drain(); err != nil {
			f.mu.Lock()
			f.lastErr = err
			f.mu.Unlock()
		}
	}
}

// drain writes the pending snapshot, if any, now.
func (f *FileStore) drain() error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.mu.Lock()
	data := f.pending
	f.pending = nil
	f.mu.Unlock()
	if data == nil {
		return nil
	}
	if err := f.write(data); err != nil {
		f.retry.Store(true)
		return err
	}
	return nil
}

// Sync writes everything that changed and waits until it is on disk. Call it
// when the game ends.
func (f *FileStore) Sync() error {
	f.mu.Lock()
	f.lastErr = nil
	f.mu.Unlock()
	if !f.readOnly.Load() && (f.dirty || f.retry.Load()) {
		data, err := f.encode()
		if err != nil {
			return err
		}
		f.dirty = false
		f.retry.Store(false)
		f.mu.Lock()
		f.pending = data
		f.mu.Unlock()
	}
	return f.drain()
}

// Close syncs and stops the background writer. Later Flushes write at once.
func (f *FileStore) Close() error {
	err := f.Sync()
	f.mu.Lock()
	if f.started && !f.closed {
		close(f.wake)
	}
	f.closed = true
	f.mu.Unlock()
	return err
}

// Syncer is a Store that writes in the background and can be made to finish.
type Syncer interface{ Sync() error }

// WithDevices returns the options with the real sound card (DeviceAudio) and
// the save file of the project in root (FileStore in savesDir; "" means
// DefaultSavesDir). If no folder for saves can be found the memory of the game
// is not saved, and a line says so.
func (o Options) WithDevices(root, savesDir string) Options {
	log := o.Log
	if log == nil {
		log = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	o.Audio = NewDeviceAudio(log)
	if savesDir == "" {
		d, err := DefaultSavesDir()
		if err != nil {
			log("Не нашёл папку для сохранений (" + err.Error() + "): память игры не сохранится после выхода.")
			return o
		}
		savesDir = d
	}
	o.Store = NewFileStore(savesDir, root, log)
	return o
}
