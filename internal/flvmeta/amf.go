// SPDX-License-Identifier: GPL-3.0-only

// Package flvmeta builds the onMetaData script tag written at the start of
// fork-owned FLV segments.
//
// This is fork-owned code: the AMF0 encoder/decoder is implemented here from
// the public format and does not copy the BililiveRecorder C# implementation.
// The keyframes/filepositions layout (MaxDataCount/MinInterval/spacer) follows
// BililiveRecorder's wire behavior because FLV players expect that shape.
package flvmeta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	amfNumber      = 0x00
	amfBoolean     = 0x01
	amfString      = 0x02
	amfObject      = 0x03
	amfNull        = 0x05
	amfUndefined   = 0x06
	amfECMAArray   = 0x08
	amfStrictArray = 0x0A
	amfDate        = 0x0B
	amfLongString  = 0x0C
)

var (
	errShortAMF  = errors.New("flvmeta: truncated AMF data")
	errAMFType   = errors.New("flvmeta: unsupported AMF type")
	errAMFMarker = errors.New("flvmeta: bad AMF terminator")
)

type amfValue struct {
	kind   byte
	num    float64
	truth  bool
	str    string
	props  []amfProperty // object / ECMA array
	values []amfValue    // strict array
	dateMS float64
	tz     int16
}

type amfProperty struct {
	key   string
	value amfValue
}

type amfReader struct {
	b []byte
	i int
}

func parseScriptData(payload []byte) ([]amfProperty, error) {
	r := &amfReader{b: payload}
	name, err := r.readValue()
	if err != nil {
		return nil, err
	}
	if (name.kind != amfString && name.kind != amfLongString) || name.str != "onMetaData" {
		return nil, fmt.Errorf("flvmeta: not an onMetaData script tag")
	}
	value, err := r.readValue()
	if err != nil {
		return nil, err
	}
	if value.kind != amfECMAArray && value.kind != amfObject {
		return nil, fmt.Errorf("flvmeta: onMetaData value is AMF type 0x%02x", value.kind)
	}
	return value.props, nil
}

func (r *amfReader) readValue() (amfValue, error) {
	if r.i >= len(r.b) {
		return amfValue{}, errShortAMF
	}
	kind := r.b[r.i]
	r.i++
	switch kind {
	case amfNumber:
		v, err := r.readFloat64()
		return amfValue{kind: kind, num: v}, err
	case amfBoolean:
		if r.i >= len(r.b) {
			return amfValue{}, errShortAMF
		}
		v := r.b[r.i] != 0
		r.i++
		return amfValue{kind: kind, truth: v}, nil
	case amfString:
		s, err := r.readShortString()
		return amfValue{kind: kind, str: s}, err
	case amfLongString:
		if r.i+4 > len(r.b) {
			return amfValue{}, errShortAMF
		}
		n := int(binary.BigEndian.Uint32(r.b[r.i : r.i+4]))
		r.i += 4
		if n < 0 || r.i+n > len(r.b) {
			return amfValue{}, errShortAMF
		}
		s := string(r.b[r.i : r.i+n])
		r.i += n
		return amfValue{kind: kind, str: s}, nil
	case amfObject:
		props, err := r.readProperties(false)
		return amfValue{kind: kind, props: props}, err
	case amfECMAArray:
		if r.i+4 > len(r.b) {
			return amfValue{}, errShortAMF
		}
		r.i += 4 // entry count; the terminator is authoritative
		props, err := r.readProperties(true)
		return amfValue{kind: kind, props: props}, err
	case amfStrictArray:
		if r.i+4 > len(r.b) {
			return amfValue{}, errShortAMF
		}
		n := int(binary.BigEndian.Uint32(r.b[r.i : r.i+4]))
		r.i += 4
		if n < 0 {
			return amfValue{}, errShortAMF
		}
		values := make([]amfValue, 0, n)
		for k := 0; k < n; k++ {
			v, err := r.readValue()
			if err != nil {
				return amfValue{}, err
			}
			values = append(values, v)
		}
		return amfValue{kind: kind, values: values}, nil
	case amfDate:
		ms, err := r.readFloat64()
		if err != nil {
			return amfValue{}, err
		}
		if r.i+2 > len(r.b) {
			return amfValue{}, errShortAMF
		}
		tz := int16(binary.BigEndian.Uint16(r.b[r.i : r.i+2]))
		r.i += 2
		return amfValue{kind: kind, dateMS: ms, tz: tz}, nil
	case amfNull, amfUndefined:
		return amfValue{kind: kind}, nil
	default:
		return amfValue{}, fmt.Errorf("%w 0x%02x", errAMFType, kind)
	}
}

func (r *amfReader) readProperties(ecma bool) ([]amfProperty, error) {
	var props []amfProperty
	for {
		if r.i+2 > len(r.b) {
			return nil, errShortAMF
		}
		n := int(binary.BigEndian.Uint16(r.b[r.i : r.i+2]))
		if n == 0 && r.i+3 <= len(r.b) && r.b[r.i+2] == 0x09 {
			r.i += 3
			return props, nil
		}
		if r.i+2+n > len(r.b) {
			return nil, errShortAMF
		}
		key := string(r.b[r.i+2 : r.i+2+n])
		r.i += 2 + n
		value, err := r.readValue()
		if err != nil {
			return nil, err
		}
		props = append(props, amfProperty{key: key, value: value})
	}
}

func (r *amfReader) readShortString() (string, error) {
	if r.i+2 > len(r.b) {
		return "", errShortAMF
	}
	n := int(binary.BigEndian.Uint16(r.b[r.i : r.i+2]))
	r.i += 2
	if r.i+n > len(r.b) {
		return "", errShortAMF
	}
	s := string(r.b[r.i : r.i+n])
	r.i += n
	return s, nil
}

func (r *amfReader) readFloat64() (float64, error) {
	if r.i+8 > len(r.b) {
		return 0, errShortAMF
	}
	v := math.Float64frombits(binary.BigEndian.Uint64(r.b[r.i : r.i+8]))
	r.i += 8
	return v, nil
}

func appendString(dst []byte, s string) []byte {
	if len(s) > 0xFFFF {
		dst = append(dst, amfLongString)
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(s)))
		return append(dst, s...)
	}
	dst = append(dst, amfString)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(s)))
	return append(dst, s...)
}

func appendPropertyKey(dst []byte, key string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(key)))
	return append(dst, key...)
}

func appendNumber(dst []byte, v float64) []byte {
	dst = append(dst, amfNumber)
	return binary.BigEndian.AppendUint64(dst, math.Float64bits(v))
}

func appendValue(dst []byte, v amfValue) []byte {
	switch v.kind {
	case amfNumber:
		return appendNumber(dst, v.num)
	case amfBoolean:
		dst = append(dst, amfBoolean, 0)
		if v.truth {
			dst[len(dst)-1] = 1
		}
		return dst
	case amfString:
		return appendString(dst, v.str)
	case amfLongString:
		return appendString(dst, v.str)
	case amfObject:
		dst = append(dst, amfObject)
		dst = appendProperties(dst, v.props)
		return append(dst, 0x00, 0x00, 0x09)
	case amfECMAArray:
		dst = append(dst, amfECMAArray)
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(v.props)))
		dst = appendProperties(dst, v.props)
		return append(dst, 0x00, 0x00, 0x09)
	case amfStrictArray:
		dst = append(dst, amfStrictArray)
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(v.values)))
		for _, item := range v.values {
			dst = appendValue(dst, item)
		}
		return dst
	case amfDate:
		dst = append(dst, amfDate)
		dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(v.dateMS))
		return binary.BigEndian.AppendUint16(dst, uint16(v.tz))
	case amfNull, amfUndefined:
		return append(dst, v.kind)
	default:
		return dst
	}
}

func appendProperties(dst []byte, props []amfProperty) []byte {
	for _, prop := range props {
		if len(prop.key) > 0xFFFF {
			continue
		}
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(prop.key)))
		dst = append(dst, prop.key...)
		dst = appendValue(dst, prop.value)
	}
	return dst
}

func newStringValue(s string) amfValue            { return amfValue{kind: amfString, str: s} }
func newNumberValue(v float64) amfValue           { return amfValue{kind: amfNumber, num: v} }
func newObjectValue(props []amfProperty) amfValue { return amfValue{kind: amfObject, props: props} }
func newStrictArrayValue(values []amfValue) amfValue {
	return amfValue{kind: amfStrictArray, values: values}
}
