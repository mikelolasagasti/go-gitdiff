// Package gitdiff parses and applies patches generated by Git. It supports
// line-oriented text patches, binary patches, and can also parse standard
// unified diffs generated by other tools.
package gitdiff

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const commitPrefix = "commit"

// Parse parses a patch with changes to one or more files. Any content before
// the first file is returned as the second value. If an error occurs while
// parsing, it returns all files parsed before the error.
func Parse(r io.Reader) (<-chan *File, error) {
	p := newParser(r)
	out := make(chan *File)

	if err := p.Next(); err != nil {
		close(out)
		if err == io.EOF {
			return out, nil
		}
		return out, err
	}

	go func() {
		defer close(out)

		ph := &PatchHeader{}
		for {
			file, pre, err := p.ParseNextFileHeader()
			if err != nil {
				if err == io.EOF {
					return
				}
				p.Next()
				continue
			}

			if strings.Contains(pre, commitPrefix) {
				ph, _ = ParsePatchHeader(pre)
			}

			if file == nil {
				break
			}

			for _, fn := range []func(*File) (int, error){
				p.ParseTextFragments,
				p.ParseBinaryFragments,
			} {
				n, err := fn(file)
				if err != nil {
					return
				}
				if n > 0 {
					break
				}
			}

			file.PatchHeader = ph
			out <- file
		}
	}()

	return out, nil
}

// TODO(bkeyes): consider exporting the parser type with configuration
// this would enable OID validation, p-value guessing, and prefix stripping
// by allowing users to set or override defaults

// parser invariants:
// - methods that parse objects:
//     - start with the parser on the first line of the first object
//     - if returning nil, do not advance
//     - if returning an error, do not advance past the object
//     - if returning an object, advance to the first line after the object
// - any exported parsing methods must initialize the parser by calling Next()

type stringReader interface {
	ReadString(delim byte) (string, error)
}

type parser struct {
	r stringReader

	eof    bool
	lineno int64
	lines  [3]string
}

func newParser(r io.Reader) *parser {
	if r, ok := r.(stringReader); ok {
		return &parser{r: r}
	}
	return &parser{r: bufio.NewReader(r)}
}

// Next advances the parser by one line. It returns any error encountered while
// reading the line, including io.EOF when the end of stream is reached.
func (p *parser) Next() error {
	if p.eof {
		return io.EOF
	}

	if p.lineno == 0 {
		// on first call to next, need to shift in all lines
		for i := 0; i < len(p.lines)-1; i++ {
			if err := p.shiftLines(); err != nil && err != io.EOF {
				return err
			}
		}
	}

	err := p.shiftLines()
	if err != nil && err != io.EOF {
		return err
	}

	p.lineno++
	if p.lines[0] == "" {
		p.eof = true
		return io.EOF
	}
	return nil
}

func (p *parser) shiftLines() (err error) {
	for i := 0; i < len(p.lines)-1; i++ {
		p.lines[i] = p.lines[i+1]
	}
	p.lines[len(p.lines)-1], err = p.r.ReadString('\n')
	return
}

// Line returns a line from the parser without advancing it. A delta of 0
// returns the current line, while higher deltas return read-ahead lines. It
// returns an empty string if the delta is higher than the available lines,
// either because of the buffer size or because the parser reached the end of
// the input. Valid lines always contain at least a newline character.
func (p *parser) Line(delta uint) string {
	return p.lines[delta]
}

// Errorf generates an error and appends the current line information.
func (p *parser) Errorf(delta int64, msg string, args ...interface{}) error {
	return fmt.Errorf("gitdiff: line %d: %s", p.lineno+delta, fmt.Sprintf(msg, args...))
}
