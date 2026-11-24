// Package bencode reads and writes BitTorrent's encoding. Values decode to
// int64, string, []any and map[string]any.
package bencode

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

const maxDepth = 64

var ErrSyntax = errors.New("bencode: malformed input")

// Decode reads one value from data and reports how many bytes it used.
func Decode(data []byte) (any, int, error) {
	d := decoder{
		data: data,
	}

	v, err := d.value(0)
	if err != nil {
		return nil, 0, err
	}

	return v, d.pos, nil
}

// DecodeAll reads one value that must fill data.
func DecodeAll(data []byte) (any, error) {
	v, n, err := Decode(data)
	if err != nil {
		return nil, err
	}

	if n != len(data) {
		return nil, fmt.Errorf("%w: %d bytes after the value", ErrSyntax, len(data)-n)
	}

	return v, nil
}

// RawValue finds the value of key in the top-level dictionary in data and
// returns its bytes as they appear, so it can be hashed.
func RawValue(data []byte, key string) ([]byte, error) {
	d := decoder{
		data: data,
	}
	if !d.eat('d') {
		return nil, ErrSyntax
	}

	for !d.eat('e') {
		k, err := d.str()
		if err != nil {
			return nil, err
		}

		start := d.pos

		_, err = d.value(1)
		if err != nil {
			return nil, err
		}

		if k == key {
			return data[start:d.pos], nil
		}
	}

	return nil, fmt.Errorf("bencode: no %q key", key)
}

type decoder struct {
	data []byte
	pos  int
}

func (d *decoder) eat(c byte) bool {
	if d.pos < len(d.data) && d.data[d.pos] == c {
		d.pos++
		return true
	}

	return false
}

func (d *decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nested too deeply", ErrSyntax)
	}

	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("%w: unexpected end", ErrSyntax)
	}

	c := d.data[d.pos]

	switch {
	case c == 'i':
		d.pos++
		return d.int('e')
	case c == 'l':
		d.pos++

		list := []any{}

		for !d.eat('e') {
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}

			list = append(list, v)
		}

		return list, nil
	case c == 'd':
		d.pos++

		dict := map[string]any{}

		for !d.eat('e') {
			k, err := d.str()
			if err != nil {
				return nil, err
			}

			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}

			dict[k] = v
		}

		return dict, nil
	case c >= '0' && c <= '9':
		return d.str()
	}

	return nil, fmt.Errorf("%w: unexpected %q at %d", ErrSyntax, d.data[d.pos], d.pos)
}

func (d *decoder) int(end byte) (int64, error) {
	i := bytes.IndexByte(d.data[d.pos:], end)
	if i < 0 {
		return 0, fmt.Errorf("%w: unterminated number", ErrSyntax)
	}

	n, err := strconv.ParseInt(string(d.data[d.pos:d.pos+i]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: bad number", ErrSyntax)
	}

	d.pos += i + 1

	return n, nil
}

func (d *decoder) str() (string, error) {
	n, err := d.int(':')
	if err != nil {
		return "", err
	}

	if n < 0 || n > int64(len(d.data)-d.pos) {
		return "", fmt.Errorf("%w: string runs past the end", ErrSyntax)
	}

	s := string(d.data[d.pos : d.pos+int(n)])
	d.pos += int(n)

	return s, nil
}

// Encode writes v, which may hold ints of any size, strings, byte slices,
// lists and string-keyed maps. Map keys are sorted, as the spec requires.
func Encode(v any) []byte {
	var b bytes.Buffer

	encode(&b, v)

	return b.Bytes()
}

func encode(b *bytes.Buffer, v any) {
	switch v := v.(type) {
	case int:
		fmt.Fprintf(b, "i%de", v)
	case int64:
		fmt.Fprintf(b, "i%de", v)
	case uint16:
		fmt.Fprintf(b, "i%de", v)
	case string:
		fmt.Fprintf(b, "%d:%s", len(v), v)
	case []byte:
		fmt.Fprintf(b, "%d:", len(v))
		b.Write(v)
	case []any:
		b.WriteByte('l')

		for _, e := range v {
			encode(b, e)
		}

		b.WriteByte('e')
	case []string:
		b.WriteByte('l')

		for _, e := range v {
			encode(b, e)
		}

		b.WriteByte('e')
	case map[string]any:
		b.WriteByte('d')

		keys := make([]string, 0, len(v))

		for k := range v {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		for _, k := range keys {
			encode(b, k)
			encode(b, v[k])
		}

		b.WriteByte('e')
	default:
		panic(fmt.Sprintf("bencode: cannot encode %T", v))
	}
}

// Dict reads typed values out of a decoded dictionary.
type Dict map[string]any

func (d Dict) Str(k string) string {
	s, _ := d[k].(string)
	return s
}

func (d Dict) Int(k string) int64 {
	n, _ := d[k].(int64)
	return n
}

func (d Dict) List(k string) []any {
	l, _ := d[k].([]any)
	return l
}

func (d Dict) Dict(k string) Dict {
	m, _ := d[k].(map[string]any)
	return m
}
