package runtime

import (
	"image"

	"github.com/vide2d/vide-studio/internal/assets"
)

// Images decodes project PNG files on demand and remembers the result,
// including failures, so a broken file is read once. Headless: the renderer
// turns the decoded images into GPU textures.
type Images struct {
	src    *Source
	cache  map[string]image.Image
	failed map[string]error
}

// NewImages returns an image cache over src.
func NewImages(src *Source) *Images {
	return &Images{src: src, cache: map[string]image.Image{}, failed: map[string]error{}}
}

// Get returns the decoded image at the project-relative path rel.
func (m *Images) Get(rel string) (image.Image, error) {
	if img, ok := m.cache[rel]; ok {
		return img, nil
	}
	if err, bad := m.failed[rel]; bad {
		return nil, err
	}
	img, err := m.decode(rel)
	if err != nil {
		m.failed[rel] = err
		return nil, err
	}
	m.cache[rel] = img
	return img, nil
}

func (m *Images) decode(rel string) (image.Image, error) {
	if _, err := m.src.SafeFile(rel); err != nil { // no symlinks on the way
		return nil, err
	}
	return assets.Decode(m.src.Root, rel)
}
