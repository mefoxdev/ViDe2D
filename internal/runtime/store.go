package runtime

import (
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
	"unicode"

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

// sizeOf is the number of bytes a key and its value take in the store.
func sizeOf(key string, enc any) int {
	b, err := json.Marshal(enc)
	if err != nil {
		return 1 << 30
	}
	return len(key) + len(b)
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
	budget := MaxStoreBytes
	enc, err := toJSON(v, 0, &budget)
	work := MaxStoreBytes - budget
	if err != nil {
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
// file is read and written through projfs (no links, atomic write, bounded).
type FileStore struct {
	*MemStore
	dir, name string
	// Log receives problems with the file (one line each). May be nil.
	Log func(string)
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

// NewFileStore opens the memory of the project in the folder root, saved in
// savesDir (see DefaultSavesDir). A missing file is an empty store; a damaged
// one is reported to log and treated as empty.
func NewFileStore(savesDir, root string, log func(string)) *FileStore {
	f := &FileStore{MemStore: NewMemStore(), dir: savesDir, name: StoreID(root) + ".json", Log: log}
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

func (f *FileStore) load() {
	r, err := projfs.OpenDir(f.dir)
	if err != nil {
		return // no folder yet: nothing saved
	}
	defer r.Close()
	data, err := r.ReadFile(f.name, maxStoreFile)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.logf("Сохранения игры не прочитались (%v): игра начинает с пустой памяти.", err)
		}
		return
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil || sf.Version != 1 {
		f.logf("Файл сохранений %s повреждён: игра начинает с пустой памяти.", f.Path())
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

// Flush writes the file if anything changed since the last write.
func (f *FileStore) Flush() error {
	if !f.dirty {
		return nil
	}
	data, err := json.Marshal(storeFile{Version: 1, Values: f.vals})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	r, err := projfs.OpenDir(f.dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.WriteFile(f.name, data); err != nil {
		return err
	}
	f.dirty = false
	return nil
}

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
