package bridge

import "time"

// serial frame headers start and end bits
// this is so we know how/when to split up mqtt messages
const (
	STX          byte = 0x02
	ETX          byte = 0x03
	MaxFrameSize      = 8192
	FlushTimeout      = 100 * time.Millisecond
)

type FrameParser struct {
	buf      []byte
	inFrame  bool
	lastData time.Time
}

func NewFrameParser() *FrameParser {
	return &FrameParser{
		buf: make([]byte, 0, 1024),
	}
}

func (p *FrameParser) Parse(data []byte) [][]byte {
	p.lastData = time.Now()
	var frames [][]byte

	for _, b := range data {
		switch b {
		case STX:
			if p.inFrame && len(p.buf) > 0 {
				frames = append(frames, p.copyBuf())
			}
			p.buf = append(p.buf[:0], b)
			p.inFrame = true

		case ETX:
			p.buf = append(p.buf, b)
			frames = append(frames, p.copyBuf())
			p.buf = p.buf[:0]
			p.inFrame = false

		default:
			if len(p.buf) >= MaxFrameSize {
				frames = append(frames, p.copyBuf())
				p.buf = p.buf[:0]
				p.inFrame = false
			}
			p.buf = append(p.buf, b)
		}
	}

	return frames
}

func (p *FrameParser) Flush() []byte {
	if len(p.buf) == 0 {
		return nil
	}
	if time.Since(p.lastData) < FlushTimeout {
		return nil
	}
	frame := p.copyBuf()
	p.buf = p.buf[:0]
	p.inFrame = false
	return frame
}

func (p *FrameParser) copyBuf() []byte {
	out := make([]byte, len(p.buf))
	copy(out, p.buf)
	return out
}
