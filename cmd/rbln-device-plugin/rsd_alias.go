package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// buses is sorted, normalized, unique and nonempty (allocationBusIDs).
func rsdAliasName(buses []string) string {
	if len(buses) == 1 {
		return buses[0]
	}
	digest := rsdSetDigest(buses)
	return rsdSetName(digest[:])
}

func rsdSetDigest(buses []string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join(buses, ",")))
}

func rsdSetName(digest []byte) string {
	return "set-" + hex.EncodeToString(digest)
}

func unlinkRSDFile(path string) error {
	err := unix.Unlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// A cached HostPath must never resolve to a group that another phase has
// repurposed. Retire every intersecting different alias before changing the
// hardware. Index multi-device aliases by each member, so {A,B} is retired
// even when the next request contains only B. Singletons have a known path.
// The caller holds the node writer lock. Write the new index before publishing
// the node so a retry after process loss can always find a published alias.
func retireOverlappingAliases(staging string, buses []string) error {
	dir := filepath.Dir(staging)
	var next []byte
	if len(buses) > 1 {
		digest := rsdSetDigest(buses)
		next = digest[:]
	}
	for _, bus := range buses {
		if next != nil {
			if err := unlinkRSDFile(filepath.Join(dir, bus)); err != nil {
				return err
			}
		}
		index := filepath.Join(dir, ".member-"+bus)
		old, err := os.ReadFile(index)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && len(old) != sha256.Size {
			return fmt.Errorf("invalid RSD membership index for %s", bus)
		}
		if bytes.Equal(old, next) {
			continue
		}
		if old != nil {
			if err := unlinkRSDFile(filepath.Join(dir, rsdSetName(old))); err != nil {
				return err
			}
		}
		if next == nil {
			if err := unlinkRSDFile(index); err != nil {
				return err
			}
			continue
		}
		marker := filepath.Join(staging, "membership")
		if err := os.WriteFile(marker, next, 0o600); err != nil {
			return err
		}
		if err := os.Rename(marker, index); err != nil {
			return err
		}
	}
	return nil
}
