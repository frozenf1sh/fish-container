package image

import (
	"io"
	"time"
)

type progressReader struct {
	r          io.Reader
	total      int64
	current    int64
	progress   ProgressFunc
	lastReport time.Time
}

func newProgressReader(r io.Reader, total int64, progress ProgressFunc) *progressReader {
	return &progressReader{
		r:          r,
		total:      total,
		progress:   progress,
		lastReport: time.Now(),
	}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.current += int64(n)
		p.report(false)
	}
	if err == io.EOF {
		p.report(true)
	}
	return n, err
}

func (p *progressReader) report(force bool) {
	if p.progress == nil {
		return
	}
	if !force && time.Since(p.lastReport) < 200*time.Millisecond {
		return
	}
	p.lastReport = time.Now()
	p.progress(p.current, p.total)
}
