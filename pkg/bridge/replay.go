package bridge

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"
)

// ReplayRecord represents one line from a JSONL replay log.
type ReplayRecord struct {
	Ts          string `json:"ts"`
	DeltaMs     int64  `json:"delta_ms"`
	Raw         string `json:"raw"`
	RepeatCount int    `json:"repeat_count"`
	RepeatMs    int64  `json:"repeat_ms"`
}

// replayChunk is sent on the data channel to Read().
type replayChunk struct {
	data []byte
	err  error
}

// ReplayReader reads from a JSONL log file and replays serial data with original timing.
// It implements the same Read/Close interface as SerialPort for use with the bridge.
type ReplayReader struct {
	dataCh  chan replayChunk
	closeCh chan struct{}
}

// OpenReplayReader opens a JSONL replay file and returns a reader that replays
// the serial data with the original timing (using delta_ms between records).
// Replays loop indefinitely until Close() is called.
func OpenReplayReader(path string) (*ReplayReader, error) {
	r := &ReplayReader{
		dataCh:  make(chan replayChunk, 1),
		closeCh: make(chan struct{}),
	}

	go r.replayLoop(path)
	return r, nil
}

func (r *ReplayReader) replayLoop(path string) {
	for {
		select {
		case <-r.closeCh:
			return
		default:
		}

		f, err := os.Open(path)
		if err != nil {
			r.send(nil, err)
			return
		}

		r.runReplay(f)
		f.Close()
	}
}

func (r *ReplayReader) runReplay(f io.Reader) {
	scanner := bufio.NewScanner(f)
	// Increase buffer for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lastSend time.Time

	for scanner.Scan() {
		select {
		case <-r.closeCh:
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec ReplayRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		// raw is a JSON string - when unmarshaled, \u0002 etc. become actual bytes
		rawBytes := []byte(rec.Raw)

		if rec.RepeatCount > 0 && rec.RepeatMs > 0 {
			// Emit the same data repeat_count times with repeat_ms between each
			interval := time.Duration(rec.RepeatMs) * time.Millisecond / time.Duration(rec.RepeatCount)
			if interval < time.Millisecond {
				interval = time.Millisecond
			}
			for i := 0; i < rec.RepeatCount; i++ {
				select {
				case <-r.closeCh:
					return
				default:
				}
				if i > 0 {
					time.Sleep(interval)
				}
				r.send(rawBytes, nil)
			}
		} else {
			// Wait delta_ms since last send
			delta := time.Duration(rec.DeltaMs) * time.Millisecond
			if !lastSend.IsZero() {
				elapsed := time.Since(lastSend)
				if delta > elapsed {
					time.Sleep(delta - elapsed)
				}
			} else if rec.DeltaMs > 0 {
				time.Sleep(delta)
			}
			lastSend = time.Now()
			r.send(rawBytes, nil)
		}
	}
}

func (r *ReplayReader) send(data []byte, err error) {
	chunk := replayChunk{data: data, err: err}
	if data != nil {
		chunk.data = make([]byte, len(data))
		copy(chunk.data, data)
	}
	select {
	case <-r.closeCh:
		return
	case r.dataCh <- chunk:
	}
}

// Read blocks until the next replay record is ready, then copies it to buf.
// Returns io.EOF when the replay file is exhausted.
func (r *ReplayReader) Read(buf []byte) (int, error) {
	select {
	case <-r.closeCh:
		return 0, io.EOF
	case chunk := <-r.dataCh:
		if chunk.err != nil {
			return 0, chunk.err
		}
		return copy(buf, chunk.data), nil
	}
}

// Close stops the replay and releases resources.
func (r *ReplayReader) Close() error {
	select {
	case <-r.closeCh:
		return nil
	default:
		close(r.closeCh)
		return nil
	}
}
