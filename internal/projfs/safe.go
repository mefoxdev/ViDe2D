package projfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxDocBytes is the largest .vide.json document the runtime, the studio and
// the client agree to read.
const MaxDocBytes = 16 << 20

// Root is an opened project folder. Every operation checks the path (no ".."
// and no symbolic link, junction or other irregular entry on the way) and acts
// on it through os.Root, so the check and the use cannot be separated by an
// attacker swapping a folder for a link (TOCTOU), and nothing can leave the
// folder even if the check were wrong. The folder itself may be reached
// through links: it is the one the user chose.
type Root struct {
	dir string
	r   *os.Root
}

// OpenDir opens the project folder dir.
func OpenDir(dir string) (*Root, error) {
	if dir == "" {
		return nil, errors.New("no project folder")
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, unwrap(err)
	}
	return &Root{dir: dir, r: r}, nil
}

// Close releases the folder handle.
func (r *Root) Close() error { return r.r.Close() }

// Dir is the path the Root was opened with.
func (r *Root) Dir() string { return r.dir }

func unwrap(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// plain reports whether the mode is that of a regular file or a folder only:
// not a symlink, and not an "irregular" entry such as a Windows junction.
func isDir(m fs.FileMode) bool     { return m&fs.ModeType == fs.ModeDir }
func isRegular(m fs.FileMode) bool { return m&fs.ModeType == 0 }

// check walks rel component by component with Lstat. Every existing component
// must be a plain folder, the last one a plain folder or file. It returns the
// info of the last component, or (nil, nil) if the path does not exist from
// some component on and mayMiss is true.
func (r *Root) check(rel string, mayMiss bool) (fs.FileInfo, error) {
	if !ValidRel(rel) {
		return nil, fmt.Errorf("unsafe path %q", rel)
	}
	parts := strings.Split(rel, "/")
	var st fs.FileInfo
	for i := range parts {
		var err error
		st, err = r.r.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			if mayMiss && errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("%s: %w", rel, unwrap(err))
		}
		if st.Mode()&fs.ModeSymlink != 0 || (!isDir(st.Mode()) && !isRegular(st.Mode())) {
			return nil, fmt.Errorf("%s: symbolic links are not allowed", rel)
		}
		if i < len(parts)-1 && !isDir(st.Mode()) {
			return nil, fmt.Errorf("%s: %s is not a folder", rel, strings.Join(parts[:i+1], "/"))
		}
	}
	return st, nil
}

// NoLinks checks that no existing component of rel is a link (components that
// do not exist yet are fine). Use it before creating or writing a file.
func (r *Root) NoLinks(rel string) error {
	_, err := r.check(rel, true)
	return err
}

// Stat returns the info of the existing regular file rel.
func (r *Root) Stat(rel string) (fs.FileInfo, error) {
	st, err := r.check(rel, false)
	if err != nil {
		return nil, err
	}
	if !isRegular(st.Mode()) {
		return nil, fmt.Errorf("%s: not a regular file", rel)
	}
	return st, nil
}

// Open opens the existing regular file rel for reading. The opened handle is
// verified to be the very file that was checked.
func (r *Root) Open(rel string) (*os.File, error) {
	st, err := r.Stat(rel)
	if err != nil {
		return nil, err
	}
	f, err := r.r.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, unwrap(err))
	}
	fst, err := f.Stat()
	if err != nil || !isRegular(fst.Mode()) || !os.SameFile(st, fst) {
		f.Close()
		return nil, fmt.Errorf("%s: the file changed while opening", rel)
	}
	return f, nil
}

// ReadFile reads the regular file rel, failing if it is larger than max bytes.
func (r *Root) ReadFile(rel string, max int64) ([]byte, error) {
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, unwrap(err))
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: file is larger than %d bytes", rel, max)
	}
	return data, nil
}

// ReadDirLimited lists the folder rel ("." is the root), at most n entries,
// sorted by name. rel is checked like any other path.
func (r *Root) ReadDirLimited(rel string, n int) ([]fs.DirEntry, error) {
	if rel != "." {
		st, err := r.check(rel, false)
		if err != nil {
			return nil, err
		}
		if !isDir(st.Mode()) {
			return nil, fmt.Errorf("%s: not a folder", rel)
		}
	}
	return r.readDir(rel, n)
}

func (r *Root) readDir(rel string, n int) ([]fs.DirEntry, error) {
	f, err := r.r.Open(rel)
	if err != nil {
		return nil, unwrap(err)
	}
	defer f.Close()
	des, err := f.ReadDir(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, unwrap(err)
	}
	sort.Slice(des, func(i, j int) bool { return des[i].Name() < des[j].Name() })
	return des, nil
}

// OpenSub opens the folder rel (checked, no links) as a Root of its own.
func (r *Root) OpenSub(rel string) (*Root, error) {
	st, err := r.check(rel, false)
	if err != nil {
		return nil, err
	}
	if !isDir(st.Mode()) {
		return nil, fmt.Errorf("%s: not a folder", rel)
	}
	sub, err := r.r.OpenRoot(rel)
	if err != nil {
		return nil, unwrap(err)
	}
	return &Root{dir: filepath.Join(r.dir, filepath.FromSlash(rel)), r: sub}, nil
}

func (r *Root) prepareWrite(rel string) error {
	st, err := r.check(rel, true)
	if err != nil {
		return err
	}
	if st != nil && !isRegular(st.Mode()) {
		return fmt.Errorf("%s: not a regular file", rel)
	}
	if i := strings.LastIndex(rel, "/"); i > 0 {
		if err := r.r.MkdirAll(rel[:i], 0o755); err != nil {
			return unwrap(err)
		}
	}
	return nil
}

// CreateNew creates the file rel with data; it fails with fs.ErrExist if it is
// already there. Missing folders are created.
func (r *Root) CreateNew(rel string, data []byte) error {
	if err := r.prepareWrite(rel); err != nil {
		return err
	}
	f, err := r.r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return unwrap(err)
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		r.r.Remove(rel)
	}
	return werr
}

// WriteFile writes data to rel atomically (temp file in the same folder, then
// rename). Missing folders are created.
func (r *Root) WriteFile(rel string, data []byte) error {
	if err := r.prepareWrite(rel); err != nil {
		return err
	}
	dir := ""
	if i := strings.LastIndex(rel, "/"); i > 0 {
		dir = rel[:i+1]
	}
	var tmp string
	var f *os.File
	for try := 0; ; try++ {
		var b [6]byte
		rand.Read(b[:])
		tmp = dir + ".vide-save-" + hex.EncodeToString(b[:])
		var err error
		f, err = r.r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if try >= 5 || !errors.Is(err, fs.ErrExist) {
			return unwrap(err)
		}
	}
	_, err := f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.r.Rename(tmp, rel)
	}
	if err != nil {
		r.r.Remove(tmp)
		return unwrap(err)
	}
	return nil
}

// ---- path-based wrappers: open the folder for one operation ----

func with[T any](root string, fn func(*Root) (T, error)) (T, error) {
	r, err := OpenDir(root)
	if err != nil {
		var zero T
		return zero, err
	}
	defer r.Close()
	return fn(r)
}

// OpenFile opens the regular file rel below the folder root for reading, with
// the same rules as SafeFile but in one step: use it instead of SafeFile
// followed by os.Open.
func OpenFile(root, rel string) (*os.File, error) {
	return with(root, func(r *Root) (*os.File, error) { return r.Open(rel) })
}

// ReadFile reads the regular file rel below root, at most max bytes.
func ReadFile(root, rel string, max int64) ([]byte, error) {
	return with(root, func(r *Root) ([]byte, error) { return r.ReadFile(rel, max) })
}

// WriteFile writes rel below root atomically, creating folders.
func WriteFile(root, rel string, data []byte) error {
	_, err := with(root, func(r *Root) (struct{}, error) { return struct{}{}, r.WriteFile(rel, data) })
	return err
}

// CreateNew creates rel below root, failing if it exists.
func CreateNew(root, rel string, data []byte) error {
	_, err := with(root, func(r *Root) (struct{}, error) { return struct{}{}, r.CreateNew(rel, data) })
	return err
}
