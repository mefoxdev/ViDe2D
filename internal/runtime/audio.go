package runtime

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
	"github.com/hajimehoshi/ebiten/v2/audio/vorbis"
	"github.com/hajimehoshi/ebiten/v2/audio/wav"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// Sound limits (safety.md).
const (
	MaxSounds         = 32       // sound effects that may sound at the same time
	MaxSoundBytes     = 32 << 20 // one sound file
	soundCacheBytes   = 64 << 20 // all cached sound files together
	soundCacheEntries = 64
	maxFailedSounds   = 256 // remembered failures (a broken name is looked up once)
	// failedSoundTTL is how long a failure is remembered: after that the file is
	// looked up again, so a sound that was added or repaired while the game runs
	// starts to work. (Wall-clock time is fine here: it is presentation, the
	// simulation never sees it.)
	failedSoundTTL = 10 * time.Second
)

// ClipFormat is the encoding of a sound file.
type ClipFormat uint8

// Sound file formats (pure Go decoders; no cgo).
const (
	FormatWAV ClipFormat = iota
	FormatOGG
	FormatMP3
)

func (f ClipFormat) String() string { return [...]string{"wav", "ogg", "mp3"}[f] }

// Clip is a sound file that was read and checked: the encoded bytes, decoded
// only while they play.
type Clip struct {
	Name   string // project-relative path, e.g. "assets/coin.wav"
	Format ClipFormat
	Data   []byte
}

// Audio plays clips. It is the only thing between the scripts and a sound
// card, so that the rest of the runtime (and its tests) is headless. All
// methods are called from the game loop only.
type Audio interface {
	// PlaySound starts a sound effect. It plays once.
	PlaySound(c *Clip) error
	// PlayMusic starts the clip as music, in a loop, instead of any music that
	// plays. It returns an error when the music did not start (no sound card,
	// a clip that does not decode): the caller then does not count it as playing.
	PlayMusic(c *Clip) error
	// StopMusic stops the music.
	StopMusic()
	// StopAll stops the music and every sound.
	StopAll()
	// Playing is the number of sound effects that still sound.
	Playing() int
}

// NullAudio plays nothing. It is what a world without an Audio gets.
type NullAudio struct{}

func (NullAudio) PlaySound(*Clip) error { return nil }
func (NullAudio) PlayMusic(*Clip) error { return nil }
func (NullAudio) StopMusic()            {}
func (NullAudio) StopAll()              {}
func (NullAudio) Playing() int          { return 0 }

var errNoMusic = fmt.Errorf("the music did not start")

// FakeAudio records what was played, for tests. A sound counts as playing
// until Finish says it ended.
type FakeAudio struct {
	Sounds     []string // clip names, in the order they were started
	Music      []string // clips started as music
	MusicNow   string   // the music that plays now, "" if none
	MusicStops int
	StopAlls   int
	FailMusic  int // the next this many PlayMusic calls fail (the device did not start the music)
	active     int
}

func (f *FakeAudio) PlaySound(c *Clip) error {
	f.Sounds = append(f.Sounds, c.Name)
	f.active++
	return nil
}

func (f *FakeAudio) PlayMusic(c *Clip) error {
	if f.FailMusic > 0 {
		f.FailMusic--
		return errNoMusic
	}
	f.Music = append(f.Music, c.Name)
	f.MusicNow = c.Name
	return nil
}

func (f *FakeAudio) StopMusic()   { f.MusicNow = ""; f.MusicStops++ }
func (f *FakeAudio) StopAll()     { f.MusicNow = ""; f.active = 0; f.StopAlls++ }
func (f *FakeAudio) Playing() int { return f.active }

// Finish ends n of the sounds that play.
func (f *FakeAudio) Finish(n int) { f.active = max(f.active-n, 0) }

// ---- loading ----

// Sounds finds, reads and checks the sound files of a project, and keeps the
// recent ones (bounded by count and by total size). Failures are remembered
// too. Headless: nothing here touches a sound card.
type Sounds struct {
	src    *Source
	cache  map[string]*Clip
	order  []string // cache keys, oldest first
	bytes  int
	failed map[string]failedSound
	fOrder []string
	now    func() time.Time // the clock of the failures' expiry; tests replace it
}

type failedSound struct {
	err error
	at  time.Time
}

// NewSounds returns a sound cache over src.
func NewSounds(src *Source) *Sounds {
	return &Sounds{src: src, cache: map[string]*Clip{}, failed: map[string]failedSound{}, now: time.Now}
}

var soundExts = []string{".wav", ".ogg", ".mp3"}

// candidates lists the project files a script name can mean: a name with a
// known extension is itself; one without ("theme") may be .wav, .ogg or .mp3.
// A bare file name lives in assets/, a name with "/" is a project path, as with images.
func soundCandidates(name string) ([]string, error) {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\x00\\") {
		return nil, fmt.Errorf("имя файла не годится")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("в имени файла есть служебные символы")
		}
	}
	rel := name
	if !strings.Contains(name, "/") {
		rel = "assets/" + name
	}
	ext := strings.ToLower(path.Ext(rel))
	for _, e := range soundExts {
		if ext == e {
			return []string{rel}, nil
		}
	}
	out := make([]string, len(soundExts))
	for i, e := range soundExts {
		out[i] = rel + e
	}
	return out, nil
}

// Get returns the clip for a script name, reading and checking the file on
// first use. The error text is meant for the player of the game.
func (s *Sounds) Get(name string) (*Clip, error) {
	if c, ok := s.cache[name]; ok {
		return c, nil
	}
	if f, bad := s.failed[name]; bad {
		if s.now().Sub(f.at) < failedSoundTTL {
			return nil, f.err
		}
		s.forgetFailure(name) // time to look again
	}
	c, err := s.load(name)
	if err != nil {
		if len(s.failed) >= maxFailedSounds {
			delete(s.failed, s.fOrder[0])
			s.fOrder = s.fOrder[1:]
		}
		s.failed[name] = failedSound{err, s.now()}
		s.fOrder = append(s.fOrder, name)
		return nil, err
	}
	s.put(name, c)
	return c, nil
}

func (s *Sounds) forgetFailure(name string) {
	delete(s.failed, name)
	for i, n := range s.fOrder {
		if n == name {
			s.fOrder = append(s.fOrder[:i], s.fOrder[i+1:]...)
			break
		}
	}
}

func (s *Sounds) put(name string, c *Clip) {
	for len(s.order) > 0 && (len(s.order) >= soundCacheEntries || s.bytes+len(c.Data) > soundCacheBytes) {
		old := s.order[0]
		s.order = s.order[1:]
		s.bytes -= len(s.cache[old].Data)
		delete(s.cache, old)
	}
	s.cache[name] = c
	s.order = append(s.order, name)
	s.bytes += len(c.Data)
}

func (s *Sounds) load(name string) (*Clip, error) {
	cands, err := soundCandidates(name)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, rel := range cands {
		f, err := s.src.OpenFile(rel)
		if err != nil {
			lastErr = fmt.Errorf("нет такого файла в проекте")
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxSoundBytes+1))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("файл не читается")
		}
		if len(data) > MaxSoundBytes {
			return nil, fmt.Errorf("файл больше %d МБ", MaxSoundBytes>>20)
		}
		c := &Clip{Name: rel, Data: data}
		switch strings.ToLower(path.Ext(rel)) {
		case ".ogg":
			c.Format = FormatOGG
		case ".mp3":
			c.Format = FormatMP3
		}
		if err := checkClip(c); err != nil {
			return nil, err
		}
		return c, nil
	}
	return nil, lastErr
}

// DecodeClip opens the stream of a clip: signed 16-bit little-endian stereo at
// the given sample rate, seekable. It is what a sound card backend plays.
func DecodeClip(c *Clip, sampleRate int) (stream io.ReadSeeker, err error) {
	defer func() {
		if r := recover(); r != nil { // a decoder must not take the game down
			stream, err = nil, fmt.Errorf("файл повреждён")
		}
	}()
	r := bytes.NewReader(c.Data)
	switch c.Format {
	case FormatOGG:
		return vorbis.DecodeWithSampleRate(sampleRate, r)
	case FormatMP3:
		return mp3.DecodeWithSampleRate(sampleRate, r)
	}
	return wav.DecodeWithSampleRate(sampleRate, r)
}

// checkClip makes sure the file is a sound the decoders understand: it opens
// the stream and reads the first bytes.
func checkClip(c *Clip) error {
	st, err := DecodeClip(c, 44100)
	if err != nil {
		return fmt.Errorf("это не звук в формате %s или файл повреждён", c.Format)
	}
	var buf [4096]byte
	if _, err := st.Read(buf[:]); err != nil && err != io.EOF {
		return fmt.Errorf("это не звук в формате %s или файл повреждён", c.Format)
	}
	return nil
}

// ---- the script side ----

// Mixer is what the script host talks to for sound: it finds the file, applies
// the limit of simultaneous sounds and hands the clip to the Audio. One Mixer
// serves the whole game (it survives scene changes), so music goes on across
// "go to scene".
type Mixer struct {
	audio  Audio
	sounds *Sounds
	music  string // the clip name of the music that plays
	log    func(string)
	warned bool
}

func newMixer(src *Source, a Audio, log func(string)) *Mixer {
	if a == nil {
		a = NullAudio{}
	}
	return &Mixer{audio: a, sounds: NewSounds(src), log: log}
}

func soundError(kind, name string, err error) error {
	return &vm.HostError{Code: vm.CodeBadFile, Message: "Не удалось взять " + kind + " «" + name + "»: " + err.Error() + ".",
		Hint: "Звуки лежат в папке assets: например play sound \"coin.wav\". Подходят файлы .wav, .ogg и .mp3."}
}

// PlaySound plays a sound effect; beyond MaxSounds at once it is skipped.
func (m *Mixer) PlaySound(name string) error {
	c, err := m.sounds.Get(name)
	if err != nil {
		return soundError("звук", name, err)
	}
	if m.audio.Playing() >= MaxSounds {
		if !m.warned {
			m.warned = true
			m.log(fmt.Sprintf("Звуков одновременно не больше %d: лишние пропускаются (например, звук в «when update» каждый кадр).", MaxSounds))
		}
		return nil
	}
	if err := m.audio.PlaySound(c); err != nil {
		return nil // a broken sound card must not stop the script
	}
	return nil
}

// PlayMusic plays the clip as music; the same music that already plays is left alone.
func (m *Mixer) PlayMusic(name string) error {
	c, err := m.sounds.Get(name)
	if err != nil {
		return soundError("музыку", name, err)
	}
	if m.music == c.Name {
		return nil
	}
	// the music counts as playing only if the device started it: otherwise the
	// next "play music" tries again
	if err := m.audio.PlayMusic(c); err == nil {
		m.music = c.Name
	}
	return nil
}

// StopMusic stops the music.
func (m *Mixer) StopMusic() {
	m.music = ""
	m.audio.StopMusic()
}

// StopAll silences everything (a restart of the game).
func (m *Mixer) StopAll() {
	m.music = ""
	m.audio.StopAll()
}
