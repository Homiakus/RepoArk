package controlplane

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func newID(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + time.Now().UTC().Format("20060102T150405.000000000Z") + "_" + hex.EncodeToString(b[:])
}
