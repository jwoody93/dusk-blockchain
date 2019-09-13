// Serialization functions for CompactSize integers

package encoding

import (
	"bytes"
	"fmt"
)

// ReadVarInt reads the discriminator byte of a CompactSize int,
// and then deserializes the number accordingly.
func ReadVarInt(r *bytes.Buffer) (uint64, error) {
	// Get discriminant from variable int
	d, err := ReadUint8(r)
	if err != nil {
		return 0, err
	}

	switch d {
	case 0xff:
		rv, err := ReadUint64LE(r)
		if err != nil {
			return 0, err
		}

		// Canonical encoding check
		if rv < uint64(0x100000000) {
			return 0, fmt.Errorf("non-canonical encoding")
		}

		return rv, nil
	case 0xfe:
		v, err := ReadUint32LE(r)
		if err != nil {
			return 0, err
		}
		rv := uint64(v)

		// Canonical encoding check
		if rv < uint64(0x10000) {
			return 0, fmt.Errorf("non-canonical encoding")
		}

		return rv, nil
	case 0xfd:
		v, err := ReadUint16LE(r)
		if err != nil {
			return 0, err
		}
		rv := uint64(v)

		// Canonical encoding check
		if rv < uint64(0xfd) {
			return 0, fmt.Errorf("non-canonical encoding")
		}

		return rv, nil
	default:
		return uint64(d), nil
	}
}

// WriteVarInt writes a CompactSize integer with a number of bytes depending on it's value
func WriteVarInt(w *bytes.Buffer, v uint64) error {
	if v < 0xfd {
		return WriteUint8(w, uint8(v))
	}

	if v <= 1<<16-1 {
		if err := WriteUint8(w, 0xfd); err != nil {
			return err
		}
		return WriteUint16LE(w, uint16(v))
	}

	if v <= 1<<32-1 {
		if err := WriteUint8(w, 0xfe); err != nil {
			return err
		}
		return WriteUint32LE(w, uint32(v))
	}

	if err := WriteUint8(w, 0xff); err != nil {
		return err
	}

	return WriteUint64LE(w, v)
}

// VarIntEncodeSize returns the number of bytes needed to serialize a CompactSize int
// of size v
func VarIntEncodeSize(v uint64) uint64 {
	// Small enough to write in 1 byte (uint8)
	if v < 0xfd {
		return 1
	}

	// Discriminant byte plus 2 (uint16)
	if v <= 1<<16-1 {
		return 3
	}

	// Discriminant byte plus 4 (uint32)
	if v <= 1<<32-1 {
		return 5
	}

	// Discriminant byte plus 8 (uint64)
	return 9
}
