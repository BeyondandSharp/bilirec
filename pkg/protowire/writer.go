package protowire

import "encoding/binary"

// Writer encodes protobuf wire fields for hand-built protobuf payloads.
type Writer struct {
	buf []byte
}

func (w *Writer) Bytes() []byte {
	return w.buf
}

func (w *Writer) writeTag(fieldNum int, wireType int) {
	w.writeVarint(uint64((fieldNum << 3) | wireType))
}

func (w *Writer) writeVarint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

func (w *Writer) WriteVarintField(fieldNum int, v uint64) {
	w.writeTag(fieldNum, WireVarint)
	w.writeVarint(v)
}

func (w *Writer) WriteStringField(fieldNum int, s string) {
	w.WriteBytesField(fieldNum, []byte(s))
}

func (w *Writer) WriteBoolField(fieldNum int, v bool) {
	if v {
		w.WriteVarintField(fieldNum, 1)
		return
	}
	w.WriteVarintField(fieldNum, 0)
}

func (w *Writer) WriteBytesField(fieldNum int, b []byte) {
	w.writeTag(fieldNum, WireBytes)
	w.writeVarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}

func (w *Writer) WriteMessageField(fieldNum int, msg []byte) {
	w.WriteBytesField(fieldNum, msg)
}

func (w *Writer) WriteFixed64Field(fieldNum int, v uint64) {
	w.writeTag(fieldNum, WireFixed64)
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], v)
	w.buf = append(w.buf, scratch[:]...)
}

func (w *Writer) WriteFixed32Field(fieldNum int, v uint32) {
	w.writeTag(fieldNum, WireFixed32)
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], v)
	w.buf = append(w.buf, scratch[:]...)
}
