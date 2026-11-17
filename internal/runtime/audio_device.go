package runtime

import (
	"fmt"
	"io"
	"sync"

	"github.com/ebitengine/oto/v3"
)

const (
	deviceRate = 44100
	// maxMusicWrap stops a looping reader from spinning on a clip with no samples.
	maxMusicWrap = 2
)

// DeviceAudio plays through the sound card with oto (the sound backend of
// Ebitengine, pure Go) and the decoders of ebiten/audio. The device is opened
// at the first sound, not before: a game without sounds never touches it. If
// the device cannot be opened (a machine with no sound card, CI, a remote
// session) the game goes on without sound and one line is logged.
type DeviceAudio struct {
	// Log receives the one line about a missing device. Nil means no message.
	Log func(string)

	ctx     *oto.Context
	failed  bool
	players []*oto.Player
	music   *oto.Player
}

// oto allows one context per process, so the context is shared by every
// DeviceAudio of the process (a launcher may start several games one after
// another in one process). It is opened by the first sound and never closed.
var device struct {
	mu  sync.Mutex
	ctx *oto.Context
}

// openContext opens the sound card; tests replace it.
var openContext = func() (*oto.Context, error) {
	ctx, _, err := oto.NewContext(&oto.NewContextOptions{SampleRate: deviceRate, ChannelCount: 2, Format: oto.FormatSignedInt16LE})
	return ctx, err
}

// sharedContext returns the process's context, opening it if need be. A failure
// is not remembered here: each DeviceAudio remembers its own.
func sharedContext() (c *oto.Context, err error) {
	device.mu.Lock()
	defer device.mu.Unlock()
	if device.ctx != nil {
		return device.ctx, nil
	}
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("%v", r)
		}
	}()
	c, err = openContext()
	if err != nil {
		return nil, err
	}
	device.ctx = c
	return c, nil
}

// NewDeviceAudio returns an Audio for the real sound card; nothing is opened yet.
func NewDeviceAudio(log func(string)) *DeviceAudio { return &DeviceAudio{Log: log} }

func (a *DeviceAudio) fail(err error) {
	a.failed = true
	if a.Log != nil {
		a.Log(fmt.Sprintf("Звук выключен: не удалось открыть звуковое устройство (%v). Игра идёт без звука.", err))
	}
}

// context opens the device on first use; nil if there is none.
func (a *DeviceAudio) context() (c *oto.Context) {
	if a.failed {
		return nil
	}
	if a.ctx != nil {
		return a.ctx
	}
	ctx, err := sharedContext()
	if err != nil {
		a.fail(err)
		return nil
	}
	a.ctx = ctx
	return ctx
}

// prune closes the sounds that have finished.
func (a *DeviceAudio) prune() {
	live := a.players[:0]
	for _, p := range a.players {
		if p.IsPlaying() {
			live = append(live, p)
		} else {
			p.Close()
		}
	}
	clear(a.players[len(live):])
	a.players = live
}

func (a *DeviceAudio) start(c *Clip, loop bool) (p *oto.Player) {
	ctx := a.context()
	if ctx == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			p = nil
		}
	}()
	st, err := DecodeClip(c, deviceRate)
	if err != nil {
		return nil
	}
	var r io.Reader = st
	if loop {
		r = &loopReader{s: st}
	}
	p = ctx.NewPlayer(r)
	p.Play()
	return p
}

// PlaySound implements Audio.
func (a *DeviceAudio) PlaySound(c *Clip) error {
	a.prune()
	if len(a.players) >= MaxSounds {
		return nil
	}
	if p := a.start(c, false); p != nil {
		a.players = append(a.players, p)
	}
	return nil
}

// PlayMusic implements Audio.
func (a *DeviceAudio) PlayMusic(c *Clip) error {
	a.StopMusic()
	p := a.start(c, true)
	if p == nil {
		return errNoMusic
	}
	a.music = p
	return nil
}

// StopMusic implements Audio.
func (a *DeviceAudio) StopMusic() {
	if a.music != nil {
		a.music.Close()
		a.music = nil
	}
}

// StopAll implements Audio.
func (a *DeviceAudio) StopAll() {
	a.StopMusic()
	for _, p := range a.players {
		p.Close()
	}
	a.players = nil
}

// Playing implements Audio.
func (a *DeviceAudio) Playing() int {
	a.prune()
	return len(a.players)
}

// loopReader starts the stream over when it ends.
type loopReader struct {
	s    io.ReadSeeker
	wrap int
}

func (l *loopReader) Read(p []byte) (int, error) {
	for {
		n, err := l.s.Read(p)
		if n > 0 {
			l.wrap = 0
			if err == io.EOF {
				err = nil
			}
			return n, err
		}
		if err != io.EOF {
			return 0, err
		}
		if l.wrap++; l.wrap > maxMusicWrap {
			return 0, io.EOF
		}
		if _, err := l.s.Seek(0, io.SeekStart); err != nil {
			return 0, io.EOF
		}
	}
}
